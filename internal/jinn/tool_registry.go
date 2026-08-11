package jinn

import "fmt"

type toolRouteRisk string

const (
	toolRouteRiskReadOnly toolRouteRisk = "read_only"
	toolRouteRiskMutating toolRouteRisk = "mutating"
	toolRouteRiskShell    toolRouteRisk = "shell"
	toolRouteRiskNetwork  toolRouteRisk = "network"
)

type toolDescriptor struct {
	name      string
	features  []string
	routeRisk toolRouteRisk
}

func (descriptor toolDescriptor) mayMutate() bool {
	return descriptor.routeRisk == toolRouteRiskMutating || descriptor.routeRisk == toolRouteRiskShell
}

// toolCatalog is the runtime registry. Its order must match the embedded wire
// schema so discovery output remains stable.
var toolCatalog = [...]toolDescriptor{
	{name: "run_plan", features: []string{}, routeRisk: toolRouteRiskMutating},
	{name: "run_shell", features: []string{"risk_classification", "exit_classification", "dry_run", "stdout_stderr_split", "recovery_hints", "compress_output"}, routeRisk: toolRouteRiskShell},
	{name: "read_file", features: []string{"truncate_strategy", "include_checksum", "tail"}, routeRisk: toolRouteRiskReadOnly},
	{name: "multi_read", features: []string{"per_file_windowing", "partial_success"}, routeRisk: toolRouteRiskReadOnly},
	{name: "write_file", features: []string{"dry_run"}, routeRisk: toolRouteRiskMutating},
	{name: "edit_file", features: []string{"dry_run", "fuzzy_indent", "show_context"}, routeRisk: toolRouteRiskMutating},
	{name: "multi_edit", features: []string{"overlap_detection", "show_context", "dry_run"}, routeRisk: toolRouteRiskMutating},
	{name: "apply_patch", routeRisk: toolRouteRiskMutating},
	{name: "search_files", features: []string{"literal", "context_lines", "format_json", "case_insensitive", "zero_match_reason"}, routeRisk: toolRouteRiskReadOnly},
	{name: "stat_file", features: []string{"encoding_detection", "line_ending_detection", "bom_detection"}, routeRisk: toolRouteRiskReadOnly},
	{name: "list_dir", features: []string{"changed_since"}, routeRisk: toolRouteRiskReadOnly},
	{name: "find_files", routeRisk: toolRouteRiskReadOnly},
	{name: "list_tools", routeRisk: toolRouteRiskReadOnly},
	{name: "detect_project", routeRisk: toolRouteRiskReadOnly},
	{name: "memory", routeRisk: toolRouteRiskMutating},
	{name: "undo", routeRisk: toolRouteRiskMutating},
	{name: "lsp_query", features: []string{"definition", "references", "hover", "symbols", "diagnostics", "rename", "symbol_column", "context_lines"}, routeRisk: toolRouteRiskReadOnly},
	{name: "diff_files", features: []string{"context_lines"}, routeRisk: toolRouteRiskReadOnly},
	{name: "search_replace", features: []string{"regex", "capture_groups", "multi_file", "glob_patterns", "replace_all", "dry_run", "case_insensitive", "multiline"}, routeRisk: toolRouteRiskMutating},
	{name: webFetchTool, features: []string{"url_safety", "reader", "render", "cache"}, routeRisk: toolRouteRiskNetwork},
	{name: webSearchTool, features: []string{"brave", "exa", "domain_filters", "result_limit"}, routeRisk: toolRouteRiskNetwork},
}

func lookupToolDescriptor(name string) (toolDescriptor, bool) {
	for _, descriptor := range toolCatalog {
		if descriptor.name == name {
			descriptor.features = cloneStrings(descriptor.features)
			return descriptor, true
		}
	}
	return toolDescriptor{}, false
}

func (e *Engine) lookupToolDescriptor(name string) (toolDescriptor, bool) {
	descriptor, ok := lookupToolDescriptor(name)
	if ok && descriptor.routeRisk == toolRouteRiskShell && e.shellMode == ShellModeDisabled {
		return toolDescriptor{}, false
	}
	return descriptor, ok
}

func registeredToolNames() []string {
	names := make([]string, len(toolCatalog))
	for i, descriptor := range toolCatalog {
		names[i] = descriptor.name
	}
	return names
}

// ReadOnlyToolNames returns the canonical tool allowlist for callers that need
// to expose a strictly read-only execution surface. The returned slice is a
// defensive copy and follows the stable catalog order.
func ReadOnlyToolNames() []string {
	names := make([]string, 0, len(toolCatalog))
	for _, descriptor := range toolCatalog {
		if descriptor.routeRisk == toolRouteRiskReadOnly {
			names = append(names, descriptor.name)
		}
	}
	return names
}

// NetworkToolNames returns the stable, non-mutating open-world tool allowlist.
func NetworkToolNames() []string {
	names := make([]string, 0, 2)
	for _, descriptor := range toolCatalog {
		if descriptor.routeRisk == toolRouteRiskNetwork {
			names = append(names, descriptor.name)
		}
	}
	return names
}

func (e *Engine) registeredToolNames() []string {
	names := make([]string, 0, len(toolCatalog))
	for _, descriptor := range toolCatalog {
		if descriptor.routeRisk == toolRouteRiskShell && e.shellMode == ShellModeDisabled {
			continue
		}
		names = append(names, descriptor.name)
	}
	return names
}

func registeredToolFeatures() map[string][]string {
	features := make(map[string][]string)
	for _, descriptor := range toolCatalog {
		if descriptor.features != nil {
			features[descriptor.name] = cloneStrings(descriptor.features)
		}
	}
	return features
}

func (e *Engine) registeredToolFeatures() map[string][]string {
	features := registeredToolFeatures()
	if e.shellMode == ShellModeDisabled {
		delete(features, "run_shell")
	}
	return features
}

func validateToolCatalogSchemaParity() error {
	seen := make(map[string]bool, len(toolCatalog))
	for i, descriptor := range toolCatalog {
		if descriptor.name == "" {
			return fmt.Errorf("invalid tool catalog entry at index %d: empty name", i)
		}
		if seen[descriptor.name] {
			return fmt.Errorf("invalid tool catalog entry at index %d: duplicate name %q", i, descriptor.name)
		}
		seen[descriptor.name] = true
		switch descriptor.routeRisk {
		case toolRouteRiskReadOnly, toolRouteRiskMutating, toolRouteRiskShell, toolRouteRiskNetwork:
		default:
			return fmt.Errorf("invalid tool catalog entry %q: unknown route risk %q", descriptor.name, descriptor.routeRisk)
		}
	}

	schemaNames, err := SchemaToolNames()
	if err != nil {
		return err
	}
	catalogNames := registeredToolNames()
	if len(catalogNames) != len(schemaNames) {
		return fmt.Errorf("tool catalog/schema mismatch: catalog has %d tools, schema has %d", len(catalogNames), len(schemaNames))
	}
	for i, name := range catalogNames {
		if name != schemaNames[i] {
			return fmt.Errorf("tool catalog/schema mismatch at index %d: catalog %q, schema %q", i, name, schemaNames[i])
		}
	}
	return nil
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}
