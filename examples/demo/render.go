package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/glamour"
)

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
