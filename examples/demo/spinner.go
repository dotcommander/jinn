package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
	"unicode/utf8"
)

// palette holds ANSI escape codes. Fields are empty strings when color is
// disabled (NO_COLOR, TERM=dumb, or non-TTY stderr) so callers can always
// concatenate without branching.
type palette struct {
	prompt, dim, errc, reset                  string
	bold, italic, code, header, tool, success string
}

func newPalette(enabled bool) palette {
	if !enabled {
		return palette{}
	}
	return palette{
		prompt:  "\x1b[36m", // cyan
		dim:     "\x1b[2m",  // faint
		errc:    "\x1b[31m", // red
		reset:   "\x1b[0m",
		bold:    "\x1b[1m",
		italic:  "\x1b[3m",
		code:    "\x1b[7m",
		header:  "\x1b[35m",
		tool:    "\x1b[33m",
		success: "\x1b[32m",
	}
}

type spinner struct {
	mu        *sync.Mutex
	w         io.Writer
	p         palette
	stop      chan struct{}
	done      chan struct{}
	startTime time.Time
}

func newSpinner(w io.Writer, p palette, mu *sync.Mutex) *spinner {
	return &spinner{mu: mu, w: w, p: p}
}

func (s *spinner) start() {
	if s.stop != nil {
		s.halt()
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	s.startTime = time.Now()
	stop, done := s.stop, s.done
	startTime := s.startTime
	go func() {
		defer close(done)
		// Grace period: don't paint for fast responses. Exits cleanly if
		// halted during the wait (fast first-token case — no flicker).
		select {
		case <-stop:
			return
		case <-time.After(200 * time.Millisecond):
		}
		frames := "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
		i := 0
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		paint := func() {
			r, sz := utf8.DecodeRuneInString(frames[i:])
			s.mu.Lock()
			select {
			case <-stop:
				s.mu.Unlock()
				return
			default:
			}
			fmt.Fprintf(s.w, "\r%s%c thinking · %.1fs%s", s.p.dim, r, time.Since(startTime).Seconds(), s.p.reset)
			s.mu.Unlock()
			i += sz
			if i >= len(frames) {
				i = 0
			}
		}
		paint() // first frame immediately after grace period
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				paint()
			}
		}
	}()
}

func (s *spinner) halt() {
	if s.stop == nil {
		return
	}
	stop, done := s.stop, s.done
	s.stop, s.done = nil, nil
	close(stop)
	<-done
	s.mu.Lock()
	fmt.Fprintf(s.w, "\r\x1b[K")
	s.mu.Unlock()
}

func useColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if t := os.Getenv("TERM"); t == "dumb" {
		return false
	}
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
