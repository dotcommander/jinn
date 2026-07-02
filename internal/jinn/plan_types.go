package jinn

// DefaultMaxDepth mirrors predexec's DEFAULT_MAX_DEPTH.
const DefaultMaxDepth = 8

type PlanTree struct {
	Root     string     `json:"root"`
	Nodes    []PlanNode `json:"nodes"`
	Cwd      string     `json:"cwd,omitempty"`
	MaxDepth int        `json:"max_depth,omitempty"` // 0 => DefaultMaxDepth
	Force    bool       `json:"force,omitempty"`     // plan-level dangerous-mutation gate, Phase 2 only
}

type PlanNode struct {
	ID       string     `json:"id"`
	Commands []PlanOp   `json:"commands"`
	Parallel bool       `json:"parallel,omitempty"`
	Mutates  bool       `json:"mutates,omitempty"`
	Force    bool       `json:"force,omitempty"` // node-level dangerous-mutation gate, Phase 2 only
	Edges    []PlanEdge `json:"edges,omitempty"`
}

// PlanOp: exactly one of Shell / Tool is set. Mirrors Request{Tool,Args}
// (schema.go), not a flat {tool,...rest} shape.
type PlanOp struct {
	Shell string         `json:"shell,omitempty"`
	Tool  string         `json:"tool,omitempty"`
	Args  map[string]any `json:"args,omitempty"`
}

type PlanEdge struct {
	When Condition `json:"when"`
	To   string    `json:"to"`
}

// Condition: flattened union on Kind (not oneOf/const-union — avoids
// JSON-schema provider-compat issues when embedded in schema.json).
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

type StopReason string

const (
	StopLeaf            StopReason = "leaf"
	StopNoEdgeMatch     StopReason = "no_edge_match"
	StopMaxDepth        StopReason = "max_depth"
	StopMutationBlocked StopReason = "mutation_blocked"
	StopAborted         StopReason = "aborted"
	StopError           StopReason = "error"
)

type PlanOpResult struct {
	OK             bool   `json:"ok"`
	Result         string `json:"result,omitempty"`
	Error          string `json:"error,omitempty"`
	Classification string `json:"classification,omitempty"`
	Risk           string `json:"risk,omitempty"`
}

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
}
