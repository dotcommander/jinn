package jinn

import (
	"context"
	"fmt"
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
