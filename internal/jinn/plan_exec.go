package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

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
//
//nolint:revive // execution inputs mirror the plan operation authorization boundary.
func (e *Engine) runMutatingOp(ctx context.Context, cwd string, node *PlanNode, planForce bool, op PlanOp) (PlanOpResult, bool) {
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

	if risk == RiskDangerous && (!planForce || !node.Force) {
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

// planNodeEvaluation belongs to one execution of a node, not its ID or tree.
// JSON is decoded only if an expectation or edge asks for jsonPath.
type planNodeEvaluation struct {
	last        PlanOpResult
	jsonValue   any
	jsonDecoded bool
	jsonValid   bool
}

func (ev *planNodeEvaluation) json() (any, bool) {
	if !ev.jsonDecoded {
		ev.jsonDecoded = true
		raw := ev.last.Stdout
		if raw == "" {
			raw = ev.last.Result
		}
		ev.jsonValid = json.Unmarshal([]byte(raw), &ev.jsonValue) == nil
	}
	return ev.jsonValue, ev.jsonValid
}

func (e *Engine) evaluateCondition(last PlanOpResult, cond Condition) (bool, error) {
	return e.evaluateNodeCondition(&planNodeEvaluation{last: last}, cond)
}

//nolint:funlen,gocognit,gocyclo,nilerr,revive // malformed result data is deliberately a non-matching condition.
func (e *Engine) evaluateNodeCondition(ev *planNodeEvaluation, cond Condition) (bool, error) {
	last := ev.last
	var matched bool
	switch cond.Kind {
	case "always":
		matched = true
	case "exitCode":
		expected, ok := numericPlanValue(cond.Value, true)
		if !ok {
			return false, errors.New("invalid exitCode value")
		}
		matched = compareNumeric(float64(last.ExitCode), expected, cond.Op)
	case "fileExists":
		path, err := e.checkPath(cond.Path)
		if err != nil {
			return false, err
		}
		_, err = e.rootedStat(path)
		switch {
		case err == nil:
			matched = true
		case os.IsNotExist(err):
			matched = false
		default:
			return false, err
		}
	case "match":
		re := cond.CompiledRegex
		if re == nil {
			var err error
			re, err = regexp.Compile(cond.Regex)
			if err != nil {
				return false, err
			}
		}
		stream := last.Result
		if last.Classification != "" || last.Stdout != "" || last.Stderr != "" {
			stream = last.Stdout + last.Stderr
		}
		matched = re.MatchString(stream)
	case "numeric":
		raw := last.Stdout
		if raw == "" {
			raw = last.Result
		}
		var s string
		//nolint:nestif // nested guards express one validation/state ladder
		if cond.Extract != "" {
			re := cond.CompiledExtract
			if re == nil {
				var err error
				re, err = regexp.Compile(cond.Extract)
				if err != nil {
					return false, err
				}
			}
			m := re.FindStringSubmatch(raw)
			if len(m) < 2 {
				return false, nil
			}
			s = m[1]
		} else {
			s = strings.TrimSpace(raw)
		}
		val, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return false, nil
		}
		expected, ok := numericPlanValue(cond.Value, false)
		if !ok {
			return false, errors.New("invalid numeric value")
		}
		matched = compareNumeric(val, expected, cond.Op)
	case "jsonPath":
		v, valid := ev.json()
		if !valid {
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
	root, err := e.root.OpenRoot(rel)
	if err != nil {
		return nil, fmt.Errorf("open plan cwd root: %w", err)
	}
	return &Engine{
		workDir:                      resolved,
		historyOwner:                 e.historyOwnerEngine(),
		version:                      e.version,
		root:                         root,
		shellMode:                    e.shellMode,
		sandboxBinary:                e.sandboxBinary,
		requireMutationPreconditions: e.requireMutationPreconditions,
		tracker:                      e.tracker,
		rgPath:                       e.rgPath,
		execPath:                     e.execPath,
		LSPTimeoutSec:                e.LSPTimeoutSec,
	}, nil
}
