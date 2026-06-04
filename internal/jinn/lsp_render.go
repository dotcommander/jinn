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

// renderLocations formats a list of LSP locations as "file:line:col" headers with
// surrounding source context (contextRadius lines on each side).
func renderLocations(locs []lspLocation, workDir string, pathOK func(string) (string, error), contextRadius int) string {
	fileCache := make(map[string][]string)
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d location(s) found:\n\n", len(locs))
	for _, loc := range locs {
		path := strings.TrimPrefix(loc.URI, "file://")
		rel := path
		if workDir != "" {
			if r, err := filepath.Rel(workDir, path); err == nil {
				rel = r
			}
		}
		fmt.Fprintf(&sb, "%s:%d:%d\n", rel, loc.Range.Start.Line+1, loc.Range.Start.Character+1)
		safePath, perr := pathOK(path)
		if perr != nil {
			continue // server-supplied location escapes sandbox — skip context, keep the file:line header
		}
		lines := lspCachedLines(fileCache, safePath)
		if ctx := lspFormatContext(lines, loc.Range.Start.Line, contextRadius); ctx != "" {
			sb.WriteString(ctx)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}
