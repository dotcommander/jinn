package jinn

// mutatingAction declares one mutating tool action and the exact idempotency
// command string its handler MUST pass to runIdempotent. This slice is the
// single source of truth for "which tool+action mutate state and are therefore
// required to be idempotent". Adding a new mutating action means adding a row
// here AND wiring runIdempotent in the handler; the guard test
// (mutating_registry_test.go) fails loudly if the two ever drift.
//
// Read-only actions are intentionally ABSENT and must stay absent:
//   task.get, task.list, memory.recall, memory.list, event.list,
//   artifact.list, and resume peek (resumePeek performs zero writes).
type mutatingAction struct {
	Tool    string // Dispatch tool name (engine.go switch)
	Action  string // sub-action value (args["action"]); "" when the tool has no action fan-out
	Command string // exact command string passed to runIdempotent — must match the literal in the handler
}

// mutatingActions is the canonical mutating set. Command strings here MUST be
// byte-identical to the literals passed to runIdempotent in the handlers.
var mutatingActions = []mutatingAction{
	{Tool: "task", Action: "create", Command: "task.create"},
	{Tool: "task", Action: "begin", Command: "task.begin"},
	{Tool: "task", Action: "set_status", Command: "task.set_status"},
	{Tool: "memory", Action: "save", Command: "memory.save"},
	{Tool: "memory", Action: "forget", Command: "memory.forget"},
	{Tool: "memory", Action: "gc", Command: "memory.gc"},
	{Tool: "event", Action: "append", Command: "event.append"},
	{Tool: "artifact", Action: "add", Command: "artifact.add"},
	{Tool: "push", Action: "", Command: "push"},
	{Tool: "resume", Action: "", Command: "resume"},
}
