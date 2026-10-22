package jinn

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// lspDocSymbol is DocumentSymbol with Children for hierarchical output.
type lspDocSymbol struct {
	flat  bool
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

	// Decode the discriminating location first: flat symbols also unmarshal
	// as DocumentSymbol but silently lose all coordinates in that shape.
	var docSyms []lspDocSymbol
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
			docSyms[i].flat = true
			docSyms[i].Name = s.Name
			docSyms[i].Kind = s.Kind
			docSyms[i].Range.Start.Line = s.Location.Range.Start.Line
			docSyms[i].SelectionRange.Start.Line = s.Location.Range.Start.Line
			docSyms[i].SelectionRange.Start.Character = s.Location.Range.Start.Character
		}
		return docSyms, nil
	}

	// Hierarchical DocumentSymbol[] (has selectionRange + children).
	if err := json.Unmarshal(raw, &docSyms); err == nil && len(docSyms) > 0 {
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
func (c *lspClient) resolveSymbolPosition(absPath, symbol string) (line, char int, flat bool, err error) {
	docSyms, err := c.documentSymbols(absPath)
	if err != nil {
		return 0, 0, false, err
	}
	var matches []lspDocSymbol
	collectSymbolMatches(docSyms, symbol, &matches)

	switch len(matches) {
	case 1:
		sel := matches[0].SelectionRange.Start
		return sel.Line + 1, sel.Character + 1, matches[0].flat, nil
	case 0:
		return 0, 0, false, &ErrWithSuggestion{
			Err:        fmt.Errorf("lsp_query: symbol %q not found in the file", symbol),
			Suggestion: "run action \"symbols\" to list the file's declarations, or pass an explicit 'line'",
			Code:       ErrCodeInvalidArgs,
		}
	default:
		lines := make([]string, len(matches))
		for i, m := range matches {
			lines[i] = strconv.Itoa(m.SelectionRange.Start.Line + 1)
		}
		return 0, 0, false, &ErrWithSuggestion{
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

// resolveSymbolRequest preserves exact hierarchical selections while resolving
// a flat declaration range to the identifier column in the rooted source.
func (e *Engine) resolveSymbolRequest(client *lspClient, req lspRequest) (lspRequest, error) {
	line, char, flat, err := client.resolveSymbolPosition(req.absPath, req.symbol)
	if err == nil && flat {
		char, err = e.resolveLSPPosition(req.action, req.absPath, line, 0, req.symbol)
	}
	req.line, req.char = line, char
	return req, err
}
