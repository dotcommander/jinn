package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
	"unicode/utf8"
)

// toolTimerState tracks one tool's lifecycle. Running=true until Finish is
// called by the dispatcher goroutine.
type toolTimerState struct {
	name    string
	start   time.Time
	elapsed time.Duration
	running bool
}

// toolTimer renders a live elapsed-time status line for a batch of concurrent
// tool runs. Exactly one renderer goroutine reads state every 100ms and writes
// to w using \r carriage-return refresh. Caller lifecycle:
//
//	t := newToolTimer(os.Stderr, p, mu)
//	t.SetNames(names)   // must be called before Start
//	t.Start()
//	// ... dispatcher goroutines call t.Finish(i) when each tool ends ...
//	t.Stop()  // blocks until renderer exits; clears the line
//
// Non-TTY stderr: renderer prints a single "→ dispatching N tool(s)..." line
// at Start and "dispatched N tool(s) in Xs" at Stop; no ticker, no \r.
type toolTimer struct {
	w       io.Writer
	p       palette
	mu      *sync.Mutex // may be nil; acquired around writes when non-nil
	isTTY   bool
	states  []toolTimerState
	stateMu sync.Mutex
	stop    chan struct{}
	done    chan struct{}
	startTS time.Time
}

func newToolTimer(w io.Writer, p palette, mu *sync.Mutex) *toolTimer {
	return &toolTimer{w: w, p: p, mu: mu, isTTY: stderrIsTTY()}
}

// SetNames populates the per-tool state. Must be called before Start.
func (t *toolTimer) SetNames(names []string) {
	t.states = make([]toolTimerState, len(names))
	for i, n := range names {
		t.states[i] = toolTimerState{name: n, running: true}
	}
}

// Start launches the renderer goroutine. Exit condition: close(t.stop).
func (t *toolTimer) Start() {
	t.startTS = time.Now()
	for i := range t.states {
		t.states[i].start = t.startTS
	}
	if !t.isTTY {
		t.lockedFprintf("→ dispatching %d tool(s)...\n", len(t.states))
		return
	}
	t.stop = make(chan struct{})
	t.done = make(chan struct{})
	stop, done := t.stop, t.done
	go func() {
		defer close(done)
		frames := "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
		fi := 0
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				r, sz := utf8.DecodeRuneInString(frames[fi:])
				t.render(r)
				fi += sz
				if fi >= len(frames) {
					fi = 0
				}
			}
		}
	}()
}

// Finish marks tool index i as complete. Thread-safe.
func (t *toolTimer) Finish(i int) {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	if i < 0 || i >= len(t.states) {
		return
	}
	t.states[i].running = false
	t.states[i].elapsed = time.Since(t.states[i].start)
}

// Stop halts the renderer, waits for it to exit, and clears the status line.
func (t *toolTimer) Stop() {
	total := time.Since(t.startTS)
	if !t.isTTY {
		t.lockedFprintf("dispatched %d tool(s) in %.1fs\n", len(t.states), total.Seconds())
		return
	}
	if t.stop == nil {
		return
	}
	close(t.stop)
	<-t.done
	t.stop, t.done = nil, nil
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	fmt.Fprint(t.w, "\r\x1b[K")
}

// render paints one frame. Chooses aggregate vs per-tool based on 1.5s rule.
func (t *toolTimer) render(frame rune) {
	t.stateMu.Lock()
	escalate := false
	running := 0
	for _, s := range t.states {
		if s.running {
			running++
			el := time.Since(s.start)
			if el > 1500*time.Millisecond {
				escalate = true
			}
		}
	}
	snapshot := make([]toolTimerState, len(t.states))
	copy(snapshot, t.states)
	t.stateMu.Unlock()

	totalElapsed := time.Since(t.startTS)

	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	// Erase prior line before painting.
	fmt.Fprint(t.w, "\r\x1b[K")

	if !escalate {
		fmt.Fprintf(t.w, "%s%c %d tool(s) running · %.1fs%s",
			t.p.dim, frame, running, totalElapsed.Seconds(), t.p.reset)
		return
	}
	// Escalated multi-line view. Move cursor up after so the next frame
	// re-overwrites from the top. N lines = len(snapshot)+1.
	fmt.Fprintf(t.w, "%s%c running %d tools · %.1fs%s\n",
		t.p.dim, frame, len(snapshot), totalElapsed.Seconds(), t.p.reset)
	for i, s := range snapshot {
		var mark, timing string
		if s.running {
			el := time.Since(s.start)
			mark = "·"
			timing = fmt.Sprintf("%.1fs", el.Seconds())
		} else {
			mark = "✓"
			timing = fmt.Sprintf("%.1fs", s.elapsed.Seconds())
		}
		branch := "├─"
		if i == len(snapshot)-1 {
			branch = "└─"
		}
		fmt.Fprintf(t.w, "%s   %s %s %s %s%s\n",
			t.p.dim, branch, s.name, mark, timing, t.p.reset)
	}
	// Move cursor back up N+1 lines so next frame overwrites from the top.
	fmt.Fprintf(t.w, "\x1b[%dA", len(snapshot)+1)
}

func (t *toolTimer) lockedFprintf(format string, args ...any) {
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	fmt.Fprintf(t.w, format, args...)
}

// stderrIsTTY reports whether stderr is a character device (TTY).
// Mirrors stdinIsTTY / useColor pattern in spinner.go — no go-isatty dep.
func stderrIsTTY() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
