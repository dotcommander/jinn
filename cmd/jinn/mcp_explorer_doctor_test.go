package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestDoctorConcurrentChecksKeepOutputOrder(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	secondDone := make(chan struct{})
	aliases := []string{"alpha", "bravo", "charlie"}
	outcomes := make([]mcpDoctorLiveOutcome, len(aliases))
	for index, alias := range aliases {
		outcomes[index].check = mcpDoctorCheck{Alias: alias, Status: mcpDoctorStatusOK}
	}
	doctorConcurrentLiveChecks(ctx, time.Second, outcomes, func(ctx context.Context, _ time.Duration, check mcpDoctorCheck) mcpDoctorLiveOutcome {
		switch check.Alias {
		case "alpha":
			select {
			case <-secondDone:
			case <-ctx.Done():
				t.Errorf("bravo did not run concurrently: %v", ctx.Err())
			}
		case "bravo":
			close(secondDone)
		}
		return mcpDoctorLiveOutcome{check: check}
	})
	output := mcpDoctorOutput{SchemaVersion: 1}
	for _, outcome := range outcomes {
		output.Checks = append(output.Checks, outcome.check)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	var decoded mcpDoctorOutput
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for index, check := range decoded.Checks {
		if check.Alias != aliases[index] {
			t.Fatalf("output order at %d = %q, want %q", index, check.Alias, aliases[index])
		}
	}
}
