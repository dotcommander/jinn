package jinn

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

//nolint:gocognit,gocyclo,revive,funlen // validation follows the ordered graph invariants reported to callers.
func validatePlan(plan *PlanTree) error {
	if len(plan.Nodes) > maxPlanNodes {
		return planInvalid("plan has %d nodes; maximum is %d", len(plan.Nodes), maxPlanNodes)
	}
	if plan.MaxDepth < 0 || plan.MaxDepth > maxPlanDepth {
		return planInvalid("plan max_depth must be between 0 and %d", maxPlanDepth)
	}
	if len(plan.Nodes) == 0 {
		return &ErrWithSuggestion{
			Err:        errors.New("plan has no nodes"),
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
		if len(n.ID) > 256 {
			return planInvalid("node id exceeds 256 bytes")
		}
		if len(n.Commands) > maxPlanCommands {
			return planInvalid("node %s has too many commands (maximum %d)", n.ID, maxPlanCommands)
		}
		if len(n.Edges) > maxPlanEdges {
			return planInvalid("node %s has too many edges (maximum %d)", n.ID, maxPlanEdges)
		}
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

	// Validate each node's expect with the same condition contract as edges.
	// Unlike edges, expect gates the node's own result, so the low-confidence
	// tier rule (which guards mutating edge targets) does not apply here.
	for ni := range plan.Nodes {
		n := &plan.Nodes[ni]
		if n.Expect != nil {
			if err := validateCondition(n.Expect); err != nil {
				return planInvalid("node %s has invalid expect: %v", n.ID, err)
			}
		}
	}

	// Check edges target known nodes and tier rules.
	for ni := range plan.Nodes {
		n := &plan.Nodes[ni]
		for ei := range n.Edges {
			e := &n.Edges[ei]
			// validateCondition compiles Regex/Extract once here so per-op
			// evaluation reuses the compiled regexp instead of recompiling.
			if err := validateCondition(&e.When); err != nil {
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

//nolint:perfsprint // compact operation validation errors are stable user-facing contract text.
func validatePlanOp(op PlanOp) error {
	hasShell := strings.TrimSpace(op.Shell) != ""
	hasTool := op.Tool != ""
	if hasShell == hasTool {
		return fmt.Errorf("plan op must set exactly one non-empty shell or tool")
	}
	if hasTool {
		descriptor, ok := lookupToolDescriptor(op.Tool)
		if !ok {
			return fmt.Errorf("unknown tool: %s", op.Tool)
		}
		if descriptor.routeRisk == toolRouteRiskNetwork {
			return fmt.Errorf("tool %s is not permitted in run_plan", op.Tool)
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

// validateCondition validates cond and, for match/numeric kinds, stores the
// compiled Regex/Extract on cond for later per-op evaluation.
//
//nolint:funlen,gocognit,gocyclo,revive,perfsprint // condition variants map directly to their wire contract.
func validateCondition(cond *Condition) error {
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
			cond.CompiledExtract = re
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
		re, err := regexp.Compile(cond.Regex)
		if err != nil {
			return fmt.Errorf("match regex: %w", err)
		}
		cond.CompiledRegex = re
		if cond.Op != "" || cond.Value != nil || cond.Path != "" || cond.Extract != "" {
			return fmt.Errorf("match does not accept comparison fields")
		}
	default:
		return fmt.Errorf("unknown condition kind: %s", cond.Kind)
	}
	return nil
}
