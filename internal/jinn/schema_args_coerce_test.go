package jinn

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestArgsCoerce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		jsonInput   string
		wantArgs    map[string]interface{}
		wantErrCont string
	}{
		{
			name:      "object args -> map populated normally",
			jsonInput: `{"tool":"run_shell","args":{"command":"ls"}}`,
			wantArgs:  map[string]interface{}{"command": "ls"},
		},
		{
			name:      "double-encoded string args -> same map as object form",
			jsonInput: `{"tool":"run_shell","args":"{\"command\":\"ls\",\"timeout\":5}"}`,
			wantArgs:  map[string]interface{}{"command": "ls", "timeout": float64(5)},
		},
		{
			name:        "double-encoded garbage string -> error",
			jsonInput:   `{"tool":"run_shell","args":"not json"}`,
			wantErrCont: "JSON-encoded string",
		},
		{
			name:        "double-encoded array string -> error",
			jsonInput:   `{"tool":"run_shell","args":"[1,2]"}`,
			wantErrCont: "JSON-encoded string",
		},
		{
			name:        "array args -> error",
			jsonInput:   `{"tool":"run_shell","args":[1,2]}`,
			wantErrCont: "must be a JSON object",
		},
		{
			name:      "args omitted -> nil Args",
			jsonInput: `{"tool":"run_shell"}`,
			wantArgs:  nil,
		},
		{
			name:      "args null -> nil Args",
			jsonInput: `{"tool":"run_shell","args":null}`,
			wantArgs:  nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var req Request
			err := json.Unmarshal([]byte(tt.jsonInput), &req)

			if tt.wantErrCont != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrCont)
				}
				if !strings.Contains(err.Error(), tt.wantErrCont) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErrCont, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantArgs == nil && req.Args != nil {
				t.Fatalf("expected nil Args, got %v", req.Args)
			}
			if tt.wantArgs != nil {
				if len(req.Args) != len(tt.wantArgs) {
					t.Fatalf("Args len: want %d, got %d (%v)", len(tt.wantArgs), len(req.Args), req.Args)
				}
				for k, wantV := range tt.wantArgs {
					gotV, ok := req.Args[k]
					if !ok {
						t.Fatalf("missing key %q in Args", k)
					}
					switch want := wantV.(type) {
					case float64:
						gotF, ok := gotV.(float64)
						if !ok || gotF != want {
							t.Fatalf("key %q: want %v (%T), got %v (%T)", k, want, want, gotV, gotV)
						}
					case string:
						gotS, ok := gotV.(string)
						if !ok || gotS != want {
							t.Fatalf("key %q: want %q (%T), got %v (%T)", k, want, want, gotV, gotV)
						}
					default:
						t.Fatalf("unsupported type in wantArgs: %T", want)
					}
				}
			}
		})
	}
}

func TestRunPlanSchemaRequiresOneNonEmptyCommand(t *testing.T) {
	t.Parallel()
	schema, err := LeanSchema()
	if err != nil {
		t.Fatalf("LeanSchema(): %v", err)
	}
	var tools []map[string]any
	if err := json.Unmarshal([]byte(schema), &tools); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	var commands map[string]any
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		if function["name"] != "run_plan" {
			continue
		}
		parameters := function["parameters"].(map[string]any)
		plan := parameters["properties"].(map[string]any)["plan"].(map[string]any)
		nodes := plan["properties"].(map[string]any)["nodes"].(map[string]any)
		node := nodes["items"].(map[string]any)
		commands = node["properties"].(map[string]any)["commands"].(map[string]any)
		break
	}
	if commands == nil {
		t.Fatal("run_plan commands schema not found")
	}
	if got, ok := commands["minItems"].(float64); !ok || got != 1 {
		t.Fatalf("commands.minItems = %v, want 1", commands["minItems"])
	}
	items := commands["items"].(map[string]any)
	oneOf, ok := items["oneOf"].([]any)
	if !ok || len(oneOf) != 2 {
		t.Fatalf("commands.items.oneOf = %v, want two exclusive operation forms", items["oneOf"])
	}
	properties := items["properties"].(map[string]any)
	knownTools, ok := properties["tool"].(map[string]any)["enum"].([]any)
	if !ok || len(knownTools) != len(toolCatalog) {
		t.Fatalf("commands.items.properties.tool.enum = %v, want %d known tools", knownTools, len(toolCatalog))
	}
	planNodes := findRunPlanNodesSchema(t, tools)
	if got, ok := planNodes["minItems"].(float64); !ok || got != 1 {
		t.Fatalf("plan.nodes.minItems = %v, want 1", planNodes["minItems"])
	}
	when := planNodes["items"].(map[string]any)["properties"].(map[string]any)["edges"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["when"].(map[string]any)
	conditions, ok := when["oneOf"].([]any)
	if !ok || len(conditions) != 6 {
		t.Fatalf("when.oneOf = %v, want six condition forms", when["oneOf"])
	}
	var jsonPath map[string]any
	for _, raw := range conditions {
		condition := raw.(map[string]any)
		kind := condition["properties"].(map[string]any)["kind"].(map[string]any)["enum"].([]any)
		if kind[0] == "jsonPath" {
			jsonPath = condition
			break
		}
	}
	if jsonPath == nil {
		t.Fatal("jsonPath condition schema not found")
	}
	value := jsonPath["properties"].(map[string]any)["value"].(map[string]any)
	not, ok := value["not"].(map[string]any)
	if !ok || not["type"] != "null" {
		t.Fatalf("jsonPath value schema = %v, want null exclusion", value)
	}
}

func findRunPlanNodesSchema(t *testing.T, tools []map[string]any) map[string]any {
	t.Helper()
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		if function["name"] != "run_plan" {
			continue
		}
		parameters := function["parameters"].(map[string]any)
		plan := parameters["properties"].(map[string]any)["plan"].(map[string]any)
		return plan["properties"].(map[string]any)["nodes"].(map[string]any)
	}
	t.Fatal("run_plan nodes schema not found")
	return nil
}
