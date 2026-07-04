package jinn

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// --- action methods ---

// lspSendCheck sends an LSP request and reports whether the reply was null/empty.
// When empty is true, callers return their per-action empty message; otherwise
// raw holds the result for unmarshalling.
func (c *lspClient) lspSendCheck(method string, params any) (raw json.RawMessage, empty bool, err error) {
	raw, err = c.sendRequest(method, params)
	if err != nil {
		return nil, false, err
	}
	return raw, string(raw) == "null" || len(raw) == 0, nil
}

func (c *lspClient) definition(absPath string, line, char int, workDir string, pathOK func(string) (string, error)) (string, error) {
	raw, empty, err := c.lspSendCheck("textDocument/definition", tdPos(absPath, line, char))
	if err != nil {
		return "", err
	}
	if empty {
		return "no definition found", nil
	}

	// 3-way unmarshal: []Location → single Location → []LocationLink
	locs := unmarshalLocations(raw)
	if len(locs) == 0 {
		return "no definition found", nil
	}

	return renderLocations(locs, workDir, pathOK, 2), nil
}

func (c *lspClient) references(absPath string, line, char int, workDir string, pathOK func(string) (string, error)) (string, error) {
	type refParams struct {
		TextDocument map[string]string `json:"textDocument"`
		Position     map[string]any    `json:"position"`
		Context      map[string]bool   `json:"context"`
	}
	raw, empty, err := c.lspSendCheck("textDocument/references", refParams{
		TextDocument: map[string]string{"uri": pathToURI(absPath)},
		Position:     lspPosition(line, char),
		Context:      map[string]bool{"includeDeclaration": true},
	})
	if err != nil {
		return "", err
	}
	if empty {
		return "no references found", nil
	}
	var locs []lspLocation
	if err := json.Unmarshal(raw, &locs); err != nil || len(locs) == 0 {
		return "no references found", nil //nolint:nilerr // malformed (non-null) reply or empty array means no references, not a tool error
	}
	const refCap = 100
	truncated := len(locs) > refCap
	total := len(locs)
	if truncated {
		locs = locs[:refCap]
	}

	result := renderLocations(locs, workDir, pathOK, 1)
	if truncated {
		result += fmt.Sprintf("\n[truncated: showing %d of %d]", refCap, total)
	}
	return result, nil
}

func (c *lspClient) hover(absPath string, line, char int) (string, error) {
	raw, empty, err := c.lspSendCheck("textDocument/hover", tdPos(absPath, line, char))
	if err != nil {
		return "", err
	}
	if empty {
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
	Name  string `json:"name"`
	Kind  int    `json:"kind"`
	Range struct {
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

// documentSymbols fetches the file's symbols and normalizes both server response
// shapes — hierarchical DocumentSymbol[] and flat SymbolInformation[] — into one
// []lspDocSymbol tree. Returns nil (and no error) when the server reports none.
func (c *lspClient) documentSymbols(absPath string) ([]lspDocSymbol, error) {
	raw, empty, err := c.lspSendCheck("textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(absPath)},
	})
	if err != nil {
		return nil, err
	}
	if empty {
		return nil, nil
	}

	// Hierarchical DocumentSymbol[] (has selectionRange + children).
	var docSyms []lspDocSymbol
	if err := json.Unmarshal(raw, &docSyms); err == nil && len(docSyms) > 0 {
		return docSyms, nil
	}

	// Flat SymbolInformation[] (has location.uri) — normalize into lspDocSymbol.
	type symInfo struct {
		Name     string      `json:"name"`
		Kind     int         `json:"kind"`
		Location lspLocation `json:"location"`
	}
	var syms []symInfo
	if err := json.Unmarshal(raw, &syms); err == nil && len(syms) > 0 && syms[0].Location.URI != "" {
		docSyms = make([]lspDocSymbol, len(syms))
		for i, s := range syms {
			docSyms[i].Name = s.Name
			docSyms[i].Kind = s.Kind
			docSyms[i].Range.Start.Line = s.Location.Range.Start.Line
			docSyms[i].SelectionRange.Start.Line = s.Location.Range.Start.Line
			docSyms[i].SelectionRange.Start.Character = s.Location.Range.Start.Character
		}
		return docSyms, nil
	}

	return nil, nil
}

func (c *lspClient) symbols(absPath string) (string, error) {
	docSyms, err := c.documentSymbols(absPath)
	if err != nil {
		return "", err
	}
	if len(docSyms) == 0 {
		return "no symbols found", nil
	}
	var sb strings.Builder
	formatSymbolTree(&sb, docSyms, 0)
	return strings.TrimRight(sb.String(), "\n"), nil
}

// resolveSymbolPosition finds the 1-based line/character of a symbol's
// declaration by name from the file's document symbols. It returns a clear
// error when the name matches zero or multiple declarations rather than
// guessing a position.
func (c *lspClient) resolveSymbolPosition(absPath, symbol string) (line, char int, err error) {
	docSyms, err := c.documentSymbols(absPath)
	if err != nil {
		return 0, 0, err
	}
	var matches []lspDocSymbol
	collectSymbolMatches(docSyms, symbol, &matches)

	switch len(matches) {
	case 1:
		sel := matches[0].SelectionRange.Start
		return sel.Line + 1, sel.Character + 1, nil
	case 0:
		return 0, 0, &ErrWithSuggestion{
			Err:        fmt.Errorf("lsp_query: symbol %q not found in the file", symbol),
			Suggestion: "run action \"symbols\" to list the file's declarations, or pass an explicit 'line'",
			Code:       ErrCodeInvalidArgs,
		}
	default:
		lines := make([]string, len(matches))
		for i, m := range matches {
			lines[i] = strconv.Itoa(m.SelectionRange.Start.Line + 1)
		}
		return 0, 0, &ErrWithSuggestion{
			Err:        fmt.Errorf("lsp_query: symbol %q is ambiguous — declared on lines %s", symbol, strings.Join(lines, ", ")),
			Suggestion: "pass an explicit 'line' (with 'symbol' or 'character') to select the intended declaration",
			Code:       ErrCodeInvalidArgs,
		}
	}
}

// collectSymbolMatches recurses the document-symbol tree, appending every symbol
// whose name equals the target.
func collectSymbolMatches(syms []lspDocSymbol, name string, out *[]lspDocSymbol) {
	for _, s := range syms {
		if s.Name == name {
			*out = append(*out, s)
		}
		if len(s.Children) > 0 {
			collectSymbolMatches(s.Children, name, out)
		}
	}
}

func (c *lspClient) diagnostics(absPath string) (string, error) {
	raw, empty, err := c.lspSendCheck("textDocument/diagnostic", map[string]any{
		"textDocument":     map[string]string{"uri": pathToURI(absPath)},
		"identifier":       nil,
		"previousResultId": nil,
	})
	if err != nil {
		return "", err
	}
	if empty {
		return "no diagnostics found", nil
	}

	diagnostics := unmarshalDiagnostics(raw)
	if len(diagnostics) == 0 {
		return "no diagnostics found", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d diagnostic(s) found:\n\n", len(diagnostics))
	for _, diagnostic := range diagnostics {
		line := diagnostic.Range.Start.Line + 1
		character := diagnostic.Range.Start.Character + 1
		severity := diagnosticSeverityName(diagnostic.Severity)
		source := diagnostic.Source
		if source == "" {
			source = "lsp"
		}
		code := diagnosticCodeString(diagnostic.Code)
		if code != "" {
			code = " " + code
		}
		fmt.Fprintf(&sb, "%s:%d:%d: %s %s%s: %s\n", absPath, line, character, severity, source, code, diagnostic.Message)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func unmarshalDiagnostics(raw json.RawMessage) []lspDiagnostic {
	var pull struct {
		Items []lspDiagnostic `json:"items"`
	}
	if err := json.Unmarshal(raw, &pull); err == nil && pull.Items != nil {
		return pull.Items
	}

	var diagnostics []lspDiagnostic
	if err := json.Unmarshal(raw, &diagnostics); err == nil {
		return diagnostics
	}
	return nil
}

func diagnosticSeverityName(severity int) string {
	switch severity {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	default:
		return "diagnostic"
	}
}

func diagnosticCodeString(code any) string {
	switch v := code.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return fmt.Sprintf("%g", v)
	default:
		return fmt.Sprint(v)
	}
}

func (c *lspClient) rename(absPath string, line, char int, newName, workDir string) (string, error) {
	type renameParams struct {
		TextDocument map[string]string `json:"textDocument"`
		Position     map[string]any    `json:"position"`
		NewName      string            `json:"newName"`
	}
	raw, empty, err := c.lspSendCheck("textDocument/rename", renameParams{
		TextDocument: map[string]string{"uri": pathToURI(absPath)},
		Position:     lspPosition(line, char),
		NewName:      newName,
	})
	if err != nil {
		return "", err
	}
	if empty {
		return "no rename changes", nil
	}
	var edit lspWorkspaceEdit
	if err := json.Unmarshal(raw, &edit); err != nil {
		return "", fmt.Errorf("lsp rename parse: %w", err)
	}
	return formatWorkspaceEdit(&edit, workDir), nil
}
