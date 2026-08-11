package main

import (
	"strings"
	"testing"

	"github.com/dotcommander/jinn/internal/jinn"
)

func TestOneShotCompressionRemainsOptIn(t *testing.T) {
	t.Parallel()
	request, err := readRequest(strings.NewReader(`{"tool":"read_file","args":{"path":"README.md"}}`))
	if err != nil {
		t.Fatalf("readRequest: %v", err)
	}
	exact := "commit a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0\ncommit f9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0\n"
	result := &jinn.ToolResult{Text: exact}
	applyCompression(request, result)
	if result.Text != exact || result.Meta != nil {
		t.Fatalf("omitted compress changed one-shot output: text=%q meta=%#v", result.Text, result.Meta)
	}
}
