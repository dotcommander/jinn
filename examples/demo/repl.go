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
	"time"
)

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

	userTurns := 0

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
				userTurns = 0
			}
			continue
		}

		userMsg := line
		if cfg.rewritePrompts && shouldRewrite(line) {
			rewriteSigCtx, rcancel := signal.NotifyContext(ctx, os.Interrupt)
			rewriteCtx, rstop := startEscInterrupt(rewriteSigCtx)
			rewritten, rerr := rewriteUserInput(rewriteCtx, cfg, cfg.rewriterPrompt, line)
			rstop()
			rcancel()
			if rerr != nil {
				if errors.Is(rerr, context.Canceled) {
					fmt.Fprintf(os.Stderr, "\r\x1b[K%s(interrupted)%s\n", p.dim, p.reset)
				} else {
					fmt.Fprintf(os.Stderr, "%swarning: rewrite failed: %v — using raw input%s\n", p.dim, rerr, p.reset)
				}
			} else {
				printRewritePreview(p, line, rewritten)
				userMsg = rewritten
			}
		}
		messages = append(messages, message{Role: "user", Content: userMsg})
		userTurns++

		turnCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
		dispatchCtx, stop := startEscInterrupt(turnCtx)
		var cerr error
		messages, userTurns, cerr = maybeCompact(dispatchCtx, cfg, messages, userTurns)
		if errors.Is(cerr, errCompactionCanceled) {
			// The old turnCtx is poisoned by Ctrl-C/Esc. Re-arm both signal
			// and esc interrupters from the REPL root so the main turn has
			// a fresh cancellation chain.
			stop()
			cancel()
			turnCtx, cancel = signal.NotifyContext(ctx, os.Interrupt)
			dispatchCtx, stop = startEscInterrupt(turnCtx)
		}
		updated, terr := runTurns(dispatchCtx, cfg, tools, messages, replHooks(p))
		stop()
		cancel()

		messages = updated
		_ = saveSession(cfg, messages)

		fmt.Fprintln(os.Stderr)

		if terr != nil {
			if errors.Is(terr, context.Canceled) {
				fmt.Fprintf(os.Stderr, "\r\x1b[K%s(interrupted)%s\n\n", p.dim, p.reset)
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
	timer := newToolTimer(os.Stderr, p, &mu)

	return turnHooks{
		Timer: timer,
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
			spin.halt()
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

// printRewritePreview shows the original and rewritten text on stderr in the
// dim palette, sandwiched between divider lines. Non-interactive / no-TTY
// users see nothing here because the REPL itself only runs on a TTY.
func printRewritePreview(p palette, raw, rewritten string) {
	fmt.Fprintf(os.Stderr, "%s─── original ───%s\n", p.dim, p.reset)
	fmt.Fprintf(os.Stderr, "%s%s%s\n", p.dim, raw, p.reset)
	fmt.Fprintf(os.Stderr, "%s─── rewritten ───%s\n", p.dim, p.reset)
	fmt.Fprintf(os.Stderr, "%s%s%s\n", p.dim, rewritten, p.reset)
	fmt.Fprintf(os.Stderr, "%s────────────────%s\n", p.dim, p.reset)
}
