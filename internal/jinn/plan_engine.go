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
