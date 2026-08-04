package jinn

import (
	"context"
	"errors"
)

func (e *Engine) dispatchPlanOps(ctx context.Context, args map[string]interface{}, tool string) (*ToolResult, bool, error) {
	if tool != "run_plan" {
		return nil, false, nil
	}

	rawPlan, ok := args["plan"]
	if !ok {
		return nil, true, &ErrWithSuggestion{
			Err:        errors.New("args[\"plan\"] is required"),
			Suggestion: "provide a plan object with root and nodes",
			Code:       ErrCodeInvalidArgs,
		}
	}

	plan, err := coercePlan(rawPlan)
	if err != nil {
		return nil, true, err
	}

	if validationErr := validatePlan(plan); validationErr != nil {
		return nil, true, validationErr
	}
	if e.shellMode == ShellModeDisabled {
		for _, node := range plan.Nodes {
			for _, op := range node.Commands {
				if op.Shell != "" || op.Tool == "run_shell" {
					return nil, true, &ErrWithSuggestion{Err: errors.New("run_plan shell operations are disabled"), Suggestion: "restart jinn with --shell-mode=sandboxed or remove shell operations", Code: ErrCodePlanInvalid}
				}
			}
		}
	}

	result, err := e.runPlanTree(ctx, plan)
	if err != nil {
		return nil, true, err
	}
	result.Transcript = shapePlanTranscript(result.Transcript)
	recordPlanStats(result)

	return &ToolResult{
		Meta: map[string]any{"plan_run": result},
	}, true, nil
}
