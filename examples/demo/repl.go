package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// palette holds ANSI escape codes. Fields are empty strings when color is
// disabled (NO_COLOR, TERM=dumb, or non-TTY stderr) so callers can always
// concatenate without branching.
type palette struct {
	prompt, dim, errc, reset          string
	bold, code, header, tool, success string
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
		code:    "\x1b[7m",
		header:  "\x1b[35m",
		tool:    "\x1b[33m",
		success: "\x1b[32m",
	}
}

type spinner struct {
	mu   *sync.Mutex
	w    io.Writer
	p    palette
	stop chan struct{}
	done chan struct{}
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
	stop, done := s.stop, s.done
	go func() {
		defer close(done)
		frames := "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
		i := 0
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				r, sz := utf8.DecodeRuneInString(frames[i:])
				s.mu.Lock()
				// Re-check after acquiring the mutex: halt() may have
				// closed stop while we were parked, in which case we must
				// not paint another frame after halt's erase.
				select {
				case <-stop:
					s.mu.Unlock()
					return
				default:
				}
				fmt.Fprintf(s.w, "\r%s%c thinking…%s", s.p.dim, r, s.p.reset)
				s.mu.Unlock()
				i += sz
				if i >= len(frames) {
					i = 0
				}
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

// runREPL drives an interactive loop. Ctrl-D and /exit quit; Ctrl-C cancels
// the current turn and re-prompts. Outside of turns, SIGINT reaches the
// default handler and terminates the process (standard REPL contract).
func runREPL(ctx context.Context, cfg *config, messages []message) error {
	p := newPalette(useColor())

	tools, err := fetchToolsSchema(ctx, cfg)
	if err != nil {
		return err
	}

	printBanner(cfg, p)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)

	for {
		printPrompt(p)
		if !scanner.Scan() {
			fmt.Fprintln(os.Stderr)
			return scanner.Err()
		}
		line := strings.TrimRight(scanner.Text(), " \t\r")
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "/") {
			exit, reset := handleMeta(line, p)
			if exit {
				return nil
			}
			if reset {
				messages = resetMessages(messages)
			}
			continue
		}

		messages = append(messages, message{Role: "user", Content: line})

		turnCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
		updated, terr := runTurns(turnCtx, cfg, tools, messages, replHooks(p))
		cancel()

		messages = updated
		_ = saveSession(cfg, messages)

		fmt.Fprintln(os.Stderr)

		if terr != nil {
			if errors.Is(terr, context.Canceled) {
				fmt.Fprintf(os.Stderr, "%s(interrupted)%s\n\n", p.dim, p.reset)
				continue
			}
			fmt.Fprintf(os.Stderr, "%serror:%s %v\n\n", p.errc, p.reset, terr)
		}
	}
}

func replHooks(p palette) turnHooks {
	var mu sync.Mutex
	md := newMDStream(os.Stdout, p)
	spin := newSpinner(os.Stderr, p, &mu)

	return turnHooks{
		BeforeTurn: func() {
			spin.start()
		},
		OnContent: func(delta string) {
			spin.halt()
			mu.Lock()
			defer mu.Unlock()
			md.Write(delta)
		},
		OnStreamEnd: func(hadContent bool) {
			spin.halt() // must precede mu.Lock — halt() acquires mu internally
			mu.Lock()
			defer mu.Unlock()
			md.Flush()
			if hadContent {
				fmt.Println()
			}
		},
		OnToolCall: func(name, args string) {
			spin.halt()
			mu.Lock()
			defer mu.Unlock()
			fmt.Fprintf(os.Stderr, "%s  · %s%s%s %s%s%s\n",
				p.dim,
				p.tool, name, p.reset,
				p.dim, filterToolArgs(name, args), p.reset,
			)
		},
		OnToolResult: func(name string, elapsed time.Duration, err error) {
			mu.Lock()
			defer mu.Unlock()
			if elapsed >= 500*time.Millisecond {
				fmt.Fprintf(os.Stderr, "%s    %s %s (%.1fs)%s\n",
					p.dim,
					p.success, name, elapsed.Seconds(), p.reset,
				)
			}
			spin.start()
		},
	}
}

func printBanner(cfg *config, p palette) {
	cwd := filepath.Base(cfg.workDir)
	fmt.Fprintf(os.Stderr, "%sdemo%s · %s%s%s · %s%s%s\n",
		p.bold, p.reset,
		p.tool, cfg.model, p.reset,
		p.dim, cwd, p.reset,
	)
	fmt.Fprintf(os.Stderr, "%s/help · Ctrl-D to exit%s\n\n", p.dim, p.reset)
}

func printPrompt(p palette) {
	fmt.Fprintf(os.Stderr, "%s❯%s ", p.prompt, p.reset)
}

// handleMeta returns (exit, resetRequested).
func handleMeta(line string, p palette) (bool, bool) {
	cmd := strings.Fields(line)[0]
	switch cmd {
	case "/exit", "/quit", "/q":
		fmt.Fprintf(os.Stderr, "%sbye%s\n", p.dim, p.reset)
		return true, false
	case "/help":
		printReplHelp(p)
		return false, false
	case "/reset":
		fmt.Fprintf(os.Stderr, "%sconversation reset%s\n", p.dim, p.reset)
		return false, true
	case "/clear":
		fmt.Fprint(os.Stderr, "\x1b[H\x1b[2J\x1b[3J")
		return false, false
	default:
		fmt.Fprintf(os.Stderr, "%sunknown: %s — /help for commands%s\n", p.dim, cmd, p.reset)
		return false, false
	}
}

func printReplHelp(p palette) {
	for _, row := range [][2]string{
		{"/help", "show this message"},
		{"/reset", "start fresh (keeps system prompt)"},
		{"/clear", "clear the screen"},
		{"/exit", "quit (or press Ctrl-D)"},
	} {
		fmt.Fprintf(os.Stderr, "%s  %-7s %s%s\n", p.dim, row[0], row[1], p.reset)
	}
}

func resetMessages(messages []message) []message {
	for _, m := range messages {
		if m.Role == "system" {
			return []message{m}
		}
	}
	return nil
}
