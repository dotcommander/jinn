package jinn

import (
	"encoding/json"
	"fmt"
	"strings"
)

// --- action methods ---

func (c *lspClient) definition(absPath string, line, char int) (string, error) {
	raw, err := c.sendRequest("textDocument/definition", tdPos(absPath, line, char))
	if err != nil {
		return "", err
	}
	if string(raw) == "null" || len(raw) == 0 {
		return "no definition found", nil
	}
	// Result can be Location or []Location.
	var locs []lspLocation
	if err := json.Unmarshal(raw, &locs); err == nil && len(locs) > 0 {
		return formatLocation(locs[0]), nil
	}
	var loc lspLocation
	if err := json.Unmarshal(raw, &loc); err == nil && loc.URI != "" {
		return formatLocation(loc), nil
	}
	return "no definition found", nil
}

func (c *lspClient) references(absPath string, line, char int) (string, error) {
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
	var sb strings.Builder
	for _, loc := range locs {
		sb.WriteString(formatLocation(loc))
		sb.WriteByte('\n')
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
	// documentSymbol returns []SymbolInformation (has "location") or []DocumentSymbol (has "selectionRange").
	type symInfo struct {
		Name     string      `json:"name"`
		Kind     int         `json:"kind"`
		Location lspLocation `json:"location"`
	}
	var syms []symInfo
	if err := json.Unmarshal(raw, &syms); err == nil && len(syms) > 0 && syms[0].Location.URI != "" {
		var sb strings.Builder
		for _, s := range syms {
			fmt.Fprintf(&sb, "%-15s %-20s (%d:%d)\n", symbolKindName(s.Kind), s.Name,
				s.Location.Range.Start.Line+1, s.Location.Range.Start.Character+1)
		}
		return strings.TrimRight(sb.String(), "\n"), nil
	}
	type docSym struct {
		Name           string `json:"name"`
		Kind           int    `json:"kind"`
		SelectionRange struct {
			Start struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"start"`
		} `json:"selectionRange"`
	}
	var docSyms []docSym
	if err := json.Unmarshal(raw, &docSyms); err == nil && len(docSyms) > 0 {
		var sb strings.Builder
		for _, s := range docSyms {
			fmt.Fprintf(&sb, "%-15s %-20s (%d:%d)\n", symbolKindName(s.Kind), s.Name,
				s.SelectionRange.Start.Line+1, s.SelectionRange.Start.Character+1)
		}
		return strings.TrimRight(sb.String(), "\n"), nil
	}
	return "no symbols found", nil
}
