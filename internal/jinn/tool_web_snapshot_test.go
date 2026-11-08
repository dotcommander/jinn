package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dotcommander/jinn/internal/webfetch"
)

func snapshotEngine(t *testing.T, cfg webfetch.Config) *Engine {
	t.Helper()
	cfg.SnapshotDir = t.TempDir()
	engine, err := NewWithConfig(t.TempDir(), EngineConfig{Web: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func TestWebFetchPersistSnapshotThenWebContent(t *testing.T) {
	t.Parallel()
	reader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Title\nSnapshot body line.\n"))
	}))
	t.Cleanup(reader.Close)
	engine := snapshotEngine(t, webfetch.Config{
		AllowPrivateNetworks: true,
		ReaderEndpoint:       reader.URL,
		JinaAPIKey:           "local-test",
	})

	result, _, err := engine.Dispatch(context.Background(), webFetchTool, args(
		"url", reader.URL+"/target",
		"persist_snapshot", true,
	))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Meta["outcome"] != webfetch.OutcomeContent {
		t.Fatalf("outcome meta = %#v", result.Meta)
	}
	snapshot, ok := result.Meta["snapshot"].(webfetch.SnapshotInfo)
	if !ok || snapshot.Status != "stored" || snapshot.ID == "" {
		t.Fatalf("snapshot meta = %#v", result.Meta["snapshot"])
	}

	content, _, err := engine.Dispatch(context.Background(), webContentTool, args(
		"snapshot_id", snapshot.ID,
	))
	if err != nil {
		t.Fatalf("web_content: %v", err)
	}
	if !strings.Contains(content.Text, "Snapshot body line.") {
		t.Fatalf("web_content text = %q", content.Text)
	}
	if content.Meta["kind"] != "document" {
		t.Fatalf("web_content meta = %#v", content.Meta)
	}

	// Unknown ids produce the distinct not-found error.
	_, _, err = engine.Dispatch(context.Background(), webContentTool, args(
		"snapshot_id", "0123456789abcdef0123456789abcdef",
	))
	var coded *ErrWithSuggestion
	if !errors.As(err, &coded) || coded.Code != webfetch.ErrorCodeSnapshotNotFound {
		t.Fatalf("unknown snapshot error = %#v, want snapshot_not_found", err)
	}
}

func TestWebSearchPersistSnapshotAndSummary(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Go","url":"https://go.dev","highlights":["The Go language."],"publishedDate":"2026-01-01"}]}`))
	}))
	t.Cleanup(server.Close)
	engine := snapshotEngine(t, webfetch.Config{
		AllowPrivateNetworks: true,
		ExaAPIKey:            "test",
		ExaSearchEndpoint:    server.URL,
		SearchProvider:       "exa",
	})

	result, _, err := engine.Dispatch(context.Background(), webSearchTool, args(
		"query", "go",
		"workflow", "extractive_summary",
		"persist_snapshot", true,
	))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	var decoded struct {
		Results []struct {
			Title string `json:"title"`
		} `json:"results"`
		Summary *struct {
			Status    string `json:"status"`
			Citations []int  `json:"citations"`
		} `json:"summary"`
	}
	if unmarshalErr := json.Unmarshal([]byte(result.Text), &decoded); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if len(decoded.Results) != 1 || decoded.Summary == nil || decoded.Summary.Status != "ok" {
		t.Fatalf("result = %s", result.Text)
	}
	snapshot, ok := result.Meta["snapshot"].(webfetch.SnapshotInfo)
	if !ok || snapshot.Status != "stored" {
		t.Fatalf("snapshot meta = %#v", result.Meta["snapshot"])
	}

	content, _, err := engine.Dispatch(context.Background(), webContentTool, args(
		"snapshot_id", snapshot.ID,
		"item", float64(0),
	))
	if err != nil {
		t.Fatalf("web_content: %v", err)
	}
	if !strings.Contains(content.Text, "https://go.dev") {
		t.Fatalf("web_content text = %q", content.Text)
	}
}

func TestSourceCheckDispatch(t *testing.T) {
	t.Parallel()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Doc\nRate limits apply here.\n"))
	}))
	t.Cleanup(target.Close)
	engine := snapshotEngine(t, webfetch.Config{
		AllowPrivateNetworks: true,
		JinaAPIKey:           "test",
		ReaderEndpoint:       target.URL,
	})

	result, _, err := engine.Dispatch(context.Background(), sourceCheckTool, args(
		"urls", []any{target.URL + "/page"},
		"terms", []any{"Rate limits", "absent term"},
	))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	var decoded struct {
		Sources []struct {
			Terms []struct {
				Term   string `json:"term"`
				Status string `json:"status"`
			} `json:"terms"`
		} `json:"sources"`
	}
	if unmarshalErr := json.Unmarshal([]byte(result.Text), &decoded); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if len(decoded.Sources) != 1 || len(decoded.Sources[0].Terms) != 2 {
		t.Fatalf("result = %s", result.Text)
	}
	if decoded.Sources[0].Terms[0].Status != "exact_match" || decoded.Sources[0].Terms[1].Status != "not_found" {
		t.Fatalf("terms = %+v", decoded.Sources[0].Terms)
	}
}
