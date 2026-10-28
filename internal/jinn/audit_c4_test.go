package jinn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditC402ResourceOperations(t *testing.T) {
	t.Parallel()
	var edit lspWorkspaceEdit
	fixture := map[string]any{"documentChanges": []any{
		map[string]any{"kind": "create", "uri": "file:///work/new.go"},
		map[string]any{"textDocument": map[string]any{"uri": "file:///work/new.go"}, "edits": []any{map[string]any{"newText": "package main"}}},
		map[string]any{"kind": "rename", "oldUri": "file:///work/new.go", "newUri": "file:///work/moved.go"},
		map[string]any{"kind": "delete", "uri": "file:///work/old.go"},
	}}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &edit); err != nil {
		t.Fatal(err)
	}
	output := formatWorkspaceEdit(&edit, "/work")
	last := -1
	for _, want := range []string{"create new.go", "new.go: 1 edit(s)", "rename new.go -> moved.go", "delete old.go"} {
		position := strings.Index(output, want)
		if position <= last {
			t.Fatalf("missing or unordered %q in %q", want, output)
		}
		last = position
	}
	resourceOnly := map[string]any{"documentChanges": []any{map[string]any{"kind": "create", "uri": "file:///work/new.go"}}}
	data, _ = json.Marshal(resourceOnly)
	edit = lspWorkspaceEdit{}
	if err := json.Unmarshal(data, &edit); err != nil {
		t.Fatal(err)
	}
	if output := formatWorkspaceEdit(&edit, "/work"); !strings.Contains(output, "create new.go") {
		t.Fatalf("resource-only preview = %q", output)
	}
}

func TestAuditC403IdentifierCandidates(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, text, symbol string
		column             int
		wantError          bool
	}{
		{"substring", "provided identifier", "id", 0, true},
		{"ambiguous", "id + id", "id", 0, true},
		{"unicode", "😀 provided αβ", "αβ", 12, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			engine := New(t.TempDir(), "test")
			t.Cleanup(func() { _ = engine.Close() })
			path := filepath.Join(engine.workDir, "source.go")
			if err := os.WriteFile(path, []byte(test.text), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, find := range []func(string, int, string) (int, error){findSymbolColumn, engine.findSymbolColumn} {
				column, err := find(path, 0, test.symbol)
				if (err != nil) != test.wantError || (!test.wantError && column != test.column) {
					t.Fatalf("column = %d, error = %v", column, err)
				}
			}
		})
	}
}

func TestAuditC404WindowsDriveEncoding(t *testing.T) {
	t.Parallel()
	if got := pathToURI(`C:\work\a b#%.go`); got != "file:///C:/work/a%20b%23%25.go" {
		t.Fatalf("Windows file URI = %q", got)
	}
}

func TestAuditC404PlatformRoundTrips(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ goos, path, uri string }{
		{"windows", `C:\work\a b#%é.go`, "file:///C:/work/a%20b%23%25%C3%A9.go"},
		{"windows", `C:\`, "file:///C:/"},
		{"windows", `\\server\share\a b雪.go`, "file://server/share/a%20b%E9%9B%AA.go"},
		{"linux", "/work/a b#%雪.go", "file:///work/a%20b%23%25%E9%9B%AA.go"},
	} {
		t.Run(test.goos+test.path, func(t *testing.T) {
			t.Parallel()
			if got := pathToURIForOS(test.path, test.goos); got != test.uri {
				t.Fatalf("URI = %q, want %q", got, test.uri)
			}
			if got, err := fileURIPathForOS(test.uri, test.goos); err != nil || got != test.path {
				t.Fatalf("path = %q, %v", got, err)
			}
		})
	}
	for _, raw := range []string{"https://example.test/a.go", "file:C:/a.go", "file://server:80/share/a.go", "file:///C:/a.go?query"} {
		if _, err := fileURIPathForOS(raw, "windows"); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestAuditC4I01MalformedOptionalData(t *testing.T) {
	t.Parallel()
	for _, value := range []any{nil, "bad", map[string]any{"href": 42}, map[string]any{"href": "%invalid"}, map[string]any{"href": "javascript:alert(1)"}} {
		if got := diagnosticTargetURL(value); got != "" {
			t.Fatalf("optional URL = %q", got)
		}
	}
	for _, code := range []any{nil, map[string]any{"value": "rule"}, []any{"bad"}} {
		if got := diagnosticCodeString(code); got != "" {
			t.Fatalf("malformed code = %q", got)
		}
	}
	if got := diagnosticCodeString("rule"); got != "rule" {
		t.Fatalf("string code = %q", got)
	}
}

func TestAuditC4I01DiagnosticCodeDescription(t *testing.T) {
	t.Parallel()
	var diagnostics []lspDiagnostic
	fixture := []map[string]any{{"code": 42, "message": "problem", "codeDescription": map[string]any{"href": "https://example.test/rule/42"}}}
	data, _ := json.Marshal(fixture)
	if err := json.Unmarshal(data, &diagnostics); err != nil {
		t.Fatal(err)
	}
	output := formatDiagnostics("source.go", diagnostics)
	if !strings.Contains(output, "lsp 42") || strings.Count(output, "https://example.test/rule/42") != 1 || strings.Contains(output, "map[") {
		t.Fatalf("diagnostic output = %q", output)
	}
}
