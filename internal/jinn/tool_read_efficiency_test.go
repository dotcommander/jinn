package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamTextLinesSinglePassContracts(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// Cross the sample and reader-buffer boundaries, including a long line,
	// a blank line, and a final line without a newline.
	first := strings.Repeat("x", 40<<10)
	content := first + "\n\nlast"
	writeTestFile(t, dir, "lines.txt", content)
	path := filepath.Join(dir, "lines.txt")
	want := []string{first, "", "last"}
	for _, needChecksum := range []bool{false, true} {
		lines, _, checksum, err := e.streamTextLines(path, needChecksum)
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) != len(want) {
			t.Fatalf("lines = %d, want %d", len(lines), len(want))
		}
		for i := range want {
			if lines[i] != want[i] {
				t.Errorf("line %d differs", i)
			}
		}
		if needChecksum {
			wantSum := sha256.Sum256([]byte(content))
			if checksum != hex.EncodeToString(wantSum[:]) {
				t.Errorf("checksum = %q, want %x", checksum, wantSum)
			}
		} else if checksum != "" {
			t.Errorf("unrequested checksum = %q", checksum)
		}
	}
}

func TestStreamTextLinesRejectedContentChecksum(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	for _, tc := range []struct {
		name, content string
	}{
		{"binary.dat", "\x00" + strings.Repeat("a", 40<<10)},
		{"document.pdf", "%PDF-1.4\n" + strings.Repeat("a", 40<<10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			writeTestFile(t, dir, tc.name, tc.content)
			path := filepath.Join(dir, tc.name)
			wantSum := sha256.Sum256([]byte(tc.content))
			for _, needChecksum := range []bool{false, true} {
				lines, _, checksum, err := e.streamTextLines(path, needChecksum)
				var suggested *ErrWithSuggestion
				if lines != nil || !errors.As(err, &suggested) || suggested.Code != ErrCodeBinaryFile {
					t.Fatalf("lines = %v, error = %v; want binary-file rejection", lines, err)
				}
				want := ""
				if needChecksum {
					want = hex.EncodeToString(wantSum[:])
				}
				if checksum != want {
					t.Errorf("checksum = %q, want %q", checksum, want)
				}
				result, currentErr := e.readFileContent(path, nil, needChecksum)
				if result == nil || result.Checksum != want || !errors.As(currentErr, &suggested) || suggested.Code != ErrCodeBinaryFile {
					t.Fatalf("window reader rejection = %+v, %v; want checksum %q", result, currentErr, want)
				}
			}
		})
	}
}
