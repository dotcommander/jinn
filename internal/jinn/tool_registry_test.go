package jinn

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestToolRegistryNamesAreUniqueAndMatchSchema(t *testing.T) {
	t.Parallel()
	names := registeredToolNames()
	if len(names) == 0 {
		t.Fatal("tool registry is empty")
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if name == "" {
			t.Fatal("tool registry contains an empty name")
		}
		if seen[name] {
			t.Fatalf("tool registry contains duplicate name %q", name)
		}
		seen[name] = true
	}
	if err := validateToolCatalogSchemaParity(); err != nil {
		t.Fatalf("catalog/schema parity: %v", err)
	}
	schemaNames, err := SchemaToolNames()
	if err != nil {
		t.Fatalf("schema tool names: %v", err)
	}
	if !reflect.DeepEqual(names, schemaNames) {
		t.Fatalf("registry names = %v, schema names = %v", names, schemaNames)
	}
}

func TestToolRegistryRouteRisks(t *testing.T) {
	t.Parallel()
	for _, descriptor := range toolCatalog {
		switch descriptor.routeRisk {
		case toolRouteRiskReadOnly, toolRouteRiskMutating, toolRouteRiskShell:
		default:
			t.Errorf("tool %q has unknown route risk %q", descriptor.name, descriptor.routeRisk)
		}
	}
}

func TestToolRegistryFeatureMap(t *testing.T) {
	t.Parallel()
	want := map[string][]string{
		"edit_file":      {"dry_run", "fuzzy_indent", "show_context"},
		"multi_edit":     {"overlap_detection", "show_context", "dry_run"},
		"run_shell":      {"risk_classification", "exit_classification", "dry_run", "stdout_stderr_split", "recovery_hints", "compress_output"},
		"search_files":   {"literal", "context_lines", "format_json", "case_insensitive", "zero_match_reason"},
		"read_file":      {"truncate_strategy", "include_checksum", "tail"},
		"multi_read":     {"per_file_windowing", "partial_success"},
		"write_file":     {"dry_run"},
		"stat_file":      {"encoding_detection", "line_ending_detection", "bom_detection"},
		"list_dir":       {"changed_since"},
		"diff_files":     {"context_lines"},
		"search_replace": {"regex", "capture_groups", "multi_file", "glob_patterns", "replace_all", "dry_run", "case_insensitive", "multiline"},
		"run_plan":       {},
		"lsp_query":      {"definition", "references", "hover", "symbols", "diagnostics", "rename", "symbol_column", "context_lines"},
	}
	got := registeredToolFeatures()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("feature map = %#v, want %#v", got, want)
	}
	if got["run_plan"] == nil {
		t.Fatal("run_plan features must be a non-nil empty slice")
	}
	got["run_shell"][0] = "changed"
	gotAgain := registeredToolFeatures()
	if gotAgain["run_shell"][0] != "risk_classification" {
		t.Fatal("feature map did not return a defensive copy")
	}
}

func TestPlanPhaseOneAllowlistUsesRegisteredReadOnlyTools(t *testing.T) {
	t.Parallel()
	for name := range planPhase1ToolAllowlist {
		descriptor, ok := lookupToolDescriptor(name)
		if !ok {
			t.Errorf("Phase-1 allowlisted tool %q is not registered", name)
			continue
		}
		if descriptor.routeRisk != toolRouteRiskReadOnly {
			t.Errorf("Phase-1 allowlisted tool %q has route risk %q", name, descriptor.routeRisk)
		}
	}
}

func TestToolRegistryDispatcherCompleteness(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e := New(dir, "test")
	t.Cleanup(func() { _ = e.Close() })

	calls := map[string]map[string]interface{}{
		"run_plan":       args("plan", map[string]interface{}{"root": "missing", "nodes": []interface{}{}}),
		"run_shell":      args("command", ""),
		"read_file":      args("path", "missing.txt"),
		"multi_read":     args("files", []interface{}{}),
		"write_file":     args("path", "out.txt", "content", "x", "dry_run", true),
		"edit_file":      args("path", "missing.txt", "old_text", "a", "new_text", "b", "dry_run", true),
		"multi_edit":     args("edits", []interface{}{}, "dry_run", true),
		"apply_patch":    args("patch", "invalid", "dry_run", true),
		"search_files":   args("pattern", "needle", "path", "."),
		"stat_file":      args("path", "missing.txt"),
		"list_dir":       args("path", "."),
		"find_files":     args("pattern", "*.go"),
		"list_tools":     nil,
		"detect_project": nil,
		"memory":         args("action", "invalid"),
		"undo":           args("action", "invalid"),
		"lsp_query":      args("action", "invalid", "path", "missing.go"),
		"diff_files":     args("path_a", "missing-a", "path_b", "missing-b"),
		"search_replace": args("pattern", "x", "replacement", "y", "files", []interface{}{"missing.txt"}, "dry_run", true),
	}

	for _, name := range registeredToolNames() {
		t.Run(name, func(t *testing.T) {
			_, _, err := e.Dispatch(context.Background(), name, calls[name])
			if errors.Is(err, errRegisteredToolNotDispatched) {
				t.Fatalf("registered tool reached no dispatcher: %v", err)
			}
		})
	}
}

func TestToolRegistryUnknownToolContract(t *testing.T) {
	t.Parallel()
	e := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = e.Close() })
	_, _, err := e.Dispatch(context.Background(), "nonexistent", nil)
	if err == nil || err.Error() != "unknown tool: nonexistent" {
		t.Fatalf("unknown tool error = %v", err)
	}
	if errors.Is(err, errRegisteredToolNotDispatched) {
		t.Fatal("unknown caller input was classified as dispatcher drift")
	}
}
