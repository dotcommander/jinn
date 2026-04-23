package main

import (
	"fmt"
	"io"
	"strings"
)

// mdStream buffers streaming text deltas and renders complete markdown lines
// with ANSI escape codes. Lines are delimited by \n; partial trailing text
// stays in buf until the next Write or Flush.
type mdStream struct {
	w           io.Writer
	p           palette
	buf         strings.Builder
	inCodeBlock bool
	seenContent bool
}

func newMDStream(w io.Writer, p palette) *mdStream {
	return &mdStream{w: w, p: p}
}

// Write appends delta to the internal buffer, extracts all complete lines
// (separated by \n), and renders each one.
func (m *mdStream) Write(delta string) {
	m.buf.WriteString(delta)
	for {
		line, rest, ok := strings.Cut(m.buf.String(), "\n")
		if !ok {
			m.buf.Reset()
			m.buf.WriteString(line)
			return
		}
		m.writeLine(line)
		m.buf.Reset()
		m.buf.WriteString(rest)
	}
}

// Flush renders any text remaining in the buffer.
func (m *mdStream) Flush() {
	if m.buf.Len() == 0 {
		return
	}
	m.writeLine(m.buf.String())
	m.buf.Reset()
}

// writeLine renders a single complete line according to its markdown type.
func (m *mdStream) writeLine(line string) {
	if line == "" && !m.seenContent && !m.inCodeBlock {
		return
	}
	m.seenContent = true
	switch {
	case hasPrefix(line, 0, "```"):
		lang := strings.TrimPrefix(line, "```")
		lang = strings.TrimSpace(lang)
		m.inCodeBlock = !m.inCodeBlock
		if m.inCodeBlock {
			fmt.Fprintf(m.w, "%s┌%s%s\n", m.p.dim, lang, m.p.reset)
		} else {
			fmt.Fprintf(m.w, "%s└%s\n", m.p.dim, m.p.reset)
		}

	case m.inCodeBlock:
		fmt.Fprintf(m.w, "%s│ %s%s%s\n", m.p.dim, m.p.code, line, m.p.reset)

	case hasPrefix(line, 0, "### "):
		fmt.Fprintf(m.w, "%s%s%s%s\n", m.p.bold, m.p.header, line[4:], m.p.reset)

	case hasPrefix(line, 0, "## "):
		fmt.Fprintf(m.w, "%s%s%s%s\n", m.p.bold, m.p.header, line[3:], m.p.reset)

	case hasPrefix(line, 0, "# "):
		fmt.Fprintf(m.w, "%s%s%s%s\n", m.p.bold, m.p.header, line[2:], m.p.reset)

	case hasPrefix(line, 0, "> "):
		fmt.Fprintf(m.w, "%s│ %s%s\n", m.p.dim, renderInline(line[2:], m.p), m.p.reset)

	default:
		fmt.Fprintf(m.w, "%s\n", renderInline(line, m.p))
	}
}

// renderInline handles inline markdown patterns within a single line:
// **bold** and `code`. Everything else passes through unchanged.
func renderInline(s string, p palette) string {
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '*' && hasPrefix(s, i, "**") {
			close := strings.Index(s[i+2:], "**")
			if close >= 0 {
				out.WriteString(p.bold)
				out.WriteString(s[i+2 : i+2+close])
				out.WriteString(p.reset)
				i += close + 4 // past opening ** and closing **
				continue
			}
		}
		if s[i] == '`' && hasPrefix(s, i, "`") {
			close := strings.Index(s[i+1:], "`")
			if close >= 0 {
				out.WriteString(p.code)
				out.WriteString(s[i+1 : i+1+close])
				out.WriteString(p.reset)
				i += close + 2 // past opening ` and closing `
				continue
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// hasPrefix reports whether s[i:] starts with prefix.
func hasPrefix(s string, i int, prefix string) bool {
	return len(s)-i >= len(prefix) && s[i:i+len(prefix)] == prefix
}
