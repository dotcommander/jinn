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
