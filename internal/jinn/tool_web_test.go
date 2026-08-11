package jinn

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dotcommander/jinn/internal/webfetch"
)

func TestWebToolsAreNetworkOnlyAndMapCodedErrors(t *testing.T) {
	t.Parallel()
	engine, err := NewWithConfig(t.TempDir(), EngineConfig{Web: webfetch.Config{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	if _, _, err := engine.Dispatch(context.Background(), webFetchTool, args("url", "http://127.0.0.1/")); err == nil {
		t.Fatal("web_fetch accepted a private target")
	} else {
		var coded *ErrWithSuggestion
		if !errors.As(err, &coded) || coded.Code != webfetch.ErrorCodePrivateNetwork {
			t.Fatalf("web_fetch error = %#v, want private-network coded error", err)
		}
	}
	if _, _, err := engine.Dispatch(context.Background(), webSearchTool, args("query", "jinn")); err == nil {
		t.Fatal("web_search accepted missing provider credentials")
	} else {
		var coded *ErrWithSuggestion
		if !errors.As(err, &coded) || coded.Code != webfetch.ErrorCodeMissingAPIKey {
			t.Fatalf("web_search error = %#v, want missing-key coded error", err)
		}
	}
}

func TestWebFetchProjectsAndPaginatesLocalResult(t *testing.T) {
	t.Parallel()
	reader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# First\nbody\n## Second\n"))
	}))
	t.Cleanup(reader.Close)
	engine, err := NewWithConfig(t.TempDir(), EngineConfig{Web: webfetch.Config{
		AllowPrivateNetworks: true,
		ReaderEndpoint:       reader.URL,
		JinaAPIKey:           "local-test",
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	result, _, err := engine.Dispatch(context.Background(), webFetchTool, args(
		"url", reader.URL+"/target",
		"format", "headings",
		"max_lines", float64(1),
	))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Text != "# First\n" {
		t.Fatalf("Text = %q", result.Text)
	}
	if result.Meta["format"] != "headings" || result.Meta["truncated"] != true || result.Meta["truncated_by"] != "lines" {
		t.Fatalf("projection meta = %#v", result.Meta)
	}
	if result.Meta["next_start_line"] != 1 || result.Meta["total_lines"] != 2 || result.Meta["output_lines"] != 1 {
		t.Fatalf("pagination meta = %#v", result.Meta)
	}
}

func TestWebFetchRejectsRawNonMarkdownProjection(t *testing.T) {
	t.Parallel()
	engine, err := NewWithConfig(t.TempDir(), EngineConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	_, _, err = engine.Dispatch(context.Background(), webFetchTool, args(
		"url", "https://example.com",
		"raw", true,
		"format", "headings",
	))
	var coded *ErrWithSuggestion
	if !errors.As(err, &coded) || coded.Code != webfetch.ErrorCodeInvalidArgument {
		t.Fatalf("error = %#v", err)
	}
}

func TestWebSearchSchemaMatchesHighlightSentenceLimit(t *testing.T) {
	t.Parallel()
	if err := validateToolArgs(webSearchTool, args("query", "jinn", "highlight_sentences", float64(10))); err != nil {
		t.Fatalf("10 rejected: %v", err)
	}
	if err := validateToolArgs(webSearchTool, args("query", "jinn", "highlight_sentences", float64(11))); err == nil {
		t.Fatal("11 accepted")
	}
}

func TestNetworkToolNamesAndReadOnlySurface(t *testing.T) {
	t.Parallel()
	got := NetworkToolNames()
	if len(got) != 2 || got[0] != webFetchTool || got[1] != webSearchTool {
		t.Fatalf("NetworkToolNames() = %v", got)
	}
	for _, name := range ReadOnlyToolNames() {
		if name == webFetchTool || name == webSearchTool {
			t.Fatalf("read-only surface includes %q", name)
		}
	}
}

func TestRunPlanRejectsWebToolsInEveryPhase(t *testing.T) {
	t.Parallel()
	for _, mutates := range []bool{false, true} {
		err := validatePlan(&PlanTree{Root: "root", Nodes: []PlanNode{{ID: "root", Mutates: mutates, Commands: []PlanOp{{Tool: webFetchTool}}}}})
		if err == nil {
			t.Fatalf("mutates=%v accepted web tool", mutates)
		}
		var coded *ErrWithSuggestion
		if !errors.As(err, &coded) || coded.Code != ErrCodePlanInvalid {
			t.Fatalf("mutates=%v error = %#v", mutates, err)
		}
	}
}
