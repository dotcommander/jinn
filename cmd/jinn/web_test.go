package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/dotcommander/jinn/internal/webfetch"
)

func TestWebConfigUsesJinnNamesAndVersionedUserAgent(t *testing.T) {
	t.Setenv("JINN_WEB_SEARCH_PROVIDER", "exa")
	t.Setenv("JINN_WEB_CACHE_DIR", t.TempDir())
	version = "test-version"
	t.Cleanup(func() { version = "dev" })
	cfg := webConfig()
	if cfg.SearchProvider != "exa" {
		t.Fatalf("provider = %q", cfg.SearchProvider)
	}
	if cfg.UserAgent != "jinn/"+jinn.ResolveVersion("test-version") {
		t.Fatalf("user agent = %q", cfg.UserAgent)
	}
}

func TestWebOutputProjectionKeepsUTF8(t *testing.T) {
	got := projectWebContent("hello 😀 world", webOutputLimits{MaxBytes: 8})
	if got != "hello " {
		t.Fatalf("projection = %q", got)
	}
}

func TestRenderWebFetchHumanReportsTruncation(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := renderWebFetch(&output, webfetch.Document{Content: "one\ntwo\nthree\n"}, false, webOutputLimits{MaxLines: 2}); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "one\ntwo\n") || !strings.Contains(got, "[truncated: showing 2/3 lines") {
		t.Fatalf("output = %q", got)
	}
}

func TestRenderWebFetchJSONPreservesCompatibilityFields(t *testing.T) {
	t.Parallel()
	doc := webfetch.Document{
		URL: "https://example.com", FinalURL: "https://example.com/final", StatusCode: 200,
		ContentType: "text/markdown", Title: "Title", Description: "Description",
		Domain: "example.com", Favicon: "/favicon.ico", Image: "/image.png",
		Language: "en", Published: "2026-08-10", Author: "Author", Site: "Site",
		WordCount: 3, Extractor: "article", Source: "defuddle", Rendered: true,
		Warnings: []string{"fallback"}, Content: "one\ntwo\nthree\n",
	}
	var output bytes.Buffer
	if err := renderWebFetch(&output, doc, true, webOutputLimits{MaxLines: 2}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"description", "domain", "word_count", "extractor", "rendered", "warnings", "truncated", "truncated_by", "total_lines", "output_lines", "max_lines"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("JSON missing %q: %#v", key, got)
		}
	}
	if got["content"] != "one\ntwo\n" || got["truncated_by"] != "lines" {
		t.Fatalf("JSON = %#v", got)
	}
}

func TestDefaultJinnWebCacheDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if got, want := defaultJinnWebCacheDir(), filepath.Join(os.Getenv("XDG_CACHE_HOME"), "jinn", "web", "urls"); got != want {
		t.Fatalf("XDG cache directory = %q, want %q", got, want)
	}
	t.Setenv("XDG_CACHE_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := defaultJinnWebCacheDir(), filepath.Join(home, ".cache", "jinn", "web", "urls"); got != want {
		t.Fatalf("fallback cache directory = %q, want %q", got, want)
	}
}

func TestWebCLIJSONFailureIsFlatAndNonzero(t *testing.T) {
	bin := buildWebCLITestBinary(t)
	stdout, stderr, err := runWebCLITestBinary(t, bin, "web", "fetch", "--json", "not-a-url")
	if err == nil {
		t.Fatal("web fetch --json invalid URL succeeded")
	}
	if stderr != "" {
		t.Fatalf("web fetch --json stderr = %q, want empty", stderr)
	}
	decoder := json.NewDecoder(strings.NewReader(stdout))
	var output map[string]any
	if err := decoder.Decode(&output); err != nil {
		t.Fatalf("web fetch --json output is not JSON: %v\n%s", err, stdout)
	}
	if output["ok"] != false || output["error"] == "" || output["error_code"] == "" {
		t.Fatalf("web fetch --json output = %#v, want flat coded error", output)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		t.Fatalf("web fetch --json emitted duplicate JSON: %s", stdout)
	}
}

func TestWebCLIHelpExitsCleanly(t *testing.T) {
	bin := buildWebCLITestBinary(t)
	for _, args := range [][]string{{"--help"}, {"web", "fetch", "--help"}, {"web", "search", "--help"}} {
		stdout, stderr, err := runWebCLITestBinary(t, bin, args...)
		if err != nil {
			t.Fatalf("jinn %s: %v", strings.Join(args, " "), err)
		}
		if stderr != "" {
			t.Fatalf("jinn %s stderr = %q, want empty", strings.Join(args, " "), stderr)
		}
		if stdout == "" {
			t.Fatalf("jinn %s did not print help", strings.Join(args, " "))
		}
	}
	stdout, _, _ := runWebCLITestBinary(t, bin, "--help")
	if !strings.Contains(stdout, "web") || !strings.Contains(stdout, "discover|read-only|network") {
		t.Fatalf("top-level help missing web or MCP profiles: %q", stdout)
	}
	if strings.Contains(stdout, "completion") {
		t.Fatalf("top-level help advertises unsupported completion command: %q", stdout)
	}
	stdout, stderr, err := runWebCLITestBinary(t, bin, "completion")
	if err == nil || !strings.Contains(stdout, `unknown command or flag \"completion\"`) || stderr != "" {
		t.Fatalf("jinn completion = stdout:%q stderr:%q error:%v", stdout, stderr, err)
	}
}

func buildWebCLITestBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "jinn-web-test-bin")
	build := exec.Command("go", "build", "-o", bin, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build jinn: %v\n%s", err, output)
	}
	return bin
}

//nolint:revive // stdout and stderr are intentionally parallel string results for CLI assertions.
func runWebCLITestBinary(t *testing.T, bin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}
