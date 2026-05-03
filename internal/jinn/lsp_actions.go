package jinn

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// --- action methods ---

// unmarshalLocations handles the 3 possible definition response shapes:
// []Location, single Location, or []LocationLink (normalized to []lspLocation).
func unmarshalLocations(raw json.RawMessage) []lspLocation {
	var locs []lspLocation
	if err := json.Unmarshal(raw, &locs); err == nil && len(locs) > 0 {
		return locs
	}
	var single lspLocation
	if err := json.Unmarshal(raw, &single); err == nil && single.URI != "" {
		return []lspLocation{single}
	}
	var links []lspLocationLink
	if err := json.Unmarshal(raw, &links); err == nil && len(links) > 0 {
		locs = make([]lspLocation, len(links))
		for i, l := range links {
			locs[i] = lspLocation{URI: l.TargetURI}
			locs[i].Range.Start.Line = l.TargetRange.Start.Line
			locs[i].Range.Start.Character = l.TargetRange.Start.Character
		}
		return locs
	}
	return nil
}

func (c *lspClient) definition(absPath string, line, char int, workDir string) (string, error) {
	raw, err := c.sendRequest("textDocument/definition", tdPos(absPath, line, char))
	if err != nil {
		return "", err
	}
	if string(raw) == "null" || len(raw) == 0 {
		return "no definition found", nil
	}

	// 3-way unmarshal: []Location → single Location → []LocationLink
	locs := unmarshalLocations(raw)
	if len(locs) == 0 {
		return "no definition found", nil
	}

	fileCache := make(map[string][]string)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d location(s) found:\n\n", len(locs)))
	for _, loc := range locs {
		path := strings.TrimPrefix(loc.URI, "file://")
		rel := path
		if workDir != "" {
			if r, err := filepath.Rel(workDir, path); err == nil {
				rel = r
			}
		}
		fmt.Fprintf(&sb, "%s:%d:%d\n", rel, loc.Range.Start.Line+1, loc.Range.Start.Character+1)
		lines := lspCachedLines(fileCache, path)
		if ctx := lspFormatContext(lines, loc.Range.Start.Line, 2); ctx != "" {
			sb.WriteString(ctx)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func (c *lspClient) references(absPath string, line, char int, workDir string) (string, error) {
	type refParams struct {
		TextDocument map[string]string `json:"textDocument"`
		Position     map[string]any    `json:"position"`
		Context      map[string]bool   `json:"context"`
	}
	raw, err := c.sendRequest("textDocument/references", refParams{
		TextDocument: map[string]string{"uri": pathToURI(absPath)},
		Position:     lspPosition(line, char),
		Context:      map[string]bool{"includeDeclaration": true},
	})
	if err != nil {
		return "", err
	}
	var locs []lspLocation
	if err := json.Unmarshal(raw, &locs); err != nil || len(locs) == 0 {
		return "no references found", nil
	}
	const refCap = 100
	truncated := len(locs) > refCap
	total := len(locs)
	if truncated {
		locs = locs[:refCap]
	}

	fileCache := make(map[string][]string)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d location(s) found:\n\n", len(locs)))
	for _, loc := range locs {
		path := strings.TrimPrefix(loc.URI, "file://")
		rel := path
		if workDir != "" {
			if r, err := filepath.Rel(workDir, path); err == nil {
				rel = r
			}
		}
		fmt.Fprintf(&sb, "%s:%d:%d\n", rel, loc.Range.Start.Line+1, loc.Range.Start.Character+1)
		lines := lspCachedLines(fileCache, path)
		if ctx := lspFormatContext(lines, loc.Range.Start.Line, 1); ctx != "" {
			sb.WriteString(ctx)
			sb.WriteByte('\n')
		}
	}
	result := strings.TrimRight(sb.String(), "\n")
	if truncated {
		result += fmt.Sprintf("\n[truncated: showing %d of %d]", refCap, total)
	}
	return result, nil
}

func (c *lspClient) hover(absPath string, line, char int) (string, error) {
	raw, err := c.sendRequest("textDocument/hover", tdPos(absPath, line, char))
	if err != nil {
		return "", err
	}
	if string(raw) == "null" || len(raw) == 0 {
		return "no hover information found", nil
	}
	var h struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return "", fmt.Errorf("lsp hover parse: %w", err)
	}
	// Try {"kind":..., "value":...} markup content first, then plain string.
	var markup struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(h.Contents, &markup); err == nil && markup.Value != "" {
		return markup.Value, nil
	}
	var plain string
	if err := json.Unmarshal(h.Contents, &plain); err == nil {
		return plain, nil
	}
	return string(h.Contents), nil
}

// lspSymbolKindName maps LSP SymbolKind integers to readable names.
// Only the most common kinds are listed; others fall back to "Symbol".
var lspSymbolKindName = map[int]string{
	1: "File", 2: "Module", 3: "Namespace", 4: "Package", 5: "Class",
	6: "Method", 7: "Property", 8: "Field", 9: "Constructor", 10: "Enum",
	11: "Interface", 12: "Function", 13: "Variable", 14: "Constant",
	15: "String", 16: "Number", 17: "Boolean", 18: "Array", 19: "Object",
	20: "Key", 21: "Null", 22: "EnumMember", 23: "Struct", 24: "Event",
	25: "Operator", 26: "TypeParameter",
}

func symbolKindName(k int) string {
	if name, ok := lspSymbolKindName[k]; ok {
		return name
	}
	return "Symbol"
}

// lspDocSymbol is DocumentSymbol with Children for hierarchical output.
type lspDocSymbol struct {
	Name           string         `json:"name"`
	Kind           int            `json:"kind"`
	Range          struct {
		Start struct {
			Line int `json:"line"`
		} `json:"start"`
	} `json:"range"`
	SelectionRange struct {
		Start struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"start"`
	} `json:"selectionRange"`
	Children []lspDocSymbol `json:"children,omitempty"`
}

func (c *lspClient) symbols(absPath string) (string, error) {
	raw, err := c.sendRequest("textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(absPath)},
	})
	if err != nil {
		return "", err
	}
	if string(raw) == "null" || len(raw) == 0 {
		return "no symbols found", nil
	}

	// Try hierarchical DocumentSymbol[] first (has selectionRange + children).
	var docSyms []lspDocSymbol
	if err := json.Unmarshal(raw, &docSyms); err == nil && len(docSyms) > 0 {
		var sb strings.Builder
		formatSymbolTree(&sb, docSyms, 0)
		return strings.TrimRight(sb.String(), "\n"), nil
	}

	// Fall back to flat SymbolInformation[] (has location.uri).
	type symInfo struct {
		Name     string      `json:"name"`
		Kind     int         `json:"kind"`
		Location lspLocation `json:"location"`
	}
	var syms []symInfo
	if err := json.Unmarshal(raw, &syms); err == nil && len(syms) > 0 && syms[0].Location.URI != "" {
		// Normalize flat symbols into lspDocSymbol for unified formatting.
		docSyms = make([]lspDocSymbol, len(syms))
		for i, s := range syms {
			docSyms[i].Name = s.Name
			docSyms[i].Kind = s.Kind
			docSyms[i].Range.Start.Line = s.Location.Range.Start.Line
			docSyms[i].SelectionRange.Start.Line = s.Location.Range.Start.Line
			docSyms[i].SelectionRange.Start.Character = s.Location.Range.Start.Character
		}
		var sb strings.Builder
		formatSymbolTree(&sb, docSyms, 0)
		return strings.TrimRight(sb.String(), "\n"), nil
	}

	return "no symbols found", nil
}

// formatSymbolTree renders symbols as "{indent}Kind Name (line N)" with
// 2-space indent per depth level for children.
func formatSymbolTree(sb *strings.Builder, syms []lspDocSymbol, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, s := range syms {
		line := s.Range.Start.Line + 1
		fmt.Fprintf(sb, "%s%s %s (line %d)\n", indent, symbolKindName(s.Kind), s.Name, line)
		if len(s.Children) > 0 {
			formatSymbolTree(sb, s.Children, depth+1)
		}
	}
}

func (c *lspClient) rename(absPath string, line, char int, newName, workDir string) (string, error) {
	type renameParams struct {
		TextDocument map[string]string `json:"textDocument"`
		Position     map[string]any    `json:"position"`
		NewName      string            `json:"newName"`
	}
	raw, err := c.sendRequest("textDocument/rename", renameParams{
		TextDocument: map[string]string{"uri": pathToURI(absPath)},
		Position:     lspPosition(line, char),
		NewName:      newName,
	})
	if err != nil {
		return "", err
	}
	if string(raw) == "null" || len(raw) == 0 {
		return "no rename changes", nil
	}
	var edit lspWorkspaceEdit
	if err := json.Unmarshal(raw, &edit); err != nil {
		return "", fmt.Errorf("lsp rename parse: %w", err)
	}
	return formatWorkspaceEdit(&edit, workDir), nil
}
