package mcpbatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/voocel/mcp-sdk-go/protocol"
)

func TestAuditC405CallDeadline(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		caller Caller
	}{
		{"deadline error", &testCaller{waitForContext: true}},
		{"canceled error after deadline", auditC405CanceledCaller{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				input := batchInput(Call{ID: "deadline", Tool: "wait"})
				input.CallTimeout = time.Second
				results := Execute(t.Context(), test.caller, input, testProjector)
				if result := results[0]; result.Status != "timeout" || !strings.Contains(result.Error, "deadline") || result.ID != "deadline" || result.DurationMS != 1000 {
					t.Fatalf("deadline result = %+v", result)
				}
			})
		})
	}
}

type auditC405CanceledCaller struct{}

func (auditC405CanceledCaller) Call(ctx context.Context, _ string, _ map[string]any) (*protocol.CallToolResult, error) {
	<-ctx.Done()
	return nil, context.Canceled
}

func TestAuditC405ParentCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		input := batchInput(Call{ID: "canceled", Tool: "wait"})
		go func() { <-time.After(time.Second); cancel() }()
		input.CallTimeout = 2 * time.Second
		results := Execute(ctx, &testCaller{waitForContext: true}, input, testProjector)
		if result := results[0]; result.Status != "canceled" || result.ID != "canceled" || result.DurationMS != 1000 {
			t.Fatalf("canceled result = %+v", result)
		}
	})
}
