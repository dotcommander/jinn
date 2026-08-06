package jinn

import (
	"strings"
	"testing"
)

func TestValidateToolArgsNumericSchemaContracts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		tool    string
		args    map[string]interface{}
		wantErr string
	}{
		{
			name: "search context zero is valid",
			tool: "search_files",
			args: map[string]interface{}{"pattern": "needle", "context_lines": float64(0)},
		},
		{
			name: "search match zero uses documented default",
			tool: "search_files",
			args: map[string]interface{}{"pattern": "needle", "max_matches": float64(0)},
		},
		{
			name:    "list max entries zero violates minimum",
			tool:    "list_dir",
			args:    map[string]interface{}{"max_entries": float64(0)},
			wantErr: "at least 1",
		},
		{
			name:    "find limit zero violates minimum",
			tool:    "find_files",
			args:    map[string]interface{}{"pattern": "*.go", "limit": float64(0)},
			wantErr: "at least 1",
		},
		{
			name:    "negative context is invalid",
			tool:    "search_files",
			args:    map[string]interface{}{"pattern": "needle", "context_lines": float64(-1)},
			wantErr: "non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateToolArgs(tt.tool, tt.args)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateToolArgs returned error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateToolArgs error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}
