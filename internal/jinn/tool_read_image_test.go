package jinn

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// minimalPNG is a 1×1 transparent PNG (67 bytes). Used to test the image read path.
var minimalPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}

func TestReadImage_PNG(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	if err := os.WriteFile(dir+"/pixel.png", minimalPNG, 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	result, err := e.readFile(args("path", "pixel.png"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "image" {
		t.Fatalf("expected image content block, got: %+v", result)
	}
	if result.Content[0].MimeType != "image/png" {
		t.Errorf("expected image/png, got: %s", result.Content[0].MimeType)
	}
	if result.Content[0].Data == "" {
		t.Error("expected non-empty base64 data")
	}
	if result.Text != "" {
		t.Errorf("expected empty Text for image result, got: %s", result.Text)
	}
}

func TestReadImage_JPG(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// Minimal JPEG header (not a valid image, but enough for ext detection).
	if err := os.WriteFile(dir+"/photo.jpg", []byte{0xff, 0xd8, 0xff, 0xe0}, 0o644); err != nil {
		t.Fatalf("write jpg: %v", err)
	}
	result, err := e.readFile(args("path", "photo.jpg"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// .jpg must use image/jpeg MIME type, not image/jpg.
	if len(result.Content) != 1 || result.Content[0].MimeType != "image/jpeg" {
		t.Errorf("expected image/jpeg MIME, got: %+v", result)
	}
}

func TestReadPDF_Rejected(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	if err := os.WriteFile(dir+"/report.pdf", []byte("%PDF-1.4 fake"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	_, err := e.readFile(args("path", "report.pdf"))
	if err == nil {
		t.Fatal("expected error for PDF file")
	}
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ErrWithSuggestion, got %T: %v", err, err)
	}
	if !strings.Contains(sErr.Err.Error(), "pdf extraction not supported") {
		t.Errorf("unexpected error message: %v", sErr.Err)
	}
	if !strings.Contains(sErr.Suggestion, "pdftotext") {
		t.Errorf("expected pdftotext in suggestion, got: %s", sErr.Suggestion)
	}
}

func TestReadBinary_NotRegressed(t *testing.T) {
	t.Parallel()
	// Non-image binary (contains NUL byte) must still return the [binary file: …] string.
	e, dir := testEngine(t)
	if err := os.WriteFile(dir+"/data.bin", []byte("hello\x00world"), 0o644); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	result, err := e.readFile(args("path", "data.bin"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(result.Text, "[binary file:") {
		t.Errorf("expected [binary file: prefix, got: %s", result.Text)
	}
}
