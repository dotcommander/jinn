package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
)

// palette holds ANSI escape codes. Fields are empty strings when color is
// disabled (NO_COLOR, TERM=dumb, or non-TTY stderr) so callers can always
// concatenate without branching.
type palette struct {
	prompt, dim, errc, reset string
}

func newPalette(enabled bool) palette {
	if !enabled {
		return palette{}
	}
	return palette{
		prompt: "\x1b[36m", // cyan
		dim:    "\x1b[2m",  // faint
		errc:   "\x1b[31m", // red
		reset:  "\x1b[0m",
	}
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
		fmt.Fprintln(os.Stderr)

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
	// Closure over a mutex to serialize writes from any goroutines (SSE
	// reader is single-goroutine today, but this keeps the interface safe).
	var mu sync.Mutex
	return turnHooks{
		OnContent: func(delta string) {
			mu.Lock()
			defer mu.Unlock()
			fmt.Print(delta)
		},
		OnStreamEnd: func(hadContent bool) {
			mu.Lock()
			defer mu.Unlock()
			if hadContent {
				fmt.Println()
			}
		},
		OnToolCall: func(name, args string) {
			mu.Lock()
			defer mu.Unlock()
			fmt.Fprintf(os.Stderr, "%s  ⋅ %s  %s%s\n", p.dim, name, previewArgs(args), p.reset)
		},
	}
}

func printBanner(cfg *config, p palette) {
	cwd := filepath.Base(cfg.workDir)
	fmt.Fprintf(os.Stderr, "%sdemo · %s · %s%s\n", p.dim, cfg.model, cwd, p.reset)
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
