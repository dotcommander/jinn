package mcpsnapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dotcommander/jinn/internal/mcpexplore"
	"github.com/voocel/mcp-sdk-go/protocol"
)

const version = 1

// Snapshot is the versioned approved description of one named server.
type Snapshot struct {
	Version             int               `json:"version"`
	Alias               string            `json:"alias"`
	CapturedAt          time.Time         `json:"captured_at"`
	TargetFingerprint   string            `json:"target_fingerprint"`
	ManifestFingerprint string            `json:"manifest_fingerprint"`
	Server              json.RawMessage   `json:"server"`
	Tools               []json.RawMessage `json:"tools"`
}

// Build captures a stable tool manifest without retaining any credential values.
func Build(alias string, target mcpexplore.Server, server *protocol.Implementation, tools []*protocol.Tool, capturedAt time.Time) (Snapshot, []Warning, error) {
	if err := mcpexplore.ValidateAlias(alias); err != nil {
		return Snapshot{}, nil, err
	}
	if server == nil {
		return Snapshot{}, nil, errors.New("MCP server identity is required")
	}
	serverJSON, err := CanonicalJSON(server)
	if err != nil {
		return Snapshot{}, nil, fmt.Errorf("canonicalize server: %w", err)
	}
	toolJSON, warnings, err := canonicalTools(tools)
	if err != nil {
		return Snapshot{}, nil, err
	}
	warnings = append(warnings, LintImplementation(server, "/server")...)
	targetFingerprint, err := Fingerprint(normalizedTarget(target))
	if err != nil {
		return Snapshot{}, nil, err
	}
	manifestFingerprint, err := Fingerprint(struct {
		Server json.RawMessage   `json:"server"`
		Tools  []json.RawMessage `json:"tools"`
	}{Server: serverJSON, Tools: toolJSON})
	if err != nil {
		return Snapshot{}, nil, err
	}
	snapshot := Snapshot{Version: version, Alias: alias, CapturedAt: capturedAt.UTC(), TargetFingerprint: targetFingerprint, ManifestFingerprint: manifestFingerprint, Server: serverJSON, Tools: toolJSON}
	manifest, err := CanonicalJSON(snapshotManifestForLint(snapshot))
	if err != nil {
		return Snapshot{}, nil, err
	}
	if len(manifest) > 4<<20 {
		warnings = append(warnings, Warning{Code: "manifest_too_large", Path: "", Message: "manifest exceeds 4 MiB"})
	}
	sortWarnings(warnings)
	return snapshot, warnings, nil
}

func normalizedTarget(target mcpexplore.Server) mcpexplore.Server {
	normalized := target
	normalized.Args = append([]string(nil), target.Args...)
	normalized.PassEnv = append([]string(nil), target.PassEnv...)
	sort.Strings(normalized.PassEnv)
	return normalized
}

func canonicalTools(tools []*protocol.Tool) ([]json.RawMessage, []Warning, error) {
	for index, tool := range tools {
		if tool == nil || tool.Name == "" {
			return nil, nil, fmt.Errorf("MCP tool at index %d has no name", index)
		}
	}
	ordered := append([]*protocol.Tool(nil), tools...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	output := make([]json.RawMessage, 0, len(ordered))
	warnings := make([]Warning, 0)
	for index, tool := range ordered {
		if index > 0 && ordered[index-1].Name == tool.Name {
			return nil, nil, fmt.Errorf("duplicate MCP tool name %q", tool.Name)
		}
		canonical, err := CanonicalJSON(tool)
		if err != nil {
			return nil, nil, fmt.Errorf("canonicalize MCP tool %q: %w", tool.Name, err)
		}
		output = append(output, canonical)
		warnings = append(warnings, LintTool(tool, "/tools/"+jsonPointerToken(tool.Name))...)
	}
	return output, warnings, nil
}

func snapshotManifestForLint(snapshot Snapshot) any {
	return struct {
		Server json.RawMessage   `json:"server"`
		Tools  []json.RawMessage `json:"tools"`
	}{Server: snapshot.Server, Tools: snapshot.Tools}
}

// Validate rejects malformed, stale, or internally inconsistent snapshot files.
//
//nolint:gocyclo // Snapshot validation deliberately fails closed across all persisted invariants.
func (snapshot Snapshot) Validate() error {
	if snapshot.Version != version {
		return fmt.Errorf("unsupported MCP snapshot version %d", snapshot.Version)
	}
	if err := mcpexplore.ValidateAlias(snapshot.Alias); err != nil {
		return err
	}
	if snapshot.CapturedAt.IsZero() || !validFingerprint(snapshot.TargetFingerprint) || !validFingerprint(snapshot.ManifestFingerprint) || !json.Valid(snapshot.Server) || snapshot.Tools == nil {
		return errors.New("invalid MCP snapshot")
	}
	var serverObject map[string]any
	if err := json.Unmarshal(snapshot.Server, &serverObject); err != nil || serverObject == nil {
		return errors.New("invalid MCP snapshot server identity")
	}
	for _, tool := range snapshot.Tools {
		if !json.Valid(tool) {
			return errors.New("invalid MCP snapshot tool")
		}
	}
	if _, err := toolMap(snapshot.Tools); err != nil {
		return err
	}
	previous := ""
	for _, tool := range snapshot.Tools {
		var value struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(tool, &value); err != nil || value.Name == "" || (previous != "" && previous >= value.Name) {
			return errors.New("MCP snapshot tools must have unique names in lexical order")
		}
		previous = value.Name
	}
	manifestFingerprint, err := Fingerprint(snapshotManifestForLint(snapshot))
	if err != nil {
		return err
	}
	if manifestFingerprint != snapshot.ManifestFingerprint {
		return errors.New("MCP snapshot manifest fingerprint does not match contents")
	}
	return nil
}

func validFingerprint(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func sortWarnings(warnings []Warning) {
	sort.Slice(warnings, func(i, j int) bool {
		if warnings[i].Path != warnings[j].Path {
			return warnings[i].Path < warnings[j].Path
		}
		if warnings[i].Code != warnings[j].Code {
			return warnings[i].Code < warnings[j].Code
		}
		return warnings[i].Message < warnings[j].Message
	})
}

func jsonPointerToken(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
