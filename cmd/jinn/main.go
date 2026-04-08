package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotcommander/jinn/internal/jinn"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--schema":
			fmt.Println(jinn.Schema)
			return
		case "--version":
			fmt.Println(jinn.ResolveVersion(version))
			return
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	wd, err := os.Getwd()
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(jinn.Response{Error: fmt.Sprintf("getwd: %s", err)})
		os.Exit(1)
	}

	e := jinn.New(wd)

	var req jinn.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		json.NewEncoder(os.Stdout).Encode(jinn.Response{Error: fmt.Sprintf("invalid request: %s", err)})
		os.Exit(1)
	}

	result, err := e.Dispatch(ctx, req.Tool, req.Args)
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(jinn.Response{Error: err.Error()})
		os.Exit(1)
	}

	json.NewEncoder(os.Stdout).Encode(jinn.Response{OK: true, Result: result})
}
