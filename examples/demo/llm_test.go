package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sseBody assembles an SSE response body from a slice of data lines.
// Each entry is emitted as "data: <line>\n\n".
func sseBody(lines ...string) string {
	var b strings.Builder
	for _, l := range lines {
		fmt.Fprintf(&b, "data: %s\n\n", l)
	}
	return b.String()
}

// contentChunk returns a single-choice SSE chunk with text content.
func contentChunk(content string) string {
	return fmt.Sprintf(`{"choices":[{"index":0,"delta":{"role":"assistant","content":%q},"finish_reason":null}]}`, content)
}

// toolChunk returns an SSE chunk carrying a tool-call fragment.
func toolChunk(idx int, id, name, args string) string {
	return fmt.Sprintf(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":%d,"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":null}]}`, idx, id, name, args)
}

// chatStreamFromBody spins up a one-shot httptest.Server that writes body as
// the response, then calls chatStream against it.
func chatStreamFromBody(t *testing.T, body string) (message, usage, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)

	cfg := minimalCfg(srv.URL)
	return chatStream(context.Background(), cfg, nil, nil, nil, nil)
}

// TestChatStream_ConcatenatesContent verifies that multiple content chunks are
// joined into a single final.Content string.
func TestChatStream_ConcatenatesContent(t *testing.T) {
	t.Parallel()

	body := sseBody(
		contentChunk("hello"),
		contentChunk(" world"),
		"[DONE]",
	)
	msg, _, err := chatStreamFromBody(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "hello world" {
		t.Errorf("Content = %q, want %q", msg.Content, "hello world")
	}
}

// TestChatStream_DoneTerminatesBeforeEOF verifies that [DONE] stops parsing
// even if more bytes follow in the stream.
func TestChatStream_DoneTerminatesBeforeEOF(t *testing.T) {
	t.Parallel()

	body := sseBody(
		contentChunk("stop here"),
		"[DONE]",
		contentChunk("ignored"),
	)
	msg, _, err := chatStreamFromBody(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(msg.Content, "ignored") {
		t.Errorf("Content = %q; content after [DONE] must not appear", msg.Content)
	}
}

// TestChatStream_EmptyStream returns an empty assistant message without error.
func TestChatStream_EmptyStream(t *testing.T) {
	t.Parallel()

	body := sseBody("[DONE]")
	msg, _, err := chatStreamFromBody(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "" {
		t.Errorf("Content = %q, want empty", msg.Content)
	}
	if len(msg.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %v, want nil", msg.ToolCalls)
	}
}

// TestChatStream_SkipsHeartbeatLines verifies that non-data SSE lines (e.g.
// comment lines starting with ":") are silently ignored.
func TestChatStream_SkipsHeartbeatLines(t *testing.T) {
	t.Parallel()

	body := ": heartbeat\n\n" +
		sseBody(contentChunk("ok"), "[DONE]")
	msg, _, err := chatStreamFromBody(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "ok" {
		t.Errorf("Content = %q, want %q", msg.Content, "ok")
	}
}

// TestChatStream_UnparsableChunkSkipped verifies that a malformed JSON chunk
// is skipped rather than aborting the stream.
func TestChatStream_UnparsableChunkSkipped(t *testing.T) {
	t.Parallel()

	body := sseBody(
		"not-valid-json",
		contentChunk("after bad"),
		"[DONE]",
	)
	msg, _, err := chatStreamFromBody(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "after bad" {
		t.Errorf("Content = %q, want %q", msg.Content, "after bad")
	}
}

// TestChatStream_APIErrorChunk verifies that an inline API error in the stream
// is surfaced as an error return.
func TestChatStream_APIErrorChunk(t *testing.T) {
	t.Parallel()

	errChunk := `{"error":{"message":"rate limit","type":"rate_limit_error","code":429}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", errChunk)
	}))
	t.Cleanup(srv.Close)

	cfg := minimalCfg(srv.URL)
	_, _, err := chatStream(context.Background(), cfg, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error from API error chunk, got nil")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("error = %q; want to contain 'rate limit'", err.Error())
	}
}

// TestChatStream_HTTP400_Error verifies that a non-2xx HTTP response is
// returned as an error without panicking.
func TestChatStream_HTTP400_Error(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"bad request"}}`)
	}))
	t.Cleanup(srv.Close)

	cfg := minimalCfg(srv.URL)
	_, _, err := chatStream(context.Background(), cfg, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for HTTP 400, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %q; want to contain '400'", err.Error())
	}
}

// TestChatStream_ContextCanceled_Error verifies that a canceled context
// propagates as an error.
func TestChatStream_ContextCanceled_Error(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	cfg := minimalCfg(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := chatStream(ctx, cfg, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error on canceled context, got nil")
	}
}

// TestChatStream_OnContentCallback verifies that the onContent callback
// receives each delta in order.
func TestChatStream_OnContentCallback(t *testing.T) {
	t.Parallel()

	body := sseBody(
		contentChunk("a"),
		contentChunk("b"),
		contentChunk("c"),
		"[DONE]",
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)

	cfg := minimalCfg(srv.URL)
	var got []string
	_, _, err := chatStream(context.Background(), cfg, nil, nil, func(delta string) {
		got = append(got, delta)
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(got, "") != "abc" {
		t.Errorf("callback deltas = %v, want [a b c]", got)
	}
}

// TestChatStream_SingleToolCall verifies that a well-formed tool-call stream
// is assembled into a ToolCalls entry on the final message.
func TestChatStream_SingleToolCall(t *testing.T) {
	t.Parallel()

	body := sseBody(
		toolChunk(0, "call-1", "read_file", `{"path":"/foo"}`),
		"[DONE]",
	)
	msg, _, err := chatStreamFromBody(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call-1" {
		t.Errorf("ID = %q, want %q", tc.ID, "call-1")
	}
	if tc.Function.Name != "read_file" {
		t.Errorf("Name = %q, want %q", tc.Function.Name, "read_file")
	}
	if tc.Function.Arguments != `{"path":"/foo"}` {
		t.Errorf("Arguments = %q, want %q", tc.Function.Arguments, `{"path":"/foo"}`)
	}
}

// TestChatStream_MultipleToolCalls verifies that two tool calls with different
// indices are assembled as separate entries (parallel_tool_calls disabled but
// the accumulator logic must handle multiple indices).
func TestChatStream_MultipleToolCalls(t *testing.T) {
	t.Parallel()

	body := sseBody(
		toolChunk(0, "call-1", "read_file", `{"path":"/a"}`),
		toolChunk(1, "call-2", "run_shell", `{"command":"ls"}`),
		"[DONE]",
	)
	msg, _, err := chatStreamFromBody(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("ToolCalls len = %d, want 2", len(msg.ToolCalls))
	}
}

// TestChatStream_ToolArgsDeltaStreamed verifies that argument fragments arriving
// across multiple chunks are concatenated correctly.
func TestChatStream_ToolArgsDeltaStreamed(t *testing.T) {
	t.Parallel()

	// First chunk carries id+name; subsequent chunks carry argument fragments.
	chunk1 := toolChunk(0, "call-x", "edit_file", `{"pa`)
	chunk2 := toolChunk(0, "", "", `th":"/b"}`)
	body := sseBody(chunk1, chunk2, "[DONE]")

	msg, _, err := chatStreamFromBody(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Function.Arguments != `{"path":"/b"}` {
		t.Errorf("Arguments = %q, want %q", msg.ToolCalls[0].Function.Arguments, `{"path":"/b"}`)
	}
}

// TestChatStream_DropsToolCallAccWithNoIDOrName verifies that an accumulator
// with neither ID nor Name is dropped from the final ToolCalls list.
func TestChatStream_DropsToolCallAccWithNoIDOrName(t *testing.T) {
	t.Parallel()

	// A chunk that sets index 0 accumulator but provides no id or name.
	emptyAcc := `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"","type":"function","function":{"name":"","arguments":""}}]},"finish_reason":null}]}`
	body := sseBody(emptyAcc, "[DONE]")

	msg, _, err := chatStreamFromBody(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %v, want empty — empty accumulator must be dropped", msg.ToolCalls)
	}
}

// TestChatStream_UsagePopulated verifies that when the server sends a usage
// chunk, it is returned in the usage return value.
func TestChatStream_UsagePopulated(t *testing.T) {
	t.Parallel()

	usageChunk := `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	body := sseBody(
		contentChunk("hi"),
		usageChunk,
		"[DONE]",
	)
	_, u, err := chatStreamFromBody(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.PromptTokens != 10 || u.CompletionTokens != 5 || u.TotalTokens != 15 {
		t.Errorf("usage = %+v, want {10 5 15}", u)
	}
}

// TestChatStream_OnToolArgDeltaCallback verifies that onToolArgDelta receives
// each argument fragment with the correct index and accumulated name.
func TestChatStream_OnToolArgDeltaCallback(t *testing.T) {
	t.Parallel()

	chunk1 := toolChunk(0, "call-z", "run_shell", `{"cm`)
	chunk2 := toolChunk(0, "", "", `d":"ls"}`)
	body := sseBody(chunk1, chunk2, "[DONE]")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)

	cfg := minimalCfg(srv.URL)
	var deltas []string
	_, _, err := chatStream(context.Background(), cfg, nil, nil, nil, func(idx int, name, delta string) {
		deltas = append(deltas, delta)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	combined := strings.Join(deltas, "")
	if combined != `{"cmd":"ls"}` {
		t.Errorf("arg deltas combined = %q, want %q", combined, `{"cmd":"ls"}`)
	}
}
