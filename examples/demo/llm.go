package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message mirrors OpenAI chat/completions wire format. Any of Content /
// ToolCalls / ToolCallID may be empty depending on role.
type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name string `json:"name"`
	// Arguments is a JSON-encoded string per OpenAI spec, not an object.
	Arguments string `json:"arguments"`
}

type chatRequest struct {
	Model             string           `json:"model"`
	Messages          []message        `json:"messages"`
	Tools             []map[string]any `json:"tools,omitempty"`
	ParallelToolCalls bool             `json:"parallel_tool_calls"`
	Temperature       float64          `json:"temperature,omitempty"`
	TopP              float64          `json:"top_p,omitempty"`
	MaxTokens         int              `json:"max_tokens,omitempty"`
	Stream            bool             `json:"stream,omitempty"`
	StreamOptions     *streamOptions   `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type apiErr struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

func (e *apiErr) Error() string {
	return fmt.Sprintf("api error (%s): %s", e.Type, e.Message)
}

var httpClient = &http.Client{Timeout: 10 * time.Minute}

// chatStream opens a streaming chat/completions request and invokes onContent
// for each text delta. Tool calls are accumulated by index (per OpenAI spec)
// and returned assembled in the final message. Returns usage if the upstream
// reports it (zero-value otherwise).
//
// onToolArgDelta, when non-nil, is called for each tool-call argument chunk:
// idx is the tool-call index within the response, name is the tool function
// name (empty until first name-bearing chunk), delta is the raw JSON fragment.
func chatStream(ctx context.Context, cfg *config, messages []message, tools []map[string]any, onContent func(string), onToolArgDelta func(idx int, name, delta string)) (message, usage, error) {
	reqBody := chatRequest{
		Model:             cfg.model,
		Messages:          messages,
		Tools:             tools,
		ParallelToolCalls: false,
		Temperature:       cfg.temperature,
		TopP:              cfg.topP,
		MaxTokens:         cfg.maxTokens,
		Stream:            true,
		StreamOptions:     &streamOptions{IncludeUsage: true},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return message{}, usage{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.baseURL, bytes.NewReader(body))
	if err != nil {
		return message{}, usage{}, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+cfg.apiKey)
	req.Header.Set("HTTP-Referer", "https://github.com/dotcommander/jinn")
	req.Header.Set("X-Title", "demo")

	resp, err := httpClient.Do(req)
	if err != nil {
		return message{}, usage{}, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return message{}, usage{}, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(strings.TrimSpace(string(raw)), 500))
	}

	final := message{Role: "assistant"}
	var contentBuf strings.Builder
	var calls []toolCallAcc
	var usageOut usage

	br := bufio.NewReader(resp.Body)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return message{}, usage{}, fmt.Errorf("read stream: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk streamChunk
		if jerr := json.Unmarshal([]byte(data), &chunk); jerr != nil {
			// Providers occasionally emit heartbeat lines or inline errors;
			// skip unparseable chunks rather than aborting the stream.
			continue
		}
		if chunk.Error != nil {
			return message{}, usage{}, chunk.Error
		}
		if chunk.Usage != nil {
			usageOut = *chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta

		if delta.Content != "" {
			contentBuf.WriteString(delta.Content)
			if onContent != nil {
				onContent(delta.Content)
			}
		}
		for _, tc := range delta.ToolCalls {
			for len(calls) <= tc.Index {
				calls = append(calls, toolCallAcc{})
			}
			acc := &calls[tc.Index]
			if tc.ID != "" {
				acc.ID = tc.ID
			}
			if tc.Function.Name != "" {
				acc.Name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				acc.Args.WriteString(tc.Function.Arguments)
				if onToolArgDelta != nil {
					onToolArgDelta(tc.Index, acc.Name, tc.Function.Arguments)
				}
			}
		}
	}

	final.Content = contentBuf.String()
	for _, acc := range calls {
		if acc.ID == "" && acc.Name == "" {
			continue
		}
		final.ToolCalls = append(final.ToolCalls, toolCall{
			ID:       acc.ID,
			Type:     "function",
			Function: toolFunction{Name: acc.Name, Arguments: acc.Args.String()},
		})
	}
	return final, usageOut, nil
}

type toolCallAcc struct {
	ID   string
	Name string
	Args strings.Builder
}

type streamChunk struct {
	Choices []streamChoice `json:"choices"`
	Usage   *usage         `json:"usage,omitempty"`
	Error   *apiErr        `json:"error,omitempty"`
}

type streamChoice struct {
	Index        int         `json:"index"`
	Delta        streamDelta `json:"delta"`
	FinishReason string      `json:"finish_reason"`
}

type streamDelta struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []streamToolCall `json:"tool_calls"`
}

type streamToolCall struct {
	Index    int                `json:"index"`
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function streamToolFunction `json:"function"`
}

type streamToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
