package mcpsnapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/jinn/internal/mcpexplore"
	"github.com/voocel/mcp-sdk-go/protocol"
)

func TestBuildCanonicalizesSortsAndLints(t *testing.T) {
	readOnly := true
	tools := []*protocol.Tool{
		{Name: "z", Description: "<!--unsafe-->\u202e", InputSchema: protocol.JSONSchema{"type": "object", "note": "\u200b"}},
		{Name: "a", Title: "<tag>", InputSchema: protocol.JSONSchema{"properties": map[string]any{"q": map[string]any{"type": "string"}}}, Annotations: &protocol.ToolAnnotations{ReadOnlyHint: &readOnly}, Icons: []protocol.Icon{{Src: "data:image/png;base64,x"}}, Meta: map[string]json.RawMessage{"comment": json.RawMessage(`"<!--meta-->"`)}},
	}
	snapshot, warnings, err := Build("local", mcpexplore.Server{Transport: "http", URL: "https://example.test/mcp", TokenEnv: "TOKEN_NAME"}, &protocol.Implementation{Name: "<server>\u202e", Version: "1"}, tools, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !strings.Contains(string(snapshot.Server), "<server>") || strings.Contains(string(snapshot.Server), "\\u003c") {
		t.Fatalf("server canonical JSON = %s", snapshot.Server)
	}
	var first struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(snapshot.Tools[0], &first); err != nil || first.Name != "a" {
		t.Fatalf("first sorted tool = %#v, %v", first, err)
	}
	gotCodes := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		gotCodes = append(gotCodes, warning.Code)
	}
	for _, want := range []string{"html_comment", warningBidiControl, "zero_width_or_bom", "data_icon_uri"} {
		if !contains(gotCodes, want) {
			t.Fatalf("warning codes %v missing %s", gotCodes, want)
		}
	}
	foundServerWarning := false
	for _, warning := range warnings {
		if warning.Code == warningBidiControl && warning.Path == "/server/name" {
			foundServerWarning = true
		}
	}
	if !foundServerWarning {
		t.Fatalf("warnings %#v missing server identity lint", warnings)
	}
}

func TestMetadataLintSizeAndControlLimits(t *testing.T) {
	tool := &protocol.Tool{
		Name:        "large",
		Description: "\x01" + strings.Repeat("d", 8<<10),
		InputSchema: protocol.JSONSchema{"description": strings.Repeat("s", 256<<10)},
	}
	warnings := LintTool(tool, "/tools/large")
	codes := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		codes = append(codes, warning.Code)
	}
	for _, want := range []string{"ascii_control", "description_too_large", "schema_too_large"} {
		if !contains(codes, want) {
			t.Fatalf("warning codes %v missing %s", codes, want)
		}
	}

	manifestTool := &protocol.Tool{Name: "manifest", Description: strings.Repeat("m", (4<<20)+1), InputSchema: protocol.JSONSchema{"type": "object"}}
	_, manifestWarnings, err := Build("local", mcpexplore.Server{Transport: "stdio", Command: "/bin/echo"}, &protocol.Implementation{Name: "server", Version: "1"}, []*protocol.Tool{manifestTool}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	manifestCodes := make([]string, 0, len(manifestWarnings))
	for _, warning := range manifestWarnings {
		manifestCodes = append(manifestCodes, warning.Code)
	}
	if !contains(manifestCodes, "manifest_too_large") {
		t.Fatalf("manifest warning codes = %v", manifestCodes)
	}
}

func TestSnapshotStoragePreservesLastKnownGoodAndRejectsCorruption(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	first := testSnapshot(t, "local", "first")
	if err := Save(first); err != nil {
		t.Fatal(err)
	}
	second := testSnapshot(t, "local", "second")
	if err := Save(second); err != nil {
		t.Fatal(err)
	}
	path, err := Path("local")
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil || !strings.Contains(string(backup), "first") {
		t.Fatalf("backup = %q, %v", backup, err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("snapshot directory mode = %v, %v", info.Mode(), err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(second); err == nil || !strings.Contains(err.Error(), "refusing to overwrite corrupt") {
		t.Fatalf("Save corrupt snapshot error = %v", err)
	}
}

func TestDiffReportsServerAndArrayPointers(t *testing.T) {
	approved := testSnapshot(t, "local", "old")
	current, _, err := Build("local", mcpexplore.Server{Transport: "stdio", Command: "/bin/echo"}, &protocol.Implementation{Name: "server", Version: "1"}, []*protocol.Tool{{Name: "tool", Description: "old", InputSchema: protocol.JSONSchema{"required": []any{"new"}, "type": "object"}}}, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	current.Server = json.RawMessage(`{"name":"server","version":"2"}`)
	manifest, err := Fingerprint(snapshotManifestForLint(current))
	if err != nil {
		t.Fatal(err)
	}
	current.ManifestFingerprint = manifest
	changes, err := Diff(approved, current)
	if err != nil {
		t.Fatal(err)
	}
	var foundServer, foundArray bool
	for _, change := range changes {
		if change.Kind == "server_changed" && contains(change.Paths, "/version") {
			foundServer = true
		}
		if change.Kind == changeToolChanged && contains(change.Paths, "/inputSchema/required/0") {
			foundArray = true
		}
	}
	if !foundServer || !foundArray {
		t.Fatalf("changes = %#v", changes)
	}
}

func TestDiffPreservesLargeJSONIntegers(t *testing.T) {
	approved := testSnapshot(t, "local", "same")
	current := approved
	approved.Tools = []json.RawMessage{json.RawMessage(`{"name":"tool","inputSchema":{"const":9007199254740992}}`)}
	current.Tools = []json.RawMessage{json.RawMessage(`{"name":"tool","inputSchema":{"const":9007199254740993}}`)}
	var err error
	approved.ManifestFingerprint, err = Fingerprint(snapshotManifestForLint(approved))
	if err != nil {
		t.Fatal(err)
	}
	current.ManifestFingerprint, err = Fingerprint(snapshotManifestForLint(current))
	if err != nil {
		t.Fatal(err)
	}
	changes, err := Diff(approved, current)
	if err != nil || len(changes) != 1 || changes[0].Kind != changeToolChanged || !contains(changes[0].Paths, "/inputSchema/const") {
		t.Fatalf("large integer changes = %#v, %v", changes, err)
	}
}

func TestBuildRejectsNilToolBeforeSorting(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("Build panicked: %v", recovered)
		}
	}()
	if _, _, err := Build("local", mcpexplore.Server{Transport: "stdio", Command: "/bin/echo"}, &protocol.Implementation{Name: "server", Version: "1"}, []*protocol.Tool{nil, {Name: "tool"}}, time.Now()); err == nil {
		t.Fatal("Build accepted nil tool")
	}
}

func TestPathRejectsTraversalAndValidateRejectsDuplicateTools(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	if _, err := Path("../escape"); err == nil {
		t.Fatal("Path accepted traversal")
	}
	snapshot := testSnapshot(t, "local", "one")
	snapshot.Tools = append(snapshot.Tools, snapshot.Tools[0])
	manifest, err := Fingerprint(snapshotManifestForLint(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	snapshot.ManifestFingerprint = manifest
	if err := snapshot.Validate(); err == nil {
		t.Fatal("Validate accepted duplicate tool name")
	}
}

func TestLoadRejectsSnapshotStoredUnderDifferentAlias(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	snapshot := testSnapshot(t, "first", "same")
	data, err := CanonicalJSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	path, err := Path("second")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := Load("second"); err == nil || !exists {
		t.Fatalf("Load alias mismatch = exists %v, err %v", exists, err)
	}
}

func TestTargetFingerprintNormalizesPassEnvironmentOrder(t *testing.T) {
	tools := []*protocol.Tool{{Name: "tool", InputSchema: protocol.JSONSchema{"type": "object"}}}
	first, _, err := Build("local", mcpexplore.Server{Transport: "stdio", Command: "/bin/echo", Args: []string{"serve", "one"}, PassEnv: []string{"B", "A"}}, &protocol.Implementation{Name: "server", Version: "1"}, tools, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := Build("local", mcpexplore.Server{Transport: "stdio", Command: "/bin/echo", Args: []string{"serve", "one"}, PassEnv: []string{"A", "B"}}, &protocol.Implementation{Name: "server", Version: "1"}, tools, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if first.TargetFingerprint != second.TargetFingerprint {
		t.Fatalf("target fingerprints differ: %s != %s", first.TargetFingerprint, second.TargetFingerprint)
	}
}

func testSnapshot(t *testing.T, alias, description string) Snapshot {
	t.Helper()
	snapshot, _, err := Build(alias, mcpexplore.Server{Transport: "stdio", Command: "/bin/echo"}, &protocol.Implementation{Name: "server", Version: "1"}, []*protocol.Tool{{Name: "tool", Description: description, InputSchema: protocol.JSONSchema{"required": []any{"old"}, "type": "object"}}}, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
