package jinn

import (
	"encoding/json"
	"fmt"
	"strings"
)

//nolint:nestif // nested plan outcomes intentionally remain coupled to their parent result.
func planToolResult(tr *ToolResult, err error) PlanOpResult {
	res := PlanOpResult{OK: err == nil}
	if err != nil {
		res.Error = err.Error()
		res.ExitCode = 1
		return res
	}
	if tr != nil {
		res.Result = tr.Text
		if nested, ok := nestedPlanResult(tr); ok {
			if encoded, marshalErr := json.Marshal(nested); marshalErr == nil {
				res.Result = string(encoded)
			}
			if !planRunSucceeded(nested) {
				res.OK = false
				res.ExitCode = 1
				res.Error = fmt.Sprintf("nested run_plan stopped: %s", nested.StoppedReason)
			}
		}
	}
	return res
}

func nestedPlanResult(tr *ToolResult) (*PlanRunResult, bool) {
	if tr == nil || tr.Meta == nil {
		return nil, false
	}
	switch value := tr.Meta["plan_run"].(type) {
	case *PlanRunResult:
		return value, value != nil
	case PlanRunResult:
		return &value, true
	default:
		return nil, false
	}
}

func planRunSucceeded(result *PlanRunResult) bool {
	if result == nil || result.StoppedReason != StopLeaf || len(result.Transcript) == 0 {
		return false
	}
	for _, op := range result.Transcript[len(result.Transcript)-1].Ops {
		if !op.OK {
			return false
		}
	}
	return true
}

func planShellResult(text string, meta map[string]any, err error, command string) PlanOpResult {
	res := PlanOpResult{Result: text}
	res.Stdout, _ = meta["stdout"].(string)
	res.Stderr, _ = meta["stderr"].(string)
	if err != nil {
		res.Error = err.Error()
	}
	classification := string(ClassSuccess)
	if v, ok := meta["exit_code"].(int); ok {
		res.ExitCode = v
		class, reason := classifyExitCode(extractArgv0(command), v)
		classification = string(class)
		switch reported, _ := meta["classification"].(string); reported {
		case "timeout", "signal", "resource_limit":
			classification = reported
			reason = "command terminated before normal completion"
		}
		res.Classification = classification
		res.Result = rewritePlanShellClassification(res.Result, res.Classification, reason)
	} else if reported, _ := meta["classification"].(string); reported != "" {
		classification = reported
		res.Classification = reported
	}
	if err != nil || (classification != string(ClassSuccess) && classification != string(ClassExpectedNonzero)) {
		if res.ExitCode == 0 {
			res.ExitCode = 1
		}
	} else {
		res.OK = true
	}
	if strings.TrimSpace(command) != "" {
		risk, _ := ClassifyCommand(command)
		res.Risk = risk.String()
	} else if v, ok := meta["risk"].(string); ok {
		res.Risk = v
	}
	return res
}

func rewritePlanShellClassification(result, classification, reason string) string {
	const marker = "\n[classification:"
	idx := strings.LastIndex(result, marker)
	if idx < 0 {
		return result
	}
	end := strings.Index(result[idx:], "]")
	if end < 0 {
		return result
	}
	end += idx + 1
	return result[:idx] + fmt.Sprintf("\n[classification: %s — %s]", classification, reason) + result[end:]
}

func blockedPlanOp(message string) (PlanOpResult, bool) {
	return PlanOpResult{OK: false, Error: message, ExitCode: 1}, true
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func planShellCommand(cwd, command string) string {
	return "cd -- " + shellQuote(cwd) + " && " + command
}

func clonePlanArgs(args map[string]any) map[string]any {
	cloned := make(map[string]any, len(args)+1)
	for key, value := range args {
		cloned[key] = value
	}
	return cloned
}
