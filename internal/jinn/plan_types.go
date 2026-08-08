package jinn

// DefaultMaxDepth mirrors predexec's DEFAULT_MAX_DEPTH.
const DefaultMaxDepth = 8

const (
	maxPlanNodes      = 64
	maxPlanCommands   = 16
	maxPlanEdges      = 32
	maxPlanDepth      = 32
	maxPlanOperations = 256
	maxPlanTranscript = 200 << 10
)

// PlanTree is the condition-gated execution graph accepted by run_plan.
type PlanTree struct {
	Root     string     `json:"root"`
	Nodes    []PlanNode `json:"nodes"`
	Cwd      string     `json:"cwd,omitempty"`       // existing directory within the Engine sandbox; empty keeps Engine workDir
	MaxDepth int        `json:"max_depth,omitempty"` // 0 => DefaultMaxDepth at the outermost plan; nested 0 inherits, positive values can only lower the inherited ceiling
	Force    bool       `json:"force,omitempty"`     // plan-level dangerous-mutation gate, Phase 2 only
}

// PlanNode is a plan execution step and its outgoing conditional edges.
type PlanNode struct {
	ID       string     `json:"id"`
	Commands []PlanOp   `json:"commands"`
	Parallel bool       `json:"parallel,omitempty"`
	Mutates  bool       `json:"mutates,omitempty"`
	Force    bool       `json:"force,omitempty"` // node-level dangerous-mutation gate, Phase 2 only
	Edges    []PlanEdge `json:"edges,omitempty"`
}

// PlanOp has exactly one of Shell or Tool set. It mirrors Request{Tool,Args}
// (schema.go), not a flat {tool,...rest} shape.
type PlanOp struct {
	Shell string         `json:"shell,omitempty"`
	Tool  string         `json:"tool,omitempty"`
	Args  map[string]any `json:"args,omitempty"`
}

// PlanEdge selects the next plan node when its condition matches.
type PlanEdge struct {
	When Condition `json:"when"`
	To   string    `json:"to"`
}

// Condition is a flattened Kind union in Go. The wire schema validates its
// six forms with oneOf while this type stays straightforward to coerce.
type Condition struct {
	Kind    string `json:"kind"` // exitCode|fileExists|jsonPath|numeric|match|always
	Op      string `json:"op,omitempty"`
	Value   any    `json:"value,omitempty"`
	Path    string `json:"path,omitempty"`
	Extract string `json:"extract,omitempty"`
	Regex   string `json:"regex,omitempty"`
	Stream  string `json:"stream,omitempty"` // match only: stdout|stderr
	Negate  bool   `json:"negate,omitempty"`
}

// HighConfidenceKinds — "always" IS high-confidence (unconditional edges are
// unambiguous); only "match" (fuzzy regex) is low-confidence.
var HighConfidenceKinds = map[string]bool{
	"exitCode": true, "fileExists": true, "jsonPath": true,
	"numeric": true, "always": true,
}

// StopReason identifies why a plan execution ended.
type StopReason string

const (
	// StopLeaf indicates that the current node had no outgoing edges.
	StopLeaf StopReason = "leaf"
	// StopNoEdgeMatch indicates no outgoing condition matched.
	StopNoEdgeMatch StopReason = "no_edge_match"
	// StopMaxDepth indicates the configured depth limit was reached.
	StopMaxDepth StopReason = "max_depth"
	// StopMutationBlocked indicates a mutation did not pass its safety gate.
	StopMutationBlocked StopReason = "mutation_blocked"
	// StopAborted indicates context cancellation ended the plan.
	StopAborted StopReason = "aborted"
	// StopError indicates an execution or validation failure ended the plan.
	StopError StopReason = "error"
	// StopResourceLimit indicates an operation or transcript budget was exhausted.
	StopResourceLimit StopReason = "resource_limit"
)

// PlanOpResult records the outcome of one command or tool operation.
type PlanOpResult struct {
	OK             bool   `json:"ok"`
	Result         string `json:"result,omitempty"`
	Stdout         string `json:"stdout,omitempty"`
	Stderr         string `json:"stderr,omitempty"`
	Error          string `json:"error,omitempty"`
	Classification string `json:"classification,omitempty"`
	Risk           string `json:"risk,omitempty"`
	ExitCode       int    `json:"exit_code,omitempty"` // shell exit code, or nonzero for failed/blocked tool ops
}

// PlanNodeResult records one node and its operation outcomes.
type PlanNodeResult struct {
	NodeID string         `json:"node_id"`
	Depth  int            `json:"depth"`
	Ops    []PlanOpResult `json:"ops"`
}

// PlanRunResult rides in ToolResult.Meta["plan_run"].
type PlanRunResult struct {
	Transcript     []PlanNodeResult `json:"transcript"`
	PathTaken      []string         `json:"path_taken"`
	DepthReached   int              `json:"depth_reached"`
	StoppedReason  StopReason       `json:"stopped_reason"`
	EdgesEvaluated int              `json:"edges_evaluated"`
	EdgesMatched   int              `json:"edges_matched"`

	// These counters intentionally remain internal so the public plan_run
	// schema stays stable even when transcript fitting drops old entries.
	executedNodes      int
	executedOperations int
}
