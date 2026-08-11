package mcpbatch

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/voocel/mcp-sdk-go/protocol"
)

const (
	statusCanceled       = "canceled"
	statusOK             = "ok"
	statusSkipped        = "skipped"
	statusToolError      = "tool_error"
	statusTransportError = "transport_error"
)

// Caller is the connected MCP client surface used by a batch worker pool.
type Caller interface {
	Call(context.Context, string, map[string]any) (*protocol.CallToolResult, error)
}

// Projector adapts normal tool results to the optional call projection contract.
type Projector func(*protocol.CallToolResult, Call) (any, error)

// Result is one ordered batch call outcome.
type Result struct {
	ID         string `json:"id"`
	OK         bool   `json:"ok"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Result     any    `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Execute invokes a prevalidated batch through one bounded worker pool.
func Execute(parent context.Context, caller Caller, input Input, projector Projector) []Result {
	results := make([]Result, len(input.Calls))
	for index, call := range input.Calls {
		results[index] = Result{ID: call.ID}
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	executor := executor{
		ctx:       ctx,
		cancel:    cancel,
		jobs:      make(chan int),
		results:   results,
		caller:    caller,
		input:     input,
		projector: projector,
	}
	return executor.run()
}

type executor struct {
	ctx       context.Context
	cancel    context.CancelFunc
	jobs      chan int
	results   []Result
	failed    atomic.Bool
	caller    Caller
	input     Input
	projector Projector
}

func (executor *executor) run() []Result {
	var workers sync.WaitGroup
	for range *executor.input.MaxConcurrency {
		workers.Go(executor.worker)
	}
	scheduled := executor.schedule()
	close(executor.jobs)
	workers.Wait()
	executor.finalizeUnscheduled(scheduled)
	return executor.results
}

func (executor *executor) worker() {
	for index := range executor.jobs {
		if executor.ctx.Err() != nil {
			if executor.input.FailFast && executor.failed.Load() {
				executor.results[index].Status = statusSkipped
			} else {
				executor.results[index].Status = statusCanceled
			}
			continue
		}
		result := executor.executeCall(index)
		executor.results[index] = result
		if !result.OK && executor.input.FailFast && executor.failed.CompareAndSwap(false, true) {
			executor.cancel()
		}
	}
}

func (executor *executor) schedule() int {
	scheduled := 0
	for scheduled < len(executor.input.Calls) {
		if executor.input.FailFast && executor.failed.Load() {
			break
		}
		select {
		case <-executor.ctx.Done():
			return scheduled
		case executor.jobs <- scheduled:
			scheduled++
		}
	}
	return scheduled
}

func (executor *executor) finalizeUnscheduled(scheduled int) {
	for index := scheduled; index < len(executor.results); index++ {
		if executor.input.FailFast && executor.failed.Load() {
			executor.results[index].Status = statusSkipped
		} else {
			executor.results[index].Status = statusCanceled
		}
	}
}

func (executor *executor) executeCall(index int) Result {
	call := executor.input.Calls[index]
	started := time.Now()
	ctx, cancel := context.WithTimeout(executor.ctx, executor.input.CallTimeout)
	result, err := executor.caller.Call(ctx, call.Tool, call.Arguments)
	cancel()
	output := Result{ID: call.ID, DurationMS: time.Since(started).Milliseconds()}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || executor.ctx.Err() != nil {
			output.Status = statusCanceled
		} else {
			output.Status, output.Error = statusTransportError, err.Error()
		}
		return output
	}
	if result == nil {
		output.Status, output.Error = statusTransportError, "MCP tools/call returned an empty result"
		return output
	}
	projected, err := executor.projector(result, call)
	if err != nil {
		output.Status, output.Error = statusTransportError, err.Error()
		return output
	}
	if result.IsError {
		output.Status, output.Result = statusToolError, projected
		return output
	}
	output.OK, output.Status, output.Result = true, statusOK, projected
	return output
}

// AllOK reports whether each returned result completed successfully.
func AllOK(results []Result) bool {
	for _, result := range results {
		if !result.OK {
			return false
		}
	}
	return true
}
