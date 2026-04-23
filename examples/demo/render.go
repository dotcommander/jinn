package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
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

// primaryField is the single most-informative field for each tool.
// When only this field (after noise removal) remains, show its value directly.
var primaryField = map[string]string{
	"run_shell":      "command",
	"read_file":      "path",
	"write_file":     "path",
	"edit_file":      "path",
	"multi_edit":     "path",
	"search_files":   "pattern",
	"stat_file":      "path",
	"list_dir":       "path",
	"checksum_tree":  "path",
	"detect_project": "path",
}

// filterToolArgs produces a compact display string from a tool's JSON args:
//   - strips "dry_run": false (noise when false, meaningful only when true)
//   - if a single primary field remains, returns just {value}
//   - otherwise falls through to truncated JSON
func filterToolArgs(name, argsJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return previewArgs(argsJSON)
	}
	// Strip dry_run when false — it's the default, not useful to display.
	if v, ok := m["dry_run"]; ok {
		if b, ok := v.(bool); ok && !b {
			delete(m, "dry_run")
		}
	}
	// If only the primary field remains, show {value} instead of full JSON.
	if pf, ok := primaryField[name]; ok && len(m) == 1 {
		if val, ok := m[pf]; ok {
			return fmt.Sprintf("{%v}", val)
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return previewArgs(argsJSON)
	}
	return previewArgs(string(out))
}

func previewArgs(argsJSON string) string {
	s := strings.TrimSpace(argsJSON)
	s = strings.ReplaceAll(s, "\n", " ")
	return truncate(s, 120)
}

// mdStream buffers streaming text deltas and renders them as a complete
// markdown document once the stream ends. Glamour cannot render partial
// markdown without flicker, so we trade per-token liveness (the spinner
// still runs) for a polished final render.
type mdStream struct {
	w   io.Writer
	buf strings.Builder
	r   *glamour.TermRenderer
}

func newMDStream(w io.Writer, _ palette) *mdStream {
	// WithAutoStyle honors COLORFGBG / $TERM and picks dark/light/notty.
	// WithWordWrap(0) disables hard-wrap — we let the terminal handle reflow
	// so users can copy-paste without artificial line breaks.
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		// Degrade to passthrough; never break the REPL over a styling failure.
		r = nil
	}
	return &mdStream{w: w, r: r}
}

// Write appends a streaming delta to the buffer. Rendering is deferred to Flush.
func (m *mdStream) Write(delta string) {
	m.buf.WriteString(delta)
}

// Flush renders the accumulated markdown and writes the result.
// Called once at OnStreamEnd. Safe to call on an empty buffer (no-op).
func (m *mdStream) Flush() {
	if m.buf.Len() == 0 {
		return
	}
	src := m.buf.String()
	m.buf.Reset()

	if m.r == nil {
		// Renderer init failed — write the raw markdown.
		fmt.Fprint(m.w, src)
		return
	}
	out, err := m.r.Render(src)
	if err != nil {
		fmt.Fprint(m.w, src)
		return
	}
	// Glamour appends a trailing newline; strip one to avoid double-spacing
	// against repl.go's post-stream Fprintln(os.Stderr) in OnStreamEnd.
	out = strings.TrimRight(out, "\n")
	fmt.Fprint(m.w, out)
}
