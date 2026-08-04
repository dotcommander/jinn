package jinn

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// unmarshalLocations handles the 3 possible definition response shapes:
// []Location, single Location, or []LocationLink (normalized to []lspLocation).
func unmarshalLocations(raw json.RawMessage) []lspLocation {
	var locs []lspLocation
	if err := json.Unmarshal(raw, &locs); err == nil {
		locs = nonEmptyURILocations(locs)
		if len(locs) > 0 {
			return locs
		}
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

func nonEmptyURILocations(locs []lspLocation) []lspLocation {
	out := locs[:0]
	for _, loc := range locs {
		if loc.URI == "" {
			continue
		}
		out = append(out, loc)
	}
	return out
}

func renderLocationsRooted(locs []lspLocation, engine *Engine, contextRadius int) (string, error) {
	cache := make(map[string][]string)
	return renderLocationsWithReader(locs, engine.workDir, engine.checkPath, contextRadius, func(path string) []string {
		if lines, ok := cache[path]; ok {
			return lines
		}
		data, _, err := engine.readRegularFile(path, maxLSPFileSize)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		cache[path] = lines
		return lines
	})
}

func renderLocationsWithReader(locs []lspLocation, workDir string, pathOK func(string) (string, error), contextRadius int, readLines func(string) []string) (string, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d location(s) found:\n\n", len(locs))
	for _, loc := range locs {
		path, uriErr := fileURIPath(loc.URI)
		if uriErr != nil {
			return "", fmt.Errorf("lsp returned unsupported URI: %w", uriErr)
		}
		safePath, perr := pathOK(path)
		if perr != nil {
			return "", fmt.Errorf("lsp returned path outside workspace: %w", perr)
		}
		rel := path
		if workDir != "" {
			if r, err := filepath.Rel(workDir, path); err == nil {
				rel = r
			}
		}
		fmt.Fprintf(&sb, "%s:%d:%d\n", rel, loc.Range.Start.Line+1, loc.Range.Start.Character+1)
		lines := readLines(safePath)
		if ctx := lspFormatContext(lines, loc.Range.Start.Line, contextRadius); ctx != "" {
			sb.WriteString(ctx)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n"), nil
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
