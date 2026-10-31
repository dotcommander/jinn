package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// legacyReadContent reconstructs the previous whole-file retention path.
func legacyReadContent(e *Engine, path string, args map[string]interface{}, sum bool) (*readContentResult, error) {
	lines, _, checksum, err := e.streamTextLines(path, sum)
	if err != nil {
		return &readContentResult{Checksum: checksum}, err
	}
	mode, err := parseTruncateMode(args)
	if err != nil {
		return nil, err
	}
	total := len(lines)
	if total == 0 {
		return &readContentResult{Checksum: checksum}, nil
	}
	start, end, err := resolveReadWindow(args, mode, total)
	if err != nil {
		return nil, err
	}
	numbered := true
	if value, ok := args["line_numbers"].(bool); ok {
		numbered = value
	}
	if result := oversizedLineResult(path, lines[start-1], start, total); result != nil {
		result.Checksum = checksum
		return result, nil
	}
	tr := applyTruncateStrategy(renderWindow(lines, start, end, numbered), mode, strings.ToLower(filepath.Ext(path)))
	spill := func(next int) (string, error) { return writeTruncationRemainder(path, next, lines[next-1:]) }
	sequential := mode == "head" || mode == "none" || mode == "smart"
	if result := byteTruncateResult(tr.Content, start, total, sequential, spill); result != nil {
		result.Checksum = checksum
		return result, nil
	}
	result := assembleReadResult(readWindow{startLine: start, endLine: end, total: total}, tr, mode, spill)
	result.Checksum = checksum
	return result, nil
}

func TestReadTextWindowLegacyParity(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var body strings.Builder
	for i := 1; i <= 4100; i++ {
		fmt.Fprintf(&body, "row %d: %s\n", i, strings.Repeat("x", i%43))
	}
	body.WriteString("final without newline")
	path := filepath.Join(dir, "parity.txt")
	writeTestFile(t, dir, "parity.txt", body.String())
	wantHash := sha256.Sum256([]byte(body.String()))
	cases := []struct {
		name string
		args map[string]interface{}
	}{
		{"default", map[string]interface{}{}},
		{"narrow", map[string]interface{}{"start_line": float64(3011), "end_line": float64(3014)}},
		{"tail", map[string]interface{}{"truncate": "tail"}},
		{"explicit_tail", map[string]interface{}{"tail": float64(70)}},
		{"middle", map[string]interface{}{"truncate": "middle", "end_line": float64(4101)}},
		{"smart", map[string]interface{}{"truncate": "smart", "end_line": float64(4101)}},
		{"none", map[string]interface{}{"truncate": "none", "end_line": float64(4101), "line_numbers": false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := e.readFileContent(path, tc.args, true)
			if err != nil {
				t.Fatal(err)
			}
			want, err := legacyReadContent(e, path, tc.args, true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if got.TempFile != "" {
					_ = os.Remove(got.TempFile)
				}
				if want.TempFile != "" {
					_ = os.Remove(want.TempFile)
				}
			})
			if got.TempFile != "" && want.TempFile != "" {
				actualSpill, err := os.ReadFile(got.TempFile)
				if err != nil {
					t.Fatal(err)
				}
				expectedSpill, err := os.ReadFile(want.TempFile)
				if err != nil {
					t.Fatal(err)
				}
				if string(actualSpill) != string(expectedSpill) {
					t.Error("spill bytes differ")
				}
			}
			if got.TempFile != "" {
				got.ByteHint = strings.ReplaceAll(got.ByteHint, got.TempFile, "<spill>")
			}
			if want.TempFile != "" {
				want.ByteHint = strings.ReplaceAll(want.ByteHint, want.TempFile, "<spill>")
			}
			got.TempFile, want.TempFile = "", ""
			if !reflect.DeepEqual(got, want) {
				t.Errorf("result differs: got %+v; want %+v", got, want)
			}
			if got.Checksum != hex.EncodeToString(wantHash[:]) {
				t.Error("full-file checksum differs")
			}
		})
	}
}

func BenchmarkReadTextWindowMemoryComparison(b *testing.B) {
	dir := b.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	e, err := NewWithConfig(dir, EngineConfig{Version: "dev", ShellMode: ShellModeUnsafe, UnsafeAllowMutationWithoutPreconditions: true})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = e.Close() })
	var content strings.Builder
	line := strings.Repeat("x", 80)
	for content.Len() < 8<<20 {
		fmt.Fprintf(&content, "%s\n", line)
	}
	if err := os.WriteFile(filepath.Join(dir, "bench.txt"), []byte(content.String()), 0o644); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(dir, "bench.txt")
	cases := []struct {
		name string
		args map[string]interface{}
	}{
		{"default", map[string]interface{}{}},
		{"narrow", map[string]interface{}{"start_line": float64(40000), "end_line": float64(40020)}},
		{"tail", map[string]interface{}{"truncate": "tail"}},
	}
	for _, tc := range cases {
		for _, legacy := range []bool{false, true} {
			name := tc.name + "/current"
			if legacy {
				name = tc.name + "/legacy"
			}
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					var result *readContentResult
					var err error
					if legacy {
						result, err = legacyReadContent(e, path, tc.args, false)
					} else {
						result, err = e.readFileContent(path, tc.args, false)
					}
					if err != nil {
						b.Fatal(err)
					}
					if result.TempFile != "" {
						if err := os.Remove(result.TempFile); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}
