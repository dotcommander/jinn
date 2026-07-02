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

	if err := validatePlan(plan); err != nil {
		return nil, true, err
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
