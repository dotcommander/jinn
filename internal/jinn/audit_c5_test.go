package jinn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dotcommander/jinn/internal/webfetch"
)

func TestAuditC503ToolCursorMetadata(t *testing.T) {
	t.Parallel()
	content := "abcdefghijklmnop\nnext\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(content))
	}))
	t.Cleanup(server.Close)
	engine, err := NewWithConfig(t.TempDir(), EngineConfig{Web: webfetch.Config{AllowPrivateNetworks: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	line, offset := 0, 0
	var output strings.Builder
	for range len(content) {
		result, _, err := engine.Dispatch(context.Background(), webFetchTool, args("url", server.URL, "raw", true, "max_bytes", float64(5), "start_line", float64(line), "start_line_byte", float64(offset)))
		if err != nil {
			t.Fatal(err)
		}
		output.WriteString(result.Text)
		line = result.Meta["next_start_line"].(int)
		offset = result.Meta["next_start_line_byte"].(int)
		if line == 0 && offset == 0 {
			break
		}
	}
	if output.String() != content {
		t.Fatalf("reconstructed %q, want %q", output.String(), content)
	}
}
