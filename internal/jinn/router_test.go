package jinn

import "testing"

func TestRouteToolsCoreCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		need string
		want string
		risk string
	}{
		{"read file", "read a file", "read_file", "read_only"},
		{"search text", "search text in repo", "search_files", "read_only"},
		{"apply patch", "apply patch", "apply_patch", "mutating"},
		{"run tests", "run tests", "run_shell", "shell"},
		{"rename symbol", "rename symbol", "lsp_query", "read_only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, err := RouteTools(RouteRequest{Need: tt.need, MaxTools: 3, IncludeMutating: boolPtr(true)})
			if err != nil {
				t.Fatalf("RouteTools: %v", err)
			}
			if len(resp.Matches) == 0 {
				t.Fatalf("no matches for %q", tt.need)
			}
			if resp.Matches[0].Name != tt.want {
				t.Fatalf("top match = %s, want %s; matches=%v", resp.Matches[0].Name, tt.want, resp.Matches)
			}
			if resp.Matches[0].Risk != tt.risk {
				t.Fatalf("risk = %s, want %s", resp.Matches[0].Risk, tt.risk)
			}
		})
	}
}

func TestRouteToolsLowSignal(t *testing.T) {
	t.Parallel()
	resp, err := RouteTools(RouteRequest{Need: "please help", IncludeMutating: boolPtr(true)})
	if err != nil {
		t.Fatalf("RouteTools: %v", err)
	}
	if len(resp.Matches) != 0 {
		t.Fatalf("matches = %v, want none", resp.Matches)
	}
	if len(resp.Notes) == 0 {
		t.Fatal("expected explanatory note")
	}
}

func TestRouteToolsIncludeMutatingFalse(t *testing.T) {
	t.Parallel()
	req := RouteRequest{Need: "apply patch", IncludeMutating: boolPtr(false)}
	resp, err := RouteTools(req)
	if err != nil {
		t.Fatalf("RouteTools: %v", err)
	}
	for _, m := range resp.Matches {
		if m.Mutating {
			t.Fatalf("mutating match returned with include_mutating=false: %+v", m)
		}
	}
}

func TestRouteToolsIncludeSchemaOnlyMatches(t *testing.T) {
	t.Parallel()
	resp, err := RouteTools(RouteRequest{Need: "read a file", MaxTools: 2, IncludeSchema: true, IncludeMutating: boolPtr(true)})
	if err != nil {
		t.Fatalf("RouteTools: %v", err)
	}
	if len(resp.Matches) == 0 {
		t.Fatal("expected matches")
	}
	for _, m := range resp.Matches {
		if m.Schema == nil {
			t.Fatalf("match %s missing schema", m.Name)
		}
	}
}

func boolPtr(v bool) *bool {
	return &v
}
