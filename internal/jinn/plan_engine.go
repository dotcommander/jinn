package jinn

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var planPhase1ToolAllowlist = map[string]bool{
	"read_file":    true,
	"multi_read":   true,
	"list_dir":     true,
	"search_files": true,
	"find_files":   true,
	"stat_file":    true,
	"lsp_query":    true,
}

type planExecutionContextKey struct{}
type planNestedAuthorizationKey struct{}

// planExecutionContext is carried through nested run_plan dispatches. The
// outermost plan establishes the depth budget and dangerous-mutation
// authority. Nested plans may consume that budget and authority, but cannot
// reset or elevate either one.
type planExecutionContext struct {
	depth               int
	maxDepth            int
	dangerousAuthorized bool
}

func planExecutionContextFor(ctx context.Context, plan *PlanTree) *planExecutionContext {
	if parent, ok := ctx.Value(planExecutionContextKey{}).(*planExecutionContext); ok {
		authorized, _ := ctx.Value(planNestedAuthorizationKey{}).(bool)
		return &planExecutionContext{
			depth:               parent.depth + 1,
			maxDepth:            parent.maxDepth,
			dangerousAuthorized: authorized,
		}
	}

	maxDepth := plan.MaxDepth
	if maxDepth == 0 {
		maxDepth = DefaultMaxDepth
	}
	return &planExecutionContext{
		maxDepth:            maxDepth,
		dangerousAuthorized: true,
	}
}

func planPhase1ToolAllowed(op PlanOp) bool {
	if planPhase1ToolAllowlist[op.Tool] {
		return true
	}
	action, _ := op.Args["action"].(string)
	switch op.Tool {
	case "memory":
		return action == "recall" || action == "list"
	case "undo":
		return action == "list" || action == "preview"
	default:
		return false
	}
}

// planToolRisk classifies a known mutating-node tool op for the Phase 2 risk
// gate. Nested run_shell commands use the same classifier as direct shell ops;
// callers cannot supply their own force authority.
func planToolRisk(tool string, args map[string]any) (RiskLevel, error) {
	descriptor, ok := lookupToolDescriptor(tool)
	if !ok {
		return RiskSafe, fmt.Errorf("unknown tool: %s", tool)
	}
	if tool == "run_shell" {
		command, _ := args["command"].(string)
		if strings.TrimSpace(command) == "" {
			return RiskSafe, fmt.Errorf("run_shell command is required")
		}
		risk, _ := ClassifyCommand(command)
		return risk, nil
	}

	switch tool {
	case "write_file", "edit_file", "multi_edit", "apply_patch", "search_replace":
		return RiskCaution, nil
	case "memory":
		action, _ := args["action"].(string)
		if action == "gc" {
			return RiskDangerous, nil
		}
		if action == "save" || action == "forget" {
			return RiskCaution, nil
		}
	case "undo":
		action, _ := args["action"].(string)
		if action == "clear" {
			return RiskDangerous, nil
		}
	}
	if descriptor.routeRisk == toolRouteRiskMutating {
		return RiskCaution, nil
	}
	return RiskSafe, nil
}

func validatePlan(plan *PlanTree) error {
	if len(plan.Nodes) == 0 {
		return &ErrWithSuggestion{
			Err:        fmt.Errorf("plan has no nodes"),
			Suggestion: "fix the plan structure and resubmit — validation runs before any node executes",
			Code:       ErrCodePlanInvalid,
		}
	}

	if plan.Root == "" {
		return planInvalid("plan has no root node")
	}

	// Check for duplicate node IDs and structurally valid operations.
	seen := make(map[string]bool, len(plan.Nodes))
	for _, n := range plan.Nodes {
		if n.ID == "" {
			return planInvalid("node id is required")
		}
		if seen[n.ID] {
			return planInvalid("duplicate node id: %s", n.ID)
		}
		if n.Parallel && n.Mutates {
			return &ErrWithSuggestion{
				Err:        fmt.Errorf("node %s cannot combine parallel and mutates", n.ID),
				Suggestion: "split mutating operations into serial nodes and resubmit — validation runs before any node executes",
				Code:       ErrCodePlanInvalid,
			}
		}
		if len(n.Commands) == 0 {
			return planInvalid("node %s has no commands", n.ID)
		}
		for i, op := range n.Commands {
			if err := validatePlanOp(op); err != nil {
				return planInvalid("node %s command %d: %v", n.ID, i, err)
			}
		}
		seen[n.ID] = true
	}

	if !seen[plan.Root] {
		return planInvalid("root node %s not found", plan.Root)
	}

	// Check edges target known nodes and tier rules.
	for _, n := range plan.Nodes {
		for _, e := range n.Edges {
			if err := validateCondition(e.When); err != nil {
				return planInvalid("edge from %s has invalid condition: %v", n.ID, err)
			}
			if !seen[e.To] {
				return planInvalid("edge from %s targets unknown node %s", n.ID, e.To)
			}
			// Tier rule: low-confidence conditions cannot gate mutating nodes.
			if !HighConfidenceKinds[e.When.Kind] {
				for _, target := range plan.Nodes {
					if target.ID == e.To && target.Mutates {
						return &ErrWithSuggestion{
							Err:        fmt.Errorf("condition kind %s is low-confidence and cannot gate mutating node %s", e.When.Kind, e.To),
							Suggestion: "fix the plan structure and resubmit — validation runs before any node executes",
							Code:       ErrCodePlanInvalid,
						}
					}
				}
			}
		}
	}

	return nil
}

func planInvalid(format string, args ...any) error {
	return &ErrWithSuggestion{
		Err:        fmt.Errorf(format, args...),
		Suggestion: "fix the plan structure and resubmit — validation runs before any node executes",
		Code:       ErrCodePlanInvalid,
	}
}

func validatePlanOp(op PlanOp) error {
	hasShell := strings.TrimSpace(op.Shell) != ""
	hasTool := op.Tool != ""
	if hasShell == hasTool {
		return fmt.Errorf("plan op must set exactly one non-empty shell or tool")
	}
	if hasTool {
		if _, ok := lookupToolDescriptor(op.Tool); !ok {
			return fmt.Errorf("unknown tool: %s", op.Tool)
		}
	}
	return nil
}

func validPlanOperator(op string) bool {
	switch op {
	case "eq", "ne", "lt", "lte", "gt", "gte":
		return true
	default:
		return false
	}
}

func numericPlanValue(value any, integer bool) (float64, bool) {
	var number float64
	switch v := value.(type) {
	case int:
		number = float64(v)
	case float64:
		number = v
	default:
		return 0, false
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || (integer && math.Trunc(number) != number) {
		return 0, false
	}
	return number, true
}

func validateCondition(cond Condition) error {
	switch cond.Kind {
	case "always":
		if cond.Op != "" || cond.Value != nil || cond.Path != "" || cond.Extract != "" || cond.Regex != "" || cond.Stream != "" {
			return fmt.Errorf("always does not accept comparison fields")
		}
		return nil
	case "exitCode":
		if !validPlanOperator(cond.Op) {
			return fmt.Errorf("exitCode requires a supported operator")
		}
		if _, ok := numericPlanValue(cond.Value, true); !ok {
			return fmt.Errorf("exitCode requires an integer value")
		}
		if cond.Path != "" || cond.Extract != "" || cond.Regex != "" || cond.Stream != "" {
			return fmt.Errorf("exitCode does not accept path, extract, regex, or stream")
		}
	case "fileExists":
		if strings.TrimSpace(cond.Path) == "" {
			return fmt.Errorf("fileExists requires a path")
		}
		if cond.Op != "" || cond.Value != nil || cond.Extract != "" || cond.Regex != "" || cond.Stream != "" {
			return fmt.Errorf("fileExists does not accept comparison fields")
		}
	case "numeric":
		if !validPlanOperator(cond.Op) {
			return fmt.Errorf("numeric requires a supported operator")
		}
		if _, ok := numericPlanValue(cond.Value, false); !ok {
			return fmt.Errorf("numeric requires a numeric value")
		}
		if cond.Extract != "" {
			re, err := regexp.Compile(cond.Extract)
			if err != nil {
				return fmt.Errorf("numeric extract regex: %w", err)
			}
			if re.NumSubexp() == 0 {
				return fmt.Errorf("numeric extract regex requires a capture group")
			}
		}
		if cond.Path != "" || cond.Regex != "" || cond.Stream != "" {
			return fmt.Errorf("numeric does not accept path, regex, or stream")
		}
	case "jsonPath":
		if strings.TrimSpace(cond.Path) == "" {
			return fmt.Errorf("jsonPath requires a path")
		}
		if !validPlanOperator(cond.Op) {
			return fmt.Errorf("jsonPath requires a supported operator")
		}
		if cond.Value == nil {
			return fmt.Errorf("jsonPath requires a value")
		}
		if cond.Op != "eq" && cond.Op != "ne" {
			if _, ok := numericPlanValue(cond.Value, false); !ok {
				return fmt.Errorf("jsonPath %s requires a numeric value", cond.Op)
			}
		}
		if cond.Extract != "" || cond.Regex != "" || cond.Stream != "" {
			return fmt.Errorf("jsonPath does not accept extract, regex, or stream")
		}
	case "match":
		if cond.Stream != "stdout" && cond.Stream != "stderr" {
			return fmt.Errorf("match requires stream stdout or stderr")
		}
		if _, err := regexp.Compile(cond.Regex); err != nil {
			return fmt.Errorf("match regex: %w", err)
		}
		if cond.Op != "" || cond.Value != nil || cond.Path != "" || cond.Extract != "" {
			return fmt.Errorf("match does not accept comparison fields")
		}
	default:
		return fmt.Errorf("unknown condition kind: %s", cond.Kind)
	}
	return nil
}

func planToolResult(tr *ToolResult, err error) PlanOpResult {
	res := PlanOpResult{OK: err == nil}
	if err != nil {
		res.Error = err.Error()
		res.ExitCode = 1
		return res
	}
	if tr != nil {
		res.Result = tr.Text
		if nested, ok := nestedPlanResult(tr); ok {
			if encoded, marshalErr := json.Marshal(nested); marshalErr == nil {
				res.Result = string(encoded)
			}
			if !planRunSucceeded(nested) {
				res.OK = false
				res.ExitCode = 1
				res.Error = fmt.Sprintf("nested run_plan stopped: %s", nested.StoppedReason)
			}
		}
	}
	return res
}

func nestedPlanResult(tr *ToolResult) (*PlanRunResult, bool) {
	if tr == nil || tr.Meta == nil {
		return nil, false
	}
	switch value := tr.Meta["plan_run"].(type) {
	case *PlanRunResult:
		return value, value != nil
	case PlanRunResult:
		return &value, true
	default:
		return nil, false
	}
}

func planRunSucceeded(result *PlanRunResult) bool {
	if result == nil || result.StoppedReason != StopLeaf {
		return false
	}
	for _, node := range result.Transcript {
		for _, op := range node.Ops {
			if !op.OK {
				return false
			}
		}
	}
	return true
}

func planShellResult(text string, meta map[string]any, err error, command string) PlanOpResult {
	res := PlanOpResult{OK: err == nil, Result: text}
	if err != nil {
		res.Error = err.Error()
		res.ExitCode = 1
	}
	if v, ok := meta["exit_code"].(int); ok {
		res.ExitCode = v
		class, reason := classifyExitCode(extractArgv0(command), v)
		res.Classification = string(class)
		res.Result = rewritePlanShellClassification(res.Result, res.Classification, reason)
	}
	if strings.TrimSpace(command) != "" {
		risk, _ := ClassifyCommand(command)
		res.Risk = risk.String()
	} else if v, ok := meta["risk"].(string); ok {
		res.Risk = v
	}
	return res
}

func rewritePlanShellClassification(result, classification, reason string) string {
	const marker = "\n[classification:"
	idx := strings.LastIndex(result, marker)
	if idx < 0 {
		return result
	}
	end := strings.Index(result[idx:], "]")
	if end < 0 {
		return result
	}
	end += idx + 1
	return result[:idx] + fmt.Sprintf("\n[classification: %s — %s]", classification, reason) + result[end:]
}

func blockedPlanOp(message string) (PlanOpResult, bool) {
	return PlanOpResult{OK: false, Error: message, ExitCode: 1}, true
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func planShellCommand(cwd, command string) string {
	return "cd -- " + shellQuote(cwd) + " && " + command
}

func clonePlanArgs(args map[string]any) map[string]any {
	cloned := make(map[string]any, len(args)+1)
	for key, value := range args {
		cloned[key] = value
	}
	return cloned
}

// runPlanShell fixes the shell's cwd without changing the shared Engine. The
// cwd is already resolved and sandbox-validated by scopedPlanEngine.
func (e *Engine) runPlanShell(ctx context.Context, cwd, command string, args map[string]any, force bool) (string, map[string]any, error) {
	shellArgs := clonePlanArgs(args)
	shellArgs["command"] = planShellCommand(cwd, command)
	// Only the plan's outer double-force decision can authorize danger. A
	// caller-supplied nested force must never bypass that authority boundary.
	shellArgs["force"] = force
	return e.runShell(ctx, shellArgs)
}

func (e *Engine) runPlanOp(ctx context.Context, cwd string, op PlanOp) (PlanOpResult, bool) {
	if op.Shell != "" {
		risk, _ := ClassifyCommand(op.Shell)
		if risk != RiskSafe {
			return blockedPlanOp("blocked: shell op risk exceeds Phase 1 read-only allowance")
		}
		text, meta, err := e.runPlanShell(ctx, cwd, op.Shell, nil, false)
		return planShellResult(text, meta, err, op.Shell), false
	}
	if op.Tool != "" {
		if !planPhase1ToolAllowed(op) {
			return blockedPlanOp("blocked: tool op outside Phase 1 read-only allowlist")
		}
		tr, _, err := e.Dispatch(ctx, op.Tool, op.Args)
		return planToolResult(tr, err), false
	}
	return blockedPlanOp("plan op must set exactly one of shell or tool")
}

// runMutatingOp executes op on a node with Mutates:true, under the Phase 2
// risk gate: RiskSafe/RiskCaution execute normally; RiskDangerous requires
// BOTH planForce and node.Force, else it blocks. Mirrors runPlanOp's
// (result, blocked) shape so runPlanTree can select either per node.Mutates.
// Deliberately reuses ClassifyCommand/planToolRisk rather than a separate
// destructive-command scanner — command_risk.go's tokenizer already covers
// the false-positive matrix (e.g. redirects to /dev/null) more precisely
// than a regex scan would.
func (e *Engine) runMutatingOp(ctx context.Context, cwd string, node *PlanNode, planForce bool, opCtx *planExecutionContext, op PlanOp) (PlanOpResult, bool) {
	var risk RiskLevel
	switch {
	case op.Shell != "":
		risk, _ = ClassifyCommand(op.Shell)
	case op.Tool != "":
		var err error
		risk, err = planToolRisk(op.Tool, op.Args)
		if err != nil {
			return blockedPlanOp(err.Error())
		}
	default:
		return blockedPlanOp("plan op must set exactly one of shell or tool")
	}

	if risk == RiskDangerous && !(planForce && node.Force) {
		return blockedPlanOp("blocked: dangerous mutation requires plan.force and node.force")
	}

	if op.Shell != "" {
		text, meta, err := e.runPlanShell(ctx, cwd, op.Shell, nil, risk == RiskDangerous && planForce && node.Force)
		return planShellResult(text, meta, err, op.Shell), false
	}

	if op.Tool == "run_shell" {
		command, _ := op.Args["command"].(string)
		text, meta, err := e.runPlanShell(ctx, cwd, command, op.Args, risk == RiskDangerous && planForce && node.Force)
		return planShellResult(text, meta, err, command), false
	}

	if op.Tool == "run_plan" {
		// A nested plan inherits only the current node's already-authorized
		// dangerous capability. Its own force fields can further restrict that
		// capability, but cannot create it.
		nestedCtx := context.WithValue(ctx, planNestedAuthorizationKey{}, planForce && node.Force)
		tr, _, err := e.Dispatch(nestedCtx, op.Tool, op.Args)
		return planToolResult(tr, err), false
	}

	tr, _, err := e.Dispatch(ctx, op.Tool, op.Args)
	return planToolResult(tr, err), false
}

func compareNumeric(a, b float64, op string) bool {
	switch op {
	case "eq":
		return a == b
	case "ne":
		return a != b
	case "lt":
		return a < b
	case "lte":
		return a <= b
	case "gt":
		return a > b
	case "gte":
		return a >= b
	default:
		return false
	}
}

func negateCondition(matched, negate bool) bool {
	if negate {
		return !matched
	}
	return matched
}

func (e *Engine) evaluateCondition(last PlanOpResult, cond Condition) (bool, error) {
	var matched bool
	switch cond.Kind {
	case "always":
		matched = true
	case "exitCode":
		expected, ok := numericPlanValue(cond.Value, true)
		if !ok {
			return false, fmt.Errorf("invalid exitCode value")
		}
		matched = compareNumeric(float64(last.ExitCode), expected, cond.Op)
	case "fileExists":
		path, err := e.checkPath(cond.Path)
		if err != nil {
			return false, err
		}
		_, err = os.Stat(path)
		if err == nil {
			matched = true
		} else if os.IsNotExist(err) {
			matched = false
		} else {
			return false, err
		}
	case "match":
		re, err := regexp.Compile(cond.Regex)
		if err != nil {
			return false, err
		}
		// jinn uses a single combined Result string (not separate stdout/stderr streams),
		// so both "stdout" and "stderr" test against last.Result — a deliberate simplification.
		matched = re.MatchString(last.Result)
	case "numeric":
		var s string
		if cond.Extract != "" {
			re, err := regexp.Compile(cond.Extract)
			if err != nil {
				return false, err
			}
			m := re.FindStringSubmatch(last.Result)
			if len(m) < 2 {
				return false, nil
			}
			s = m[1]
		} else {
			s = strings.TrimSpace(last.Result)
		}
		val, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return false, nil
		}
		expected, ok := numericPlanValue(cond.Value, false)
		if !ok {
			return false, fmt.Errorf("invalid numeric value")
		}
		matched = compareNumeric(val, expected, cond.Op)
	case "jsonPath":
		var v any
		if err := json.Unmarshal([]byte(last.Result), &v); err != nil {
			return false, nil
		}
		segments := strings.Split(cond.Path, ".")
		cur := v
		for _, seg := range segments {
			switch cv := cur.(type) {
			case map[string]any:
				val, ok := cv[seg]
				if !ok {
					return false, nil
				}
				cur = val
			case []any:
				idx, err := strconv.Atoi(seg)
				if err != nil || idx < 0 || idx >= len(cv) {
					return false, nil
				}
				cur = cv[idx]
			default:
				return false, nil
			}
		}
		leaf := cur
		var leafFloat, condFloat float64
		leafIsNum, condIsNum := false, false
		switch lv := leaf.(type) {
		case float64:
			leafFloat = lv
			leafIsNum = true
		case int:
			leafFloat = float64(lv)
			leafIsNum = true
		}
		switch cv := cond.Value.(type) {
		case float64:
			condFloat = cv
			condIsNum = true
		case int:
			condFloat = float64(cv)
			condIsNum = true
		}
		if leafIsNum && condIsNum {
			matched = compareNumeric(leafFloat, condFloat, cond.Op)
		} else {
			switch cond.Op {
			case "eq":
				matched = jsonValueEqual(leaf, cond.Value)
			case "ne":
				matched = !jsonValueEqual(leaf, cond.Value)
			default:
				return false, nil
			}
		}
	default:
		return false, fmt.Errorf("unknown condition kind: %s", cond.Kind)
	}
	return negateCondition(matched, cond.Negate), nil
}

func jsonValueEqual(left, right any) bool {
	leftNumber, leftIsNumber := numericPlanValue(left, false)
	rightNumber, rightIsNumber := numericPlanValue(right, false)
	if leftIsNumber || rightIsNumber {
		return leftIsNumber && rightIsNumber && leftNumber == rightNumber
	}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

// scopedPlanEngine resolves plan.cwd within the original Engine sandbox. A
// fresh lightweight Engine keeps cwd-sensitive tool behavior local to this
// plan while retaining the shared tracker used for stale-write detection.
func (e *Engine) scopedPlanEngine(cwd string) (*Engine, error) {
	if cwd == "" {
		return e, nil
	}
	resolved := cwd
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(e.workDir, resolved)
	}
	resolved, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return nil, fmt.Errorf("resolve plan cwd: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat plan cwd: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("plan cwd is not a directory: %s", cwd)
	}
	rel, err := filepath.Rel(e.workDir, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("plan cwd is outside working directory: %s", cwd)
	}
	return &Engine{
		workDir:       resolved,
		version:       e.version,
		tracker:       e.tracker,
		rgPath:        e.rgPath,
		fdPath:        e.fdPath,
		LSPTimeoutSec: e.LSPTimeoutSec,
	}, nil
}

func (e *Engine) runPlanTree(ctx context.Context, plan *PlanTree) (*PlanRunResult, error) {
	opCtx := planExecutionContextFor(ctx, plan)
	if opCtx.depth > opCtx.maxDepth {
		return &PlanRunResult{
			DepthReached:  opCtx.depth,
			StoppedReason: StopMaxDepth,
		}, nil
	}
	ctx = context.WithValue(ctx, planExecutionContextKey{}, opCtx)

	planEngine, err := e.scopedPlanEngine(plan.Cwd)
	if err != nil {
		return nil, err
	}
	if planEngine != e {
		defer planEngine.Close()
	}
	maxDepth := plan.MaxDepth
	if maxDepth == 0 {
		maxDepth = DefaultMaxDepth
	}
	byID := make(map[string]*PlanNode, len(plan.Nodes))
	for i := range plan.Nodes {
		byID[plan.Nodes[i].ID] = &plan.Nodes[i]
	}
	depth := 0
	currentID := plan.Root
	var pathTaken []string
	var transcript []PlanNodeResult
	edgesEvaluated, edgesMatched := 0, 0

	for {
		if err := ctx.Err(); err != nil {
			return &PlanRunResult{Transcript: transcript, PathTaken: pathTaken, DepthReached: depth, StoppedReason: StopAborted, EdgesEvaluated: edgesEvaluated, EdgesMatched: edgesMatched}, nil
		}
		if depth > maxDepth {
			return &PlanRunResult{Transcript: transcript, PathTaken: pathTaken, DepthReached: depth, StoppedReason: StopMaxDepth, EdgesEvaluated: edgesEvaluated, EdgesMatched: edgesMatched}, nil
		}
		node := byID[currentID]
		pathTaken = append(pathTaken, currentID)

		nodeResult := PlanNodeResult{NodeID: currentID, Depth: depth}
		var lastOpResult PlanOpResult
		blocked := false

		runOp := func(op PlanOp) (PlanOpResult, bool) { return planEngine.runPlanOp(ctx, planEngine.workDir, op) }
		if node.Mutates {
			runOp = func(op PlanOp) (PlanOpResult, bool) {
				return planEngine.runMutatingOp(ctx, planEngine.workDir, node, plan.Force && opCtx.dangerousAuthorized, opCtx, op)
			}
		}

		if node.Parallel && len(node.Commands) > 1 {
			ops := make([]PlanOpResult, len(node.Commands))
			blockedFlags := make([]bool, len(node.Commands))
			var wg sync.WaitGroup
			for i, op := range node.Commands {
				wg.Add(1)
				go func(i int, op PlanOp) {
					defer wg.Done()
					ops[i], blockedFlags[i] = runOp(op)
				}(i, op)
			}
			wg.Wait()
			for i, b := range blockedFlags {
				nodeResult.Ops = append(nodeResult.Ops, ops[i])
				lastOpResult = ops[i]
				if b {
					blocked = true
					break
				}
			}
		} else {
			for _, op := range node.Commands {
				opRes, isBlocked := runOp(op)
				nodeResult.Ops = append(nodeResult.Ops, opRes)
				lastOpResult = opRes
				if isBlocked {
					blocked = true
					break
				}
			}
		}

		transcript = append(transcript, nodeResult)
		if blocked {
			return &PlanRunResult{Transcript: transcript, PathTaken: pathTaken, DepthReached: depth, StoppedReason: StopMutationBlocked, EdgesEvaluated: edgesEvaluated, EdgesMatched: edgesMatched}, nil
		}

		matched := false
		for _, edge := range node.Edges {
			edgesEvaluated++
			ok, err := planEngine.evaluateCondition(lastOpResult, edge.When)
			if err != nil {
				return &PlanRunResult{Transcript: transcript, PathTaken: pathTaken, DepthReached: depth, StoppedReason: StopError, EdgesEvaluated: edgesEvaluated, EdgesMatched: edgesMatched}, nil
			}
			if ok {
				edgesMatched++
				matched = true
				currentID = edge.To
				depth++
				break
			}
		}
		if !matched {
			reason := StopNoEdgeMatch
			if len(node.Edges) == 0 {
				reason = StopLeaf
			}
			return &PlanRunResult{Transcript: transcript, PathTaken: pathTaken, DepthReached: depth, StoppedReason: reason, EdgesEvaluated: edgesEvaluated, EdgesMatched: edgesMatched}, nil
		}
	}
}

func shapePlanTranscript(nodes []PlanNodeResult) []PlanNodeResult {
	total := 0
	for _, n := range nodes {
		for _, op := range n.Ops {
			total += len(op.Result)
		}
	}
	if total <= PlanTranscriptMaxBytes {
		return nodes
	}
	result := make([]PlanNodeResult, len(nodes))
	copy(result, nodes)
	for i := range result {
		nodeTotal := 0
		for _, op := range result[i].Ops {
			nodeTotal += len(op.Result)
		}
		newOps := make([]PlanOpResult, len(result[i].Ops))
		for j, op := range result[i].Ops {
			newOps[j] = op
			newOps[j].Result = "[omitted: transcript cap exceeded]"
		}
		result[i].Ops = newOps
		total -= nodeTotal
		if total <= PlanTranscriptMaxBytes {
			break
		}
	}
	return result
}
