package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestImageConditionalChecksum(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	data := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	writeTestFile(t, dir, "image.png", string(data))
	h := sha256.Sum256(data)
	checksum := hex.EncodeToString(h[:])
	matched, err := e.readFile(args("path", "image.png", "if_checksum", checksum))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(matched.Text, `"unchanged":true`) || len(matched.Content) != 0 {
		t.Fatalf("expected unchanged without image payload: %+v", matched)
	}
	for _, conditional := range []string{"different", ""} {
		image, err := e.readFile(args("path", "image.png", "if_checksum", conditional))
		if err != nil || len(image.Content) != 1 || image.Content[0].Type != "image" {
			t.Fatalf("conditional %q: %+v, %v", conditional, image, err)
		}
	}
}

func TestReadFile_IncludeChecksumAddsMeta(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	content := "alpha\nbeta\n"
	path := "sum.txt"
	writeTestFile(t, e.workDir, path, content)

	expected := sha256.Sum256([]byte(content))
	expectedHex := hex.EncodeToString(expected[:])

	result, err := e.readFile(args("path", path, "include_checksum", true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := metaString(result.Meta, "sha256")
	if got != expectedHex {
		t.Errorf("expected sha256=%s, got %s", expectedHex, got)
	}
}

func TestReadFile_IfChecksumMatchReturnsUnchanged(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	content := "cached\ncontent\n"
	path := "if_match.txt"
	writeTestFile(t, e.workDir, path, content)

	expected := sha256.Sum256([]byte(content))
	expectedHex := hex.EncodeToString(expected[:])

	result, err := e.readFile(args("path", path, "if_checksum", expectedHex))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got map[string]any
	if unmarshalErr := json.Unmarshal([]byte(result.Text), &got); unmarshalErr != nil {
		t.Fatalf("expected unchanged JSON response, got error: %v", unmarshalErr)
	}

	unchanged, ok := got["unchanged"].(bool)
	if !ok {
		t.Fatalf("expected unchanged bool, got %T", got["unchanged"])
	}
	if !unchanged {
		t.Errorf("expected unchanged=true, got %v", got["unchanged"])
	}
	checksum, ok := got["checksum"].(string)
	if !ok {
		t.Fatalf("expected checksum string, got %T", got["checksum"])
	}
	if checksum != expectedHex {
		t.Errorf("expected checksum=%s, got %v", expectedHex, got["checksum"])
	}
	pathVal, ok := got["path"].(string)
	if !ok {
		t.Fatalf("expected path string in unchanged response, got %T", got["path"])
	}
	if !strings.HasSuffix(pathVal, path) {
		t.Errorf("expected unchanged path to end with %q, got %q", path, pathVal)
	}
}

func TestReadFile_IfChecksumMismatchFallsThrough(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	content := "cached\ncontent\n"
	path := "if_nomatch.txt"
	writeTestFile(t, e.workDir, path, content)

	result, err := e.readFile(args("path", path, "if_checksum", "000000000000000000000000000000000000000000000000000000000000000000"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Text, "cached") {
		t.Errorf("expected file content on checksum mismatch, got: %q", result.Text)
	}
}
