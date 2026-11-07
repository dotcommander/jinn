package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

var planPhase1ToolAllowlist = map[string]bool{
	"read_file":    true,
	"multi_read":   true,
	"list_dir":     true,
	"search_files": true,
	"find_files":   true,
	"stat_file":    true,
	"lsp_query":    true,
	"lsp_batch":    true,
}

type planExecutionContextKey struct{}
type planNestedAuthorizationKey struct{}

type planOperationBudget struct {
	mu       sync.Mutex
	reserved int
}

// planExecutionContext is carried through nested run_plan dispatches. The
// outermost plan establishes the depth budget and dangerous-mutation
// authority. depth is the absolute graph depth of this plan tree's root.
// Nested plans may consume that budget and authority, but cannot reset or
// elevate either one.
type planExecutionContext struct {
	depth               int
	maxDepth            int
	dangerousAuthorized bool
	budget              *planOperationBudget
}

func planExecutionContextFor(ctx context.Context, plan *PlanTree) *planExecutionContext {
	if parent, ok := ctx.Value(planExecutionContextKey{}).(*planExecutionContext); ok {
		authorized, _ := ctx.Value(planNestedAuthorizationKey{}).(bool)
		depth := parent.depth + 1
		maxDepth := parent.maxDepth
		if plan.MaxDepth > 0 {
			maxDepth = min(maxDepth, depth+plan.MaxDepth)
		}
		return &planExecutionContext{
			depth:               depth,
			maxDepth:            maxDepth,
			dangerousAuthorized: authorized,
			budget:              parent.budget,
		}
	}

	maxDepth := plan.MaxDepth
	if maxDepth == 0 {
		maxDepth = DefaultMaxDepth
	}
	return &planExecutionContext{
		maxDepth:            maxDepth,
		dangerousAuthorized: true,
		budget:              &planOperationBudget{},
	}
}

func planPhase1ToolAllowed(op PlanOp) bool {
	if planPhase1ToolAllowlist[op.Tool] {
		return true
	}
	action, _ := op.Args["action"].(string)
	switch op.Tool {
	case "memory":
		return action == "recall" || action == "list"
	case "undo":
		return action == "list" || action == "preview"
	default:
		return false
	}
}

// reservePlanOperations enforces one operation budget for an outer plan and
// all nested plans. A nested run_plan may consume the remaining budget, but
// cannot reset it by starting another tree.
func reservePlanOperations(opCtx *planExecutionContext, count int) bool {
	opCtx.budget.mu.Lock()
	defer opCtx.budget.mu.Unlock()
	if count > maxPlanOperations-opCtx.budget.reserved {
		return false
	}
	opCtx.budget.reserved += count
	return true
}

// planToolRisk classifies a known mutating-node tool op for the Phase 2 risk
// gate. Nested run_shell commands use the same classifier as direct shell ops;
// callers cannot supply their own force authority.
func planToolRisk(tool string, args map[string]any) (RiskLevel, error) {
	descriptor, ok := lookupToolDescriptor(tool)
	if !ok {
		return RiskSafe, fmt.Errorf("unknown tool: %s", tool)
	}
	if tool == "run_shell" {
		command, _ := args["command"].(string)
		if strings.TrimSpace(command) == "" {
			return RiskSafe, errors.New("run_shell command is required")
		}
		risk, _ := ClassifyCommand(command)
		return risk, nil
	}

	switch tool {
	case "write_file", "edit_file", "multi_edit", "apply_patch", "search_replace":
		return RiskCaution, nil
	case "memory":
		action, _ := args["action"].(string)
		if action == "gc" {
			return RiskDangerous, nil
		}
		if action == "save" || action == "forget" {
			return RiskCaution, nil
		}
	case "undo":
		action, _ := args["action"].(string)
		if action == "clear" {
			return RiskDangerous, nil
		}
	}
	if descriptor.routeRisk == toolRouteRiskMutating {
		return RiskCaution, nil
	}
	return RiskSafe, nil
}

//nolint:funlen,gocognit,gocyclo,nestif,revive // execution ordering, cancellation, and first-match edges form one state machine.
func (e *Engine) runPlanTree(ctx context.Context, plan *PlanTree) (result *PlanRunResult, err error) {
	opCtx := planExecutionContextFor(ctx, plan)
	if opCtx.depth > opCtx.maxDepth {
		return newPlanRunResult(nil, nil, opCtx.depth, StopMaxDepth, 0, 0, 0, 0), nil
	}
	ctx = context.WithValue(ctx, planExecutionContextKey{}, opCtx)

	planEngine, err := e.scopedPlanEngine(plan.Cwd)
	if err != nil {
		return nil, err
	}
	// Best-effort JSONL audit trail (one event per completed node + a final
	// run event). A nil audit means the file could not be created; plan
	// execution proceeds identically either way.
	audit := newPlanAudit(planEngine.workDir)
	defer func() {
		if audit != nil {
			audit.runEvent(result)
			if result != nil {
				result.AuditPath = audit.relPath()
			}
			audit.close()
		}
	}()
	if planEngine != e {
		defer func() { _ = planEngine.Close() }()
	}
	byID := make(map[string]*PlanNode, len(plan.Nodes))
	for i := range plan.Nodes {
		byID[plan.Nodes[i].ID] = &plan.Nodes[i]
	}
	depth := 0
	currentID := plan.Root
	var pathTaken []string
	var transcript []PlanNodeResult
	edgesEvaluated, edgesMatched := 0, 0
	executedNodes, executedOperations := 0, 0

	for {
		if err := ctx.Err(); err != nil {
			return newPlanRunResult(transcript, pathTaken, opCtx.depth+depth, StopAborted, edgesEvaluated, edgesMatched, executedNodes, executedOperations), nil
		}
		if opCtx.depth+depth > opCtx.maxDepth {
			return newPlanRunResult(transcript, pathTaken, opCtx.depth+depth, StopMaxDepth, edgesEvaluated, edgesMatched, executedNodes, executedOperations), nil
		}
		node := byID[currentID]
		if !reservePlanOperations(opCtx, len(node.Commands)) {
			return newPlanRunResult(transcript, pathTaken, opCtx.depth+depth, StopResourceLimit, edgesEvaluated, edgesMatched, executedNodes, executedOperations), nil
		}
		executedNodes++
		pathTaken = append(pathTaken, currentID)

		nodeResult := PlanNodeResult{NodeID: currentID, Depth: opCtx.depth + depth}
		var lastOpResult PlanOpResult
		blocked := false
		nodeCtx := context.WithValue(ctx, planExecutionContextKey{}, &planExecutionContext{
			depth:               opCtx.depth + depth,
			maxDepth:            opCtx.maxDepth,
			dangerousAuthorized: opCtx.dangerousAuthorized,
			budget:              opCtx.budget,
		})

		runOp := func(op PlanOp) (PlanOpResult, bool) {
			result, isBlocked := planEngine.runPlanOp(nodeCtx, planEngine.workDir, op)
			return result, isBlocked
		}
		if node.Mutates {
			runOp = func(op PlanOp) (PlanOpResult, bool) {
				result, isBlocked := planEngine.runMutatingOp(nodeCtx, planEngine.workDir, node, plan.Force && opCtx.dangerousAuthorized, op)
				return result, isBlocked
			}
		}

		if node.Parallel && len(node.Commands) > 1 {
			ops := make([]PlanOpResult, len(node.Commands))
			blockedFlags := make([]bool, len(node.Commands))
			var wg sync.WaitGroup
			for i, op := range node.Commands {
				wg.Add(1)
				go func(i int, op PlanOp) {
					defer wg.Done()
					ops[i], blockedFlags[i] = runOp(op)
				}(i, op)
			}
			wg.Wait()
			executedOperations += len(node.Commands)
			for i, b := range blockedFlags {
				nodeResult.Ops = append(nodeResult.Ops, boundPlanOpResult(ops[i]))
				lastOpResult = ops[i]
				if b {
					blocked = true
				}
			}
		} else {
			for _, op := range node.Commands {
				if ctx.Err() != nil {
					break
				}
				opRes, isBlocked := runOp(op)
				executedOperations++
				nodeResult.Ops = append(nodeResult.Ops, boundPlanOpResult(opRes))
				lastOpResult = opRes
				if isBlocked {
					blocked = true
					break
				}
			}
		}

		transcript = append(transcript, nodeResult)
		audit.nodeEvent(nodeResult, edgesEvaluated, edgesMatched)
		if !planTranscriptWithinBudget(transcript, pathTaken, opCtx.depth+depth, edgesEvaluated, edgesMatched) {
			transcript, pathTaken = fitPlanTranscript(transcript, pathTaken, opCtx.depth+depth, edgesEvaluated, edgesMatched)
			if ctx.Err() != nil {
				return newPlanRunResult(transcript, pathTaken, opCtx.depth+depth, StopAborted, edgesEvaluated, edgesMatched, executedNodes, executedOperations), nil
			}
			return newPlanRunResult(transcript, pathTaken, opCtx.depth+depth, StopResourceLimit, edgesEvaluated, edgesMatched, executedNodes, executedOperations), nil
		}
		if ctx.Err() != nil {
			return newPlanRunResult(transcript, pathTaken, opCtx.depth+depth, StopAborted, edgesEvaluated, edgesMatched, executedNodes, executedOperations), nil
		}
		if blocked {
			return newPlanRunResult(transcript, pathTaken, opCtx.depth+depth, StopMutationBlocked, edgesEvaluated, edgesMatched, executedNodes, executedOperations), nil
		}

		evaluation := planNodeEvaluation{last: lastOpResult}
		// Stage-boundary gate: expect must hold on this node's last op result
		// before any edge is evaluated. A failed expectation stops the run
		// with the offending value recorded — the malformed result never
		// reaches edge routing, so it cannot silently misroute.
		if node.Expect != nil {
			expectOK, expectErr := planEngine.evaluateNodeCondition(&evaluation, *node.Expect)
			if expectErr != nil {
				return newPlanRunResult(transcript, pathTaken, opCtx.depth+depth, StopError, edgesEvaluated, edgesMatched, executedNodes, executedOperations), nil
			}
			if !expectOK {
				failed := newPlanRunResult(transcript, pathTaken, opCtx.depth+depth, StopExpectFailed, edgesEvaluated, edgesMatched, executedNodes, executedOperations)
				failed.ExpectFailure = &ExpectFailure{Node: currentID, Condition: *node.Expect, Value: boundExpectValue(lastOpResult)}
				return failed, nil
			}
		}

		matched := false
		for _, edge := range node.Edges {
			if ctx.Err() != nil {
				return newPlanRunResult(transcript, pathTaken, opCtx.depth+depth, StopAborted, edgesEvaluated, edgesMatched, executedNodes, executedOperations), nil
			}
			edgesEvaluated++
			ok, err := planEngine.evaluateNodeCondition(&evaluation, edge.When)
			if err != nil {
				return newPlanRunResult(transcript, pathTaken, opCtx.depth+depth, StopError, edgesEvaluated, edgesMatched, executedNodes, executedOperations), nil
			}
			if ok {
				edgesMatched++
				matched = true
				currentID = edge.To
				depth++
				break
			}
		}
		if !matched {
			reason := StopNoEdgeMatch
			if len(node.Edges) == 0 {
				reason = StopLeaf
			}
			return newPlanRunResult(transcript, pathTaken, opCtx.depth+depth, reason, edgesEvaluated, edgesMatched, executedNodes, executedOperations), nil
		}
	}
}

//nolint:revive // The aggregate constructor mirrors all result fields without hidden state.
func newPlanRunResult(transcript []PlanNodeResult, pathTaken []string, depth int, reason StopReason, evaluated, matched, executedNodes, executedOperations int) *PlanRunResult {
	return &PlanRunResult{
		Transcript:         transcript,
		PathTaken:          pathTaken,
		DepthReached:       depth,
		StoppedReason:      reason,
		EdgesEvaluated:     evaluated,
		EdgesMatched:       matched,
		executedNodes:      executedNodes,
		executedOperations: executedOperations,
	}
}

// boundPlanOpResult caps every serialized string field, not only Result, so
// adversarial stdout/stderr/error payloads cannot grow plan_run metadata past
// the documented 200 KiB response budget.
func boundPlanOpResult(result PlanOpResult) PlanOpResult {
	return boundPlanOpResultLimit(result, 1024)
}

func boundPlanOpResultLimit(result PlanOpResult, fieldLimit int) PlanOpResult {
	trim := func(value string) string {
		if len(value) <= fieldLimit {
			return value
		}
		end := fieldLimit
		for end > 0 && !utf8.RuneStart(value[end]) {
			end--
		}
		return value[:end] + "…"
	}
	result.Result = trim(result.Result)
	result.Error = trim(result.Error)
	result.Stdout = trim(result.Stdout)
	result.Stderr = trim(result.Stderr)
	return result
}

// boundExpectValue returns the bounded last-op output an expect condition was
// evaluated against: stdout+stderr when present (shell ops), else the result
// text (tool ops) — mirroring evaluateCondition's stream selection.
func boundExpectValue(last PlanOpResult) string {
	raw := last.Result
	if last.Classification != "" || last.Stdout != "" || last.Stderr != "" {
		raw = last.Stdout + last.Stderr
	}
	return boundPlanOpResultLimit(PlanOpResult{Result: raw}, 1024).Result
}

func compactPlanTranscript(transcript []PlanNodeResult) {
	for i := range transcript {
		for j := range transcript[i].Ops {
			transcript[i].Ops[j] = boundPlanOpResultLimit(transcript[i].Ops[j], 16)
		}
	}
}

func fitPlanTranscript(transcript []PlanNodeResult, pathTaken []string, depth, evaluated, matched int) ([]PlanNodeResult, []string) {
	compactPlanTranscript(transcript)
	for !planTranscriptWithinBudget(transcript, pathTaken, depth, evaluated, matched) {
		switch {
		case len(transcript) > 0:
			transcript = transcript[1:]
		case len(pathTaken) > 0:
			pathTaken = pathTaken[1:]
		default:
			return nil, nil
		}
	}
	return transcript, pathTaken
}

func planTranscriptWithinBudget(transcript []PlanNodeResult, pathTaken []string, depth, evaluated, matched int) bool {
	// Budget with the longest stop-reason value so a later terminal decision
	// cannot make an already-fitted result exceed the serialized cap.
	data, err := json.Marshal(PlanRunResult{Transcript: transcript, PathTaken: pathTaken, DepthReached: depth, StoppedReason: StopMutationBlocked, EdgesEvaluated: evaluated, EdgesMatched: matched})
	return err == nil && len(data) <= maxPlanTranscript
}

func shapePlanTranscript(nodes []PlanNodeResult) []PlanNodeResult {
	total := 0
	for _, n := range nodes {
		for _, op := range n.Ops {
			total += len(op.Result)
		}
	}
	if total <= PlanTranscriptMaxBytes {
		return nodes
	}
	result := make([]PlanNodeResult, len(nodes))
	copy(result, nodes)
	for i := range result {
		nodeTotal := 0
		for _, op := range result[i].Ops {
			nodeTotal += len(op.Result)
		}
		newOps := make([]PlanOpResult, len(result[i].Ops))
		for j, op := range result[i].Ops {
			newOps[j] = op
			newOps[j].Result = "[omitted: transcript cap exceeded]"
		}
		result[i].Ops = newOps
		total -= nodeTotal
		if total <= PlanTranscriptMaxBytes {
			break
		}
	}
	return result
}
