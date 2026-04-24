package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// diffPreview lipgloss styles — added=green, removed=red, header/path=muted gray.
var (
	diffAddStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	diffRemoveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	diffHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// diffPreview extracts old_text/new_text from streaming tool-call JSON and
// renders a live diff preview to stderr. Safe to use when cfg.previewDiffs is
// false — Feed and Render become no-ops. Gracefully degrades when extraction
// fails (incomplete JSON, missing fields).
//
// Field extraction: scans the accumulated buffer for completed JSON string
// values using a simple regex. Avoids full JSON parsing since the buffer is
// often incomplete mid-stream.
//
// out is the destination for rendered output; defaults to os.Stderr when nil.
type diffPreview struct {
	enabled       bool
	out           io.Writer // nil → os.Stderr
	buf           strings.Builder
	path          string
	oldText       string
	newText       string
	shown         bool      // rendered at least once
	lastRend      time.Time // throttle: render at most every 200ms
	lastLineCount int       // lines written in previous render (for in-place erase)
}

// reJSONField matches a complete "key":"value" pair in partial JSON.
// Handles basic escaped quotes conservatively — stops at an unescaped quote.
var reJSONField = regexp.MustCompile(`"(path|old_text|new_text)"\s*:\s*"((?:[^"\\]|\\.)*)"`)

func newDiffPreview(enabled bool) *diffPreview {
	return &diffPreview{enabled: enabled}
}

// Feed appends delta to the buffer and attempts field extraction.
// Called for each streaming chunk of tool-call arguments.
func (d *diffPreview) Feed(delta string) {
	if !d.enabled {
		return
	}
	d.buf.WriteString(delta)
	d.extract()
}

// extract scans the current buffer for completed field values.
func (d *diffPreview) extract() {
	matches := reJSONField.FindAllStringSubmatch(d.buf.String(), -1)
	for _, m := range matches {
		val := strings.ReplaceAll(m[2], `\"`, `"`)
		val = strings.ReplaceAll(val, `\\`, `\`)
		val = strings.ReplaceAll(val, `\n`, "\n")
		val = strings.ReplaceAll(val, `\t`, "\t")
		switch m[1] {
		case "path":
			d.path = val
		case "old_text":
			d.oldText = val
		case "new_text":
			d.newText = val
		}
	}
}

// dest returns the output writer, defaulting to os.Stderr.
func (d *diffPreview) dest() io.Writer {
	if d.out != nil {
		return d.out
	}
	return os.Stderr
}

// isTTYWriter reports whether w is a TTY-backed file (os.Stderr, os.Stdout).
// Non-file writers (bytes.Buffer, etc.) always return false, ensuring escape
// sequences never leak into test output or piped output.
func isTTYWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// Render prints the current best-available diff to d.out (default: os.Stderr).
// Throttled to once per 200ms; only renders when at least one field is known.
// When the destination is a TTY, renders in-place by erasing the previous block
// first (cursor-up + clear-line). Non-TTY destinations get plain append output.
func (d *diffPreview) Render() {
	if !d.enabled {
		return
	}
	if d.path == "" && d.oldText == "" && d.newText == "" {
		return
	}
	if time.Since(d.lastRend) < 200*time.Millisecond && d.shown {
		return
	}
	d.lastRend = time.Now()
	d.shown = true

	w := d.dest()
	label := d.path
	if label == "" {
		label = "?"
	}

	// Build the block into a string so we can count lines before writing.
	var sb strings.Builder
	header := diffHeaderStyle.Render("┌─ preview: edit_file " + label)
	fmt.Fprintln(&sb, header)
	d.printDiffLines(&sb, d.oldText, d.newText)
	footer := diffHeaderStyle.Render("└─")
	fmt.Fprintln(&sb, footer)
	block := sb.String()

	tty := isTTYWriter(w)
	if tty && d.lastLineCount > 0 {
		// Erase previously-rendered block: cursor-up N lines, clear each line.
		for i := 0; i < d.lastLineCount; i++ {
			fmt.Fprint(w, "\x1b[1A\x1b[2K")
		}
	}

	fmt.Fprint(w, block)
	if tty {
		d.lastLineCount = strings.Count(block, "\n")
	}
}

// printDiffLines writes a styled line-by-line - / + diff to w.
// Caps at 20 lines total across both sides.
// Removed lines are rendered red, added lines green, truncation marker muted.
func (d *diffPreview) printDiffLines(w io.Writer, oldText, newText string) {
	const maxLines = 20
	old := strings.Split(oldText, "\n")
	neu := strings.Split(newText, "\n")
	printed := 0
	for _, l := range old {
		if printed >= maxLines {
			fmt.Fprintln(w, diffHeaderStyle.Render(fmt.Sprintf("│ ... truncated, %d more lines", len(old)+len(neu)-printed)))
			return
		}
		fmt.Fprintln(w, diffRemoveStyle.Render("│ - "+l))
		printed++
	}
	for _, l := range neu {
		if printed >= maxLines {
			fmt.Fprintln(w, diffHeaderStyle.Render(fmt.Sprintf("│ ... truncated, %d more lines", len(neu)-printed+len(old))))
			return
		}
		fmt.Fprintln(w, diffAddStyle.Render("│ + "+l))
		printed++
	}
}

// Reset clears state for the next tool call.
// On a TTY, erases the current live preview block so the terminal is clean.
func (d *diffPreview) Reset() {
	if d.lastLineCount > 0 {
		w := d.dest()
		if isTTYWriter(w) {
			for i := 0; i < d.lastLineCount; i++ {
				fmt.Fprint(w, "\x1b[1A\x1b[2K")
			}
		}
	}
	d.buf.Reset()
	d.path = ""
	d.oldText = ""
	d.newText = ""
	d.shown = false
	d.lastRend = time.Time{}
	d.lastLineCount = 0
}

// crlfWriter wraps w and translates bare '\n' to '\r\n'. It is a no-op for
// bytes that are already preceded by '\r'. Used during model turns when the
// TTY is in raw mode (ONLCR disabled) so streamed output still carriage-returns.
// Safe to use outside raw mode too: cooked-mode kernels translate the '\r' as a
// no-op carriage return at column 0. lastByte carries the trailing byte from the
// previous Write to detect cross-call '\r'|'\n' pairs without double-inserting '\r'.
type crlfWriter struct {
	w        io.Writer
	lastByte byte
}

func newCRLFWriter(w io.Writer) *crlfWriter { return &crlfWriter{w: w} }

func (c *crlfWriter) Write(p []byte) (int, error) {
	if bytes.IndexByte(p, '\n') < 0 {
		if len(p) > 0 {
			c.lastByte = p[len(p)-1]
		}
		_, err := c.w.Write(p)
		return len(p), err
	}
	// Build translated slice. Skip insertion when '\n' is already preceded by '\r',
	// consulting c.lastByte for the cross-Write boundary case (i == 0).
	out := make([]byte, 0, len(p)+8)
	for i := 0; i < len(p); i++ {
		if p[i] == '\n' {
			prev := c.lastByte
			if i > 0 {
				prev = p[i-1]
			}
			if prev != '\r' {
				out = append(out, '\r')
			}
		}
		out = append(out, p[i])
	}
	if len(p) > 0 {
		c.lastByte = p[len(p)-1]
	}
	// Report input length, not translated length — io.Writer contract: n ≤ len(p).
	_, err := c.w.Write(out)
	return len(p), err
}

// mdStream writes streaming text deltas directly to w. No markdown rendering —
// model output is printed as-is so it remains copy/paste clean and tokens
// appear live as they arrive.
type mdStream struct {
	w io.Writer
}

func newMDStream(w io.Writer, _ palette) *mdStream {
	return &mdStream{w: newCRLFWriter(w)}
}

// Write prints a streaming delta immediately.
func (m *mdStream) Write(delta string) {
	fmt.Fprint(m.w, delta)
}

// Flush is a no-op; Write is already live. Kept for hook-API compatibility.
func (m *mdStream) Flush() {}
