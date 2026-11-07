package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const lspBatchMaxQueries = 20

type lspBatchItem struct {
	Index      int    `json:"index"`
	OK         bool   `json:"ok"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

type lspBatchResult struct {
	Results      []lspBatchItem `json:"results"`
	Succeeded    int            `json:"succeeded"`
	Failed       int            `json:"failed"`
	ServerStarts int            `json:"server_starts"`
}

type lspBatchJob struct {
	index int
	req   lspRequest
}

func (e *Engine) lspBatch(ctx context.Context, args map[string]interface{}) (string, error) {
	return e.lspBatchWithLauncher(ctx, args, nil)
}

//nolint:gocognit,gocyclo,revive // branchy by design; splitting would scatter batch validation and dispatch
func (e *Engine) lspBatchWithLauncher(ctx context.Context, args map[string]interface{}, launcher lspLauncher) (string, error) {
	rawQueries, ok := args["queries"].([]interface{})
	if !ok || len(rawQueries) == 0 || len(rawQueries) > lspBatchMaxQueries {
		return "", &ErrWithSuggestion{Err: errors.New("queries must contain 1-20 items"), Suggestion: "provide 1-20 semantic queries", Code: ErrCodeInvalidArgs}
	}

	result := lspBatchResult{Results: make([]lspBatchItem, len(rawQueries))}
	groups := make(map[string][]lspBatchJob)
	groupArgv := make(map[string][]string)
	timeout := max(e.LSPTimeoutSec, 1)
	for index, raw := range rawQueries {
		result.Results[index].Index = index
		query, valid := raw.(map[string]interface{})
		if !valid {
			setLSPBatchError(&result.Results[index], &ErrWithSuggestion{Err: errors.New("query must be an object"), Suggestion: "use an object per query", Code: ErrCodeInvalidArgs})
			continue
		}
		req, err := e.parseLSPArgs(query, launcher == nil)
		if err != nil {
			setLSPBatchError(&result.Results[index], err)
			continue
		}
		if launcher == nil && req.action == "diagnostics" && req.ext == ".go" {
			out, queryErr := e.goDiagnostics(ctx, req, timeout)
			setLSPBatchResult(&result.Results[index], out, queryErr)
			continue
		}
		if launcher == nil && req.action == "symbols" && req.ext == ".go" && len(req.argv) == 0 {
			out, queryErr := goASTSymbols(req.absPath)
			setLSPBatchResult(&result.Results[index], out, queryErr)
			continue
		}
		key := strings.Join(req.argv, "\x00")
		groups[key] = append(groups[key], lspBatchJob{index: index, req: req})
		groupArgv[key] = req.argv
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result.ServerStarts = len(keys)
	groupResults := make([][]lspBatchItem, len(keys))
	var wg sync.WaitGroup
	for index, key := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			groupResults[index] = e.runLSPBatchGroup(ctx, groupArgv[key], groups[key], timeout, launcher)
		}()
	}
	wg.Wait()
	for _, items := range groupResults {
		for _, item := range items {
			result.Results[item.Index] = item
		}
	}
	for _, item := range result.Results {
		if item.OK {
			result.Succeeded++
		} else {
			result.Failed++
		}
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("lsp_batch marshal: %w", err)
	}
	return string(data), nil
}

func (e *Engine) runLSPBatchGroup(ctx context.Context, argv []string, jobs []lspBatchJob, timeout int, launcher lspLauncher) []lspBatchItem {
	items := make([]lspBatchItem, len(jobs))
	if launcher == nil {
		launcher = func(ctx context.Context, argv []string) (lspProc, error) {
			return launchLSP(ctx, argv, e.subprocessEnv())
		}
	}
	client := newLSPClient(launcher)
	if err := client.start(ctx, argv); err != nil {
		for index, job := range jobs {
			items[index].Index = job.index
			setLSPBatchError(&items[index], err)
		}
		return items
	}

	done := make(chan []lspBatchItem, 1)
	// The worker exits after every bounded job or when client.stop unblocks I/O.
	go func() {
		defer func() {
			client.shutdownBounded()
			client.stop()
		}()
		local := make([]lspBatchItem, len(jobs))
		if err := client.handshake(e.workDir); err != nil {
			for index, job := range jobs {
				local[index].Index = job.index
				setLSPBatchError(&local[index], err)
			}
			done <- local
			return
		}
		// A batch is one atomic tool call: each canonical file uses one snapshot
		// for all its queries, even if the file changes while the batch runs.
		byPath := make(map[string][]int)
		var paths []string
		for index, job := range jobs {
			local[index].Index = job.index
			if _, exists := byPath[job.req.absPath]; !exists {
				paths = append(paths, job.req.absPath)
			}
			byPath[job.req.absPath] = append(byPath[job.req.absPath], index)
		}
		for _, path := range paths {
			e.runLSPBatchFile(client, jobs, byPath[path], local)
		}
		done <- local
	}()

	budget := time.Duration(timeout*len(jobs)) * time.Second
	budget = min(budget, 2*time.Minute)
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case items = <-done:
		return items
	case <-ctx.Done():
		client.stop()
		items = <-done
		markLSPBatchInterrupted(items, &ErrWithSuggestion{Err: errors.New("lsp_batch canceled"), Suggestion: "retry if cancellation was unintended", Code: ErrCodeCanceled})
		return items
	case <-timer.C:
		client.stop()
		items = <-done
		markLSPBatchInterrupted(items, &ErrWithSuggestion{Err: errors.New("lsp_batch timed out"), Suggestion: "split the query batch", Code: ErrCodeTimeout})
		return items
	}
}

// runLSPBatchFile opens a single snapshot, dispatches its queries in their
// original relative order, and closes it before moving to the next file.
func (e *Engine) runLSPBatchFile(client *lspClient, jobs []lspBatchJob, indices []int, items []lspBatchItem) {
	first := jobs[indices[0]].req
	data, _, err := e.readRegularFile(first.absPath, maxLSPFileSize)
	if err != nil {
		err = fmt.Errorf("lsp didOpen read: %w", err)
	} else {
		err = client.didOpenText(first.absPath, langIDForExt(first.ext), data)
	}
	if err != nil {
		for _, index := range indices {
			setLSPBatchError(&items[index], err)
		}
		return
	}
	defer func() { _ = client.didClose(first.absPath) }()
	for _, index := range indices {
		req := jobs[index].req
		var queryErr error
		if req.needsSymbolResolution() {
			req, queryErr = e.resolveSymbolRequest(client, req)
		}
		if queryErr != nil {
			setLSPBatchError(&items[index], queryErr)
			continue
		}
		out, queryErr := e.dispatchLSPAction(client, req)
		setLSPBatchResult(&items[index], out, queryErr)
	}
}

func markLSPBatchInterrupted(items []lspBatchItem, err error) {
	for index := range items {
		if !items[index].OK {
			setLSPBatchError(&items[index], err)
		}
	}
}

func setLSPBatchResult(item *lspBatchItem, result string, err error) {
	if err != nil {
		setLSPBatchError(item, err)
		return
	}
	item.OK = true
	item.Result = result
}

func setLSPBatchError(item *lspBatchItem, err error) {
	item.Error = err.Error()
	item.ErrorCode, item.Suggestion = ErrorDetails(err)
	if item.ErrorCode == "" {
		item.ErrorCode = "lsp_error"
	}
}
