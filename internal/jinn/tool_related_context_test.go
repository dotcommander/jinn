package jinn

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRelatedContext_DispatchRanksAndFilters(t *testing.T) {
	e, _ := testEngine(t)
	home := t.TempDir()
	cfg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("JINN_CONFIG_DIR", cfg)

	writeTestFile(t, home, ".claude/kb/go/race.md", `---
name: go-race
description: "Go race debugging"
tags: [goroutine, mutex]
aliases:
  - data race
source: sess-1
---
# Go Race Conditions

Use go test -race and protect shared state with mutexes.
`)
	writeTestFile(t, home, ".claude/skills/refactor.md", `---
name: refactor-skill
description: Refactor code safely
---
# Refactoring

Extract small functions.
`)

	raw, _, err := e.Dispatch(context.Background(), "related_context", args(
		"query", "fix goroutine data race with mutex",
		"types", []interface{}{"kb"},
		"limit", float64(10),
	))
	if err != nil {
		t.Fatalf("related_context: %v", err)
	}
	var got relatedContextResponse
	if err := json.Unmarshal([]byte(raw.Text), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, raw.Text)
	}
	if !got.Index.Rebuilt {
		t.Fatal("first call should rebuild index")
	}
	if len(got.Results) != 1 {
		t.Fatalf("results len = %d, want 1: %#v", len(got.Results), got.Results)
	}
	res := got.Results[0]
	if res.Type != "kb" || res.Name != "go-race" {
		t.Fatalf("unexpected top result: %#v", res)
	}
	if res.Score == 0 {
		t.Fatal("expected positive score")
	}
	if !containsString(res.Matched, "goroutine") || !containsString(res.Matched, "race") {
		t.Fatalf("matched terms = %#v", res.Matched)
	}
	if res.Source != "sess-1" {
		t.Fatalf("source = %q, want sess-1", res.Source)
	}
}

func TestRelatedContext_CacheHitAndForcedRebuild(t *testing.T) {
	e, _ := testEngine(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())

	writeTestFile(t, home, ".claude/skills/go.md", "# Go Skill\n\nGoroutine and channel guidance.\n")

	first := callRelatedContext(t, e, args("query", "goroutine channels", "client", "claude"))
	if !first.Index.Rebuilt {
		t.Fatal("first call should rebuild")
	}
	second := callRelatedContext(t, e, args("query", "goroutine channels", "client", "claude"))
	if second.Index.Rebuilt {
		t.Fatal("second call should use cache")
	}
	third := callRelatedContext(t, e, args("query", "goroutine channels", "client", "claude", "rebuild", true))
	if !third.Index.Rebuilt {
		t.Fatal("forced rebuild should report rebuilt")
	}
}

func TestRelatedContext_PluginCommandSource(t *testing.T) {
	e, _ := testEngine(t)
	home := t.TempDir()
	plugin := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())

	writeTestFile(t, plugin, "dev-commands/fix.md", "# Fix Command\n\nRepair build failures.\n")
	writeTestFile(t, plugin, ".claude-plugin/plugin.json", `{"commands":"dev-commands"}`)
	pluginsJSON := `{"plugins":{"demo":[{"installPath":` + quoteJSON(plugin) + `}]}}`
	writeTestFile(t, home, ".claude/plugins/installed_plugins.json", pluginsJSON)

	got := callRelatedContext(t, e, args("query", "repair build failure", "types", []interface{}{"command"}, "client", "claude"))
	if len(got.Results) != 1 {
		t.Fatalf("results len = %d, want 1: %#v", len(got.Results), got.Results)
	}
	if got.Results[0].Type != "command" {
		t.Fatalf("type = %q, want command", got.Results[0].Type)
	}
}

func TestRelatedContext_ConfiguredPaths(t *testing.T) {
	e, _ := testEngine(t)
	home := t.TempDir()
	cfg := t.TempDir()
	extra := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("JINN_CONFIG_DIR", cfg)

	writeTestFile(t, extra, "project/notes.md", "# Project Runbook\n\nFlume queue retry policy.\n")
	writeTestFile(t, cfg, "jinn/config.json", `{"related_context":{"paths":[`+quoteJSON(extra)+`]}}`)

	got := callRelatedContext(t, e, args("query", "flume retry", "rebuild", true))
	if len(got.Results) != 1 {
		t.Fatalf("results len = %d, want 1: %#v", len(got.Results), got.Results)
	}
	if got.Results[0].Name != "notes" || got.Results[0].Type != "kb" {
		t.Fatalf("unexpected result: %#v", got.Results[0])
	}
	if got.Index.SourceCount != 1 {
		t.Fatalf("source count = %d, want 1", got.Index.SourceCount)
	}
}

func TestRelatedContext_PiSkillSource(t *testing.T) {
	e, _ := testEngine(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())

	writeTestFile(t, home, ".pi/agent/skills/release.md", `---
name: pi-release
description: Pi release workflow
---
# Pi Release

Package and publish Pi extensions.
`)

	got := callRelatedContext(t, e, args(
		"query", "publish pi extension package",
		"types", []interface{}{"skill"},
		"client", "pi",
		"rebuild", true,
	))
	if len(got.Results) != 1 {
		t.Fatalf("results len = %d, want 1: %#v", len(got.Results), got.Results)
	}
	if got.Results[0].Name != "pi-release" || got.Results[0].Type != "skill" {
		t.Fatalf("unexpected result: %#v", got.Results[0])
	}
}

func TestRelatedContext_ClientScopesSkills(t *testing.T) {
	e, _ := testEngine(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())

	writeTestFile(t, home, ".claude/skills/claude-only.md", "# Claude Only\n\nuniqueclaudeskill token.\n")
	writeTestFile(t, home, ".codex/skills/codex-only.md", "# Codex Only\n\nuniquecodexskill token.\n")
	writeTestFile(t, home, ".pi/agent/skills/pi-only.md", "# Pi Only\n\nuniquepiskill token.\n")

	for _, tc := range []struct {
		client string
		want   string
	}{
		{client: "claude", want: "claude-only"},
		{client: "codex", want: "codex-only"},
		{client: "pi", want: "pi-only"},
	} {
		t.Run(tc.client, func(t *testing.T) {
			got := callRelatedContext(t, e, args(
				"query", "uniqueclaudeskill uniquecodexskill uniquepiskill",
				"types", []interface{}{"skill"},
				"client", tc.client,
				"rebuild", true,
			))
			if got.Index.Client != tc.client {
				t.Fatalf("index client = %q, want %q", got.Index.Client, tc.client)
			}
			if len(got.Results) != 1 || got.Results[0].Name != tc.want {
				t.Fatalf("%s results should only include %s: %#v", tc.client, tc.want, got.Results)
			}
		})
	}
}

func TestRelatedContext_ConfigDoesNotSelectClient(t *testing.T) {
	e, _ := testEngine(t)
	home := t.TempDir()
	cfg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("JINN_CONFIG_DIR", cfg)

	writeTestFile(t, home, ".codex/skills/codex-only.md", "# Codex Only\n\nuniquecodexskill token.\n")
	writeTestFile(t, cfg, "jinn/config.json", `{"related_context":{"client":"codex"}}`)

	got := callRelatedContext(t, e, args(
		"query", "uniquecodexskill",
		"types", []interface{}{"skill"},
		"rebuild", true,
	))
	if len(got.Results) != 0 {
		t.Fatalf("config client must not select skill dirs: %#v", got.Results)
	}
	if !strings.Contains(strings.Join(got.Warnings, "\n"), "client not provided") {
		t.Fatalf("warnings = %#v, want missing client warning", got.Warnings)
	}
}

func TestRelatedContext_ConfiguredPathWarnings(t *testing.T) {
	e, _ := testEngine(t)
	home := t.TempDir()
	cfg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("JINN_CONFIG_DIR", cfg)

	if err := os.MkdirAll(filepath.Join(cfg, "jinn/.ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, cfg, "jinn/config.json", `{"related_context":{"paths":[".ssh"]}}`)

	got := callRelatedContext(t, e, args("query", "anything", "rebuild", true))
	if len(got.Warnings) == 0 {
		t.Fatal("expected warning for sensitive configured path")
	}
	if !strings.Contains(strings.Join(got.Warnings, "\n"), "sensitive path") {
		t.Fatalf("warnings = %#v, want sensitive path warning", got.Warnings)
	}
}

func TestRelatedContext_InvalidArgs(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, _, err := e.Dispatch(context.Background(), "related_context", args("query", "x", "types", "kb"))
	if err == nil {
		t.Fatal("expected invalid types error")
	}
	if !strings.Contains(err.Error(), "types must be an array") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRelatedFrontmatter(t *testing.T) {
	t.Parallel()
	fm := parseRelatedFrontmatter([]byte(`---
name: "go-race"
description: 'Race debugging'
tags:
  - go
  - race
aliases: [goroutine leak, mutex]
source_file: /tmp/source.md
---
# Body
`))
	if fm.Name != "go-race" || fm.Description != "Race debugging" {
		t.Fatalf("bad scalar parse: %#v", fm)
	}
	if !containsString(fm.Tags, "go") || !containsString(fm.Tags, "race") {
		t.Fatalf("bad tags: %#v", fm.Tags)
	}
	if !containsString(fm.Aliases, "goroutine leak") || !containsString(fm.Aliases, "mutex") {
		t.Fatalf("bad aliases: %#v", fm.Aliases)
	}
	if fm.SourceFile != "/tmp/source.md" {
		t.Fatalf("source_file = %q", fm.SourceFile)
	}
}

func callRelatedContext(t *testing.T, e *Engine, a map[string]interface{}) relatedContextResponse {
	t.Helper()
	raw, _, err := e.Dispatch(context.Background(), "related_context", a)
	if err != nil {
		t.Fatalf("related_context: %v", err)
	}
	var got relatedContextResponse
	if err := json.Unmarshal([]byte(raw.Text), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, raw.Text)
	}
	return got
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestRelatedContextIndexPathUsesConfigDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", cfg)
	path, err := relatedContextIndexPath("codex")
	if err != nil {
		t.Fatalf("relatedContextIndexPath: %v", err)
	}
	want := filepath.Join(cfg, "jinn", "context-index-codex.json")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func TestRelatedContext_SeparateClientCaches(t *testing.T) {
	e, _ := testEngine(t)
	home := t.TempDir()
	cfg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("JINN_CONFIG_DIR", cfg)

	writeTestFile(t, home, ".claude/skills/claude.md", "# Claude\n\nclaudecache token.\n")
	writeTestFile(t, home, ".codex/skills/codex.md", "# Codex\n\ncodexcache token.\n")

	claude := callRelatedContext(t, e, args("query", "claudecache", "types", []interface{}{"skill"}, "client", "claude"))
	if !claude.Index.Rebuilt {
		t.Fatal("first claude call should rebuild")
	}
	codex := callRelatedContext(t, e, args("query", "codexcache", "types", []interface{}{"skill"}, "client", "codex"))
	if !codex.Index.Rebuilt {
		t.Fatal("first codex call should rebuild")
	}
	claudeAgain := callRelatedContext(t, e, args("query", "claudecache", "types", []interface{}{"skill"}, "client", "claude"))
	if claudeAgain.Index.Rebuilt {
		t.Fatal("claude cache should survive codex cache rebuild")
	}

	for _, client := range []string{"claude", "codex"} {
		path, err := relatedContextIndexPath(client)
		if err != nil {
			t.Fatalf("index path for %s: %v", client, err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s cache at %s: %v", client, path, err)
		}
	}
}

func TestRelatedContext_NoSources(t *testing.T) {
	e, _ := testEngine(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())

	got := callRelatedContext(t, e, args("query", "anything"))
	if len(got.Results) != 0 {
		t.Fatalf("results len = %d, want 0", len(got.Results))
	}
	if len(got.Warnings) == 0 {
		t.Fatal("expected warning for no source dirs")
	}
}

func TestRelatedContextNeedsRebuildDetectsEqualCountOlderMtime(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestFile(t, dir, "x.md", "# New\n")
	count, mtime, err := relatedContextSourceStats([]string{dir})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	idx := relatedContextIndex{
		Version:      relatedContextIndexVersion,
		FileCount:    count,
		SourceMTime:  mtime.Add(time.Second),
		SourceCount:  1,
		SourceClient: "none",
	}
	if !relatedContextNeedsRebuild(idx, "none", []string{dir}) {
		t.Fatal("expected rebuild when cached mtime differs even if current is older")
	}
}
