package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// buildCompactMessages returns a valid slice for compactHistory:
//
//	[system, ...n assistant/user pairs..., user]
//
// n must be >= 1 so there is at least one "middle" message.
func buildCompactMessages(n int) []message {
	msgs := make([]message, 0, n+2)
	msgs = append(msgs, message{Role: "system", Content: "sys"})
	for i := range n {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		msgs = append(msgs, message{Role: role, Content: fmt.Sprintf("turn %d", i)})
	}
	// Ensure last message is user.
	msgs = append(msgs, message{Role: "user", Content: "latest"})
	return msgs
}

// sseServer returns an httptest.Server that emits a single-chunk SSE stream
// with the given content, then [DONE].
func sseServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	chunk := fmt.Sprintf(`{"choices":[{"index":0,"delta":{"role":"assistant","content":%q},"finish_reason":null}]}`, content)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// minimalCfg returns a *config suitable for compactHistory tests.
// baseURL must be set by the caller.
func minimalCfg(baseURL string) *config {
	return &config{
		model:       "test-model",
		baseURL:     baseURL,
		apiKey:      "sk-test",
		maxTokens:   512,
		temperature: 1.0,
		topP:        1.0,
	}
}

func TestCompactHistory_TooFewMessages_Noop(t *testing.T) {
	t.Parallel()

	cfg := minimalCfg("http://unused")
	msgs := []message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
	}
	got, err := compactHistory(context.Background(), cfg, msgs, "summarize")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(msgs) {
		t.Errorf("expected noop (len=%d), got len=%d", len(msgs), len(got))
	}
}

func TestCompactHistory_NoMiddleMessages_Noop(t *testing.T) {
	t.Parallel()

	cfg := minimalCfg("http://unused")
	// [system, user] — len=2, also covered by <3 check.
	msgs := []message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "latest"},
	}
	got, err := compactHistory(context.Background(), cfg, msgs, "summarize")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(msgs) {
		t.Errorf("noop expected, got len=%d", len(got))
	}
}

func TestCompactHistory_MissingSystemMsg_Error(t *testing.T) {
	t.Parallel()

	cfg := minimalCfg("http://unused")
	msgs := []message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply"},
		{Role: "user", Content: "latest"},
	}
	_, err := compactHistory(context.Background(), cfg, msgs, "summarize")
	if err == nil {
		t.Fatal("expected error for missing system message, got nil")
	}
}

func TestCompactHistory_LastNotUser_Error(t *testing.T) {
	t.Parallel()

	cfg := minimalCfg("http://unused")
	msgs := []message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "turn"},
		{Role: "assistant", Content: "last"},
	}
	_, err := compactHistory(context.Background(), cfg, msgs, "summarize")
	if err == nil {
		t.Fatal("expected error for last message not being user, got nil")
	}
}

func TestCompactHistory_CallsLLM_ReturnsCompacted(t *testing.T) {
	t.Parallel()

	srv := sseServer(t, "summary text")
	cfg := minimalCfg(srv.URL)
	msgs := buildCompactMessages(3)

	got, err := compactHistory(context.Background(), cfg, msgs, "summarize")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect [system, assistant(summary), user].
	if len(got) != 3 {
		t.Fatalf("expected 3-message compacted slice, got %d", len(got))
	}
	if got[0].Role != "system" {
		t.Errorf("got[0].Role = %q, want system", got[0].Role)
	}
	if got[1].Role != "assistant" {
		t.Errorf("got[1].Role = %q, want assistant", got[1].Role)
	}
	if !strings.Contains(got[1].Content, "summary text") {
		t.Errorf("got[1].Content = %q, want to contain 'summary text'", got[1].Content)
	}
	if got[2].Role != "user" {
		t.Errorf("got[2].Role = %q, want user", got[2].Role)
	}
}

func TestCompactHistory_ContextCanceled_Error(t *testing.T) {
	t.Parallel()

	// Server that blocks until context is cancelled.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	cfg := minimalCfg(srv.URL)
	msgs := buildCompactMessages(3)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	_, err := compactHistory(ctx, cfg, msgs, "summarize")
	if err == nil {
		t.Fatal("expected error on canceled context, got nil")
	}
}

func TestCompactHistory_EmptySummary_Error(t *testing.T) {
	t.Parallel()

	// Server returns an empty content field.
	chunk := `{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	t.Cleanup(srv.Close)

	cfg := minimalCfg(srv.URL)
	msgs := buildCompactMessages(3)

	_, err := compactHistory(context.Background(), cfg, msgs, "summarize")
	if err == nil {
		t.Fatal("expected error for empty summary, got nil")
	}
}
