package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotcommander/jinn/internal/webfetch"
)

func TestAuditC503CLIContinuation(t *testing.T) {
	t.Parallel()
	doc := webfetch.Document{Content: "abcdefghijk"}
	var out bytes.Buffer
	if err := renderWebFetch(&out, doc, false, webOutputLimits{MaxBytes: 5}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--start-line=0 --start-line-byte=5") {
		t.Fatalf("missing continuation: %q", out.String())
	}
	out.Reset()
	if err := renderWebFetch(&out, doc, true, webOutputLimits{MaxBytes: 5}); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["next_start_line"] != float64(0) || result["next_start_line_byte"] != float64(5) {
		t.Fatalf("cursor = %#v", result)
	}
}
