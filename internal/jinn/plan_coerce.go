package jinn

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

func coercePlan(raw any) (*PlanTree, error) {
	// Step 1: normalize raw to map[string]any.
	var m map[string]any
	switch v := raw.(type) {
	case map[string]any:
		m = v
	case string:
		if err := json.Unmarshal([]byte(v), &m); err != nil {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("args[\"plan\"] is a JSON-encoded string but does not contain a valid plan object"),
				Suggestion: "ensure plan is a valid JSON object, either as a native object or a JSON-encoded string",
				Code:       ErrCodePlanCoerceFailed,
			}
		}
	default:
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("args[\"plan\"] must be a JSON object"),
			Suggestion: "ensure plan is a valid JSON object, either as a native object or a JSON-encoded string",
			Code:       ErrCodePlanCoerceFailed,
		}
	}

	// Step 2: if nodes is itself a JSON-encoded string, unwrap it.
	if nodesStr, ok := m["nodes"].(string); ok {
		var nodesSlice []any
		if err := json.Unmarshal([]byte(nodesStr), &nodesSlice); err != nil {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("plan.nodes is a JSON-encoded string but does not decode to an array"),
				Suggestion: "ensure plan.nodes is a JSON array, either as a native array or a JSON-encoded string",
				Code:       ErrCodePlanCoerceFailed,
			}
		}
		m["nodes"] = nodesSlice
	}

	// Step 3: walk nodes -> edges, coerce string-valued "when" fields.
	if nodes, ok := m["nodes"].([]any); ok {
		for _, nodeRaw := range nodes {
			nodeMap, ok := nodeRaw.(map[string]any)
			if !ok {
				continue
			}
			edgesRaw, ok := nodeMap["edges"]
			if !ok {
				continue
			}
			edgesSlice, ok := edgesRaw.([]any)
			if !ok {
				continue
			}
			for _, edgeRaw := range edgesSlice {
				edgeMap, ok := edgeRaw.(map[string]any)
				if !ok {
					continue
				}
				whenVal, ok := edgeMap["when"]
				if !ok {
					continue
				}
				whenStr, isStr := whenVal.(string)
				if !isStr {
					continue
				}
				cond, err := coerceCondition(whenStr)
				if err != nil {
					return nil, err
				}
				// Marshal Condition back to map[string]any and replace.
				condBytes, _ := json.Marshal(cond)
				var condMap map[string]any
				json.Unmarshal(condBytes, &condMap)
				edgeMap["when"] = condMap
			}
		}
	}

	// Step 4: marshal corrected map and decode into *PlanTree.
	corrected, err := json.Marshal(m)
	if err != nil {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("failed to marshal corrected plan: %w", err),
			Suggestion: "internal error; verify plan structure",
			Code:       ErrCodePlanCoerceFailed,
		}
	}
	var tree PlanTree
	if err := json.Unmarshal(corrected, &tree); err != nil {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("failed to decode plan: %w", err),
			Suggestion: "ensure plan matches the expected schema",
			Code:       ErrCodePlanCoerceFailed,
		}
	}

	return &tree, nil
}

var (
	// exit OP N: exit eq 0, exit gte 1, etc.
	exitRe = regexp.MustCompile(`^exit\s+(eq|ne|lt|lte|gt|gte)\s+(\d+)$`)

	// stream =~ /regex/ or stream !~ /regex/
	matchRe = regexp.MustCompile(`^(stdout|stderr)\s+(=~|!~)\s+/([^/]*)/$`)

	// file exists <path> or file missing <path>
	fileExistsRe = regexp.MustCompile(`^file\s+(exists|missing)\s+(.+)$`)
)

func coerceCondition(s string) (Condition, error) {
	s = strings.TrimSpace(s)

	if s == "always" {
		return Condition{Kind: "always"}, nil
	}

	if m := exitRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[2])
		return Condition{Kind: "exitCode", Op: m[1], Value: n}, nil
	}

	if m := matchRe.FindStringSubmatch(s); m != nil {
		return Condition{
			Kind:   "match",
			Stream: m[1],
			Regex:  m[3],
			Negate: m[2] == "!~",
		}, nil
	}

	if m := fileExistsRe.FindStringSubmatch(s); m != nil {
		return Condition{
			Kind:   "fileExists",
			Path:   m[2],
			Negate: m[1] == "missing",
		}, nil
	}

	return Condition{}, &ErrWithSuggestion{
		Err:        fmt.Errorf("plan.nodes[].edges[].when is a string but not a recognized condition shorthand: %s", s),
		Suggestion: "use one of: 'always', 'exit OP N', 'stdout|stderr =~|!~ /regex/', 'file exists|missing <path>', or pass a structured condition object",
		Code:       ErrCodePlanCoerceFailed,
	}
}
