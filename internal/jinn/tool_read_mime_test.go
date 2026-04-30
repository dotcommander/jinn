package jinn

import (
	"os"
	"testing"
)

func TestReadFile_DetectsImageWithoutExtension(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// Write valid PNG bytes to a file with no extension — extension fallback must not be used.
	if err := os.WriteFile(dir+"/noext", minimalPNG, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := e.readFile(args("path", "noext"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) == 0 || result.Content[0].Type != "image" {
		t.Fatalf("expected image content block, got text: %s", result.Text)
	}
	if result.Content[0].MimeType != "image/png" {
		t.Errorf("expected MimeType=image/png, got: %s", result.Content[0].MimeType)
	}
}

func TestReadFile_DetectsSpoofedExtension(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// JPEG magic bytes in a file named .png — content-type detection wins over extension.
	jpegBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00}
	if err := os.WriteFile(dir+"/something.png", jpegBytes, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := e.readFile(args("path", "something.png"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) == 0 || result.Content[0].Type != "image" {
		t.Fatalf("expected image content block, got text: %s", result.Text)
	}
	if result.Content[0].MimeType != "image/jpeg" {
		t.Errorf("expected MimeType=image/jpeg (content wins over .png extension), got: %s", result.Content[0].MimeType)
	}
}

func TestReadFile_SVGStillWorks(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`
	if err := os.WriteFile(dir+"/test.svg", []byte(svg), 0o644); err != nil {
		t.Fatalf("write svg: %v", err)
	}

	result, err := e.readFile(args("path", "test.svg"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) == 0 || result.Content[0].Type != "image" {
		t.Fatalf("expected image content block for SVG, got text: %s", result.Text)
	}
	if result.Content[0].MimeType != "image/svg+xml" {
		t.Errorf("expected MimeType=image/svg+xml, got: %s", result.Content[0].MimeType)
	}
	if result.Content[0].Data == "" {
		t.Error("expected non-empty base64 data for SVG")
	}
}
