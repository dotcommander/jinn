package jinn

import (
	"context"
	"encoding/json"
	"fmt"
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

// planToolRisk classifies a mutating-node tool op for the Phase 2 risk gate.
// Unlike mutatingActions (mutating_registry.go), this covers file-mutating
// tools (write_file/edit_file/multi_edit/apply_patch/search_replace) in
// addition to memory actions — mutatingActions intentionally excludes them.
func planToolRisk(tool, action string) RiskLevel {
	switch tool {
	case "write_file", "edit_file", "multi_edit", "apply_patch", "search_replace":
		return RiskCaution
	case "memory":
		if action == "gc" {
			return RiskDangerous
		}
		if action == "save" || action == "forget" {
			return RiskCaution
		}
	}
	return RiskSafe
}

func validatePlan(plan *PlanTree) error {
	if len(plan.Nodes) == 0 {
		return &ErrWithSuggestion{
			Err:        fmt.Errorf("plan has no nodes"),
			Suggestion: "fix the plan structure and resubmit — validation runs before any node executes",
			Code:       ErrCodePlanInvalid,
		}
	}

	// Check for duplicate node IDs.
	seen := make(map[string]bool, len(plan.Nodes))
	for _, n := range plan.Nodes {
		if seen[n.ID] {
			return &ErrWithSuggestion{
				Err:        fmt.Errorf("duplicate node id: %s", n.ID),
				Suggestion: "fix the plan structure and resubmit — validation runs before any node executes",
				Code:       ErrCodePlanInvalid,
			}
		}
		seen[n.ID] = true
	}

	if plan.Root == "" {
		return &ErrWithSuggestion{
			Err:        fmt.Errorf("plan has no root node"),
			Suggestion: "fix the plan structure and resubmit — validation runs before any node executes",
			Code:       ErrCodePlanInvalid,
		}
	}

	if !seen[plan.Root] {
		return &ErrWithSuggestion{
			Err:        fmt.Errorf("root node %s not found", plan.Root),
			Suggestion: "fix the plan structure and resubmit — validation runs before any node executes",
			Code:       ErrCodePlanInvalid,
		}
	}

	// Check edges target known nodes and tier rules.
	for _, n := range plan.Nodes {
		for _, e := range n.Edges {
			if !seen[e.To] {
				return &ErrWithSuggestion{
					Err:        fmt.Errorf("edge from %s targets unknown node %s", n.ID, e.To),
					Suggestion: "fix the plan structure and resubmit — validation runs before any node executes",
					Code:       ErrCodePlanInvalid,
				}
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

func (e *Engine) runPlanOp(ctx context.Context, op PlanOp) (PlanOpResult, bool) {
	if op.Shell != "" {
		risk, _ := ClassifyCommand(op.Shell)
		if risk != RiskSafe {
			return PlanOpResult{OK: false, Error: "blocked: shell op risk exceeds Phase 1 read-only allowance"}, true
		}
		text, meta, err := e.runShell(ctx, map[string]interface{}{"command": op.Shell})
		res := PlanOpResult{OK: err == nil, Result: text}
		if err != nil {
			res.Error = err.Error()
		}
		if v, ok := meta["classification"].(string); ok {
			res.Classification = v
		}
		if v, ok := meta["risk"].(string); ok {
			res.Risk = v
		}
		if v, ok := meta["exit_code"].(int); ok {
			res.ExitCode = v
		}
		return res, false
	}
	if op.Tool != "" {
		if !planPhase1ToolAllowlist[op.Tool] {
			return PlanOpResult{OK: false, Error: "blocked: tool op outside Phase 1 read-only allowlist"}, true
		}
		tr, _, err := e.Dispatch(ctx, op.Tool, op.Args)
		res := PlanOpResult{OK: err == nil}
		if err != nil {
			res.Error = err.Error()
		} else if tr != nil {
			res.Result = tr.Text
		}
		return res, false
	}
	return PlanOpResult{OK: false, Error: "plan op must set exactly one of shell or tool"}, true
}

// runMutatingOp executes op on a node with Mutates:true, under the Phase 2
// risk gate: RiskSafe/RiskCaution execute normally; RiskDangerous requires
// BOTH planForce and node.Force, else it blocks. Mirrors runPlanOp's
// (result, blocked) shape so runPlanTree can select either per node.Mutates.
// Deliberately reuses ClassifyCommand/planToolRisk rather than a separate
// destructive-command scanner — command_risk.go's tokenizer already covers
// the false-positive matrix (e.g. redirects to /dev/null) more precisely
// than a regex scan would.
func (e *Engine) runMutatingOp(ctx context.Context, node *PlanNode, planForce bool, op PlanOp) (PlanOpResult, bool) {
	var risk RiskLevel
	switch {
	case op.Shell != "":
		risk, _ = ClassifyCommand(op.Shell)
	case op.Tool != "":
		action, _ := op.Args["action"].(string)
		risk = planToolRisk(op.Tool, action)
	default:
		return PlanOpResult{OK: false, Error: "plan op must set exactly one of shell or tool"}, true
	}

	if risk == RiskDangerous && !(planForce && node.Force) {
		return PlanOpResult{OK: false, Error: "blocked: dangerous mutation requires plan.force and node.force"}, true
	}

	if op.Shell != "" {
		text, meta, err := e.runShell(ctx, map[string]interface{}{"command": op.Shell})
		res := PlanOpResult{OK: err == nil, Result: text}
		if err != nil {
			res.Error = err.Error()
		}
		if v, ok := meta["classification"].(string); ok {
			res.Classification = v
		}
		if v, ok := meta["risk"].(string); ok {
			res.Risk = v
		}
		if v, ok := meta["exit_code"].(int); ok {
			res.ExitCode = v
		}
		return res, false
	}

	tr, _, err := e.Dispatch(ctx, op.Tool, op.Args)
	res := PlanOpResult{OK: err == nil}
	if err != nil {
		res.Error = err.Error()
	} else if tr != nil {
		res.Result = tr.Text
	}
	return res, false
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

func (e *Engine) evaluateCondition(last PlanOpResult, cond Condition, cwd string) (bool, error) {
	switch cond.Kind {
	case "always":
		return true, nil
	case "exitCode":
		var expected int
		switch v := cond.Value.(type) {
		case float64:
			expected = int(v)
		case int:
			expected = v
		default:
			return false, nil
		}
		return compareNumeric(float64(last.ExitCode), float64(expected), cond.Op), nil
	case "fileExists":
		p := cond.Path
		if cwd != "" && !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		_, err := os.Stat(p)
		if err == nil {
			return !cond.Negate, nil
		}
		if os.IsNotExist(err) {
			return cond.Negate, nil
		}
		return false, nil
	case "match":
		re, err := regexp.Compile(cond.Regex)
		if err != nil {
			return false, nil
		}
		// jinn uses a single combined Result string (not separate stdout/stderr streams),
		// so both "stdout" and "stderr" test against last.Result — a deliberate simplification.
		if re.MatchString(last.Result) {
			return !cond.Negate, nil
		}
		return cond.Negate, nil
	case "numeric":
		var s string
		if cond.Extract != "" {
			re, err := regexp.Compile(cond.Extract)
			if err != nil {
				return false, nil
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
		var expected float64
		switch v := cond.Value.(type) {
		case float64:
			expected = v
		case int:
			expected = float64(v)
		default:
			return false, nil
		}
		return compareNumeric(val, expected, cond.Op), nil
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
			return compareNumeric(leafFloat, condFloat, cond.Op), nil
		}
		switch cond.Op {
		case "eq":
			return fmt.Sprintf("%v", leaf) == fmt.Sprintf("%v", cond.Value), nil
		case "ne":
			return fmt.Sprintf("%v", leaf) != fmt.Sprintf("%v", cond.Value), nil
		default:
			return false, nil
		}
	default:
		return false, nil
	}
}

func (e *Engine) runPlanTree(ctx context.Context, plan *PlanTree) (*PlanRunResult, error) {
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

		if node.Mutates {
			transcript = append(transcript, PlanNodeResult{NodeID: currentID, Depth: depth})
			return &PlanRunResult{Transcript: transcript, PathTaken: pathTaken, DepthReached: depth, StoppedReason: StopMutationBlocked, EdgesEvaluated: edgesEvaluated, EdgesMatched: edgesMatched}, nil
		}

		nodeResult := PlanNodeResult{NodeID: currentID, Depth: depth}
		var lastOpResult PlanOpResult
		blocked := false

		if node.Parallel && len(node.Commands) > 1 {
			ops := make([]PlanOpResult, len(node.Commands))
			blockedFlags := make([]bool, len(node.Commands))
			var wg sync.WaitGroup
			for i, op := range node.Commands {
				wg.Add(1)
				go func(i int, op PlanOp) {
					defer wg.Done()
					ops[i], blockedFlags[i] = e.runPlanOp(ctx, op)
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
				opRes, isBlocked := e.runPlanOp(ctx, op)
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
			ok, _ := e.evaluateCondition(lastOpResult, edge.When, plan.Cwd)
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
