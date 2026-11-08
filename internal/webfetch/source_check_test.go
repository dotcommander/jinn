package webfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckTermsInContentClassifies(t *testing.T) {
	t.Parallel()
	content := "# Title\nStreaming is supported on this page.\nOther line."
	results := checkTermsInContent(content, []string{"Streaming", "streaming", "missing term"})
	byTerm := map[string]SourceTermResult{}
	for _, result := range results {
		byTerm[result.Term] = result
	}
	if byTerm["Streaming"].Status != "exact_match" {
		t.Fatalf("exact = %+v", byTerm["Streaming"])
	}
	if byTerm["streaming"].Status != "normalized_match" {
		t.Fatalf("normalized = %+v", byTerm["streaming"])
	}
	if byTerm["missing term"].Status != "not_found" {
		t.Fatalf("not_found = %+v", byTerm["missing term"])
	}
	if len(byTerm["Streaming"].Excerpts) != 1 || byTerm["Streaming"].Lines[0] != 2 {
		t.Fatalf("evidence = %+v", byTerm["Streaming"])
	}
}

func TestSourceCheckOverSnapshotAndURL(t *testing.T) {
	t.Parallel()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Doc\nRate limits apply here.\n"))
	}))
	t.Cleanup(target.Close)
	service := NewService(Config{
		AllowPrivateNetworks: true,
		JinaAPIKey:           "test",
		ReaderEndpoint:       target.URL,
		URLCacheDir:          t.TempDir(),
	})
	service.snapshots = newSnapshotStore(t.TempDir(), time.Hour)
	info := service.SaveDocumentSnapshot(Document{Content: "# Snap\nAlpha beta gamma.\n"})
	if info.Status != "stored" {
		t.Fatalf("info = %+v", info)
	}
	resp, err := service.SourceCheck(context.Background(), SourceCheckRequest{
		Claim:       "rate limits and alpha",
		URLs:        []string{target.URL + "/page"},
		SnapshotIDs: []string{info.ID},
		Terms:       []string{"Rate limits", "Alpha beta", "absent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Sources) != 2 {
		t.Fatalf("sources = %+v", resp.Sources)
	}
	urlSource := resp.Sources[0]
	if urlSource.Error != "" || len(urlSource.Terms) != 3 {
		t.Fatalf("url source = %+v", urlSource)
	}
	if urlSource.Terms[0].Status != "exact_match" {
		t.Fatalf("url term = %+v", urlSource.Terms[0])
	}
	snapSource := resp.Sources[1]
	if snapSource.Digest == "" || snapSource.Terms[1].Status != "exact_match" {
		t.Fatalf("snapshot source = %+v", snapSource)
	}
	if resp.Claim != "rate limits and alpha" {
		t.Fatalf("claim = %q", resp.Claim)
	}
}

func TestSourceCheckPartialFailurePreservesEvidence(t *testing.T) {
	t.Parallel()
	service := NewService(Config{URLCacheDir: t.TempDir()})
	service.snapshots = newSnapshotStore(t.TempDir(), time.Hour)
	info := service.SaveDocumentSnapshot(Document{Content: "present term here\n"})
	resp, err := service.SourceCheck(context.Background(), SourceCheckRequest{
		URLs:        []string{"https://invalid.invalid/x"},
		SnapshotIDs: []string{info.ID},
		Terms:       []string{"present term"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Sources) != 2 {
		t.Fatalf("sources = %+v", resp.Sources)
	}
	if resp.Sources[0].Error == "" {
		t.Fatalf("url source = %+v, want recorded error", resp.Sources[0])
	}
	if resp.Sources[1].Error != "" || resp.Sources[1].Terms[0].Status != "exact_match" {
		t.Fatalf("snapshot source = %+v", resp.Sources[1])
	}
}

func TestSourceCheckValidation(t *testing.T) {
	t.Parallel()
	service := NewService(Config{URLCacheDir: t.TempDir()})
	if _, err := service.SourceCheck(context.Background(), SourceCheckRequest{Terms: []string{"x"}}); err == nil {
		t.Fatal("no sources accepted")
	}
	if _, err := service.SourceCheck(context.Background(), SourceCheckRequest{URLs: []string{"https://example.com"}, Terms: nil}); err == nil {
		t.Fatal("no terms accepted")
	}
	many := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		many = append(many, "https://example.com")
	}
	if _, err := service.SourceCheck(context.Background(), SourceCheckRequest{URLs: many, Terms: []string{"x"}}); err == nil {
		t.Fatal("6 sources accepted")
	}
}
