package jinn

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestAuditC301(t *testing.T) {
	// Serial: isolates the process-wide history configuration.
	e, dir := historyEngine(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, dir, "sub/nested/file.txt", "before")
	scoped, err := e.scopedPlanEngine("sub")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scoped.Close() })
	nested, err := scoped.scopedPlanEngine("nested")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nested.Close() })
	if nested.historyDir() != e.historyDir() {
		t.Fatal("nested history identity differs")
	}
	if _, err := nested.writeFile(map[string]any{"path": "file.txt", "content": "after"}); err != nil {
		t.Fatal(err)
	}
	history, err := e.loadHistory()
	if err != nil || len(history.Entries) != 1 {
		t.Fatalf("root history: %+v, %v", history, err)
	}
	entry := history.Entries[0]
	if entry.Target != filepath.Join("sub", "nested", "file.txt") {
		t.Fatalf("target = %q", entry.Target)
	}
	if _, err := e.undoRestore(map[string]any{"id": entry.ID}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "sub", "nested", "file.txt"))
	if err != nil || string(content) != "before" {
		t.Fatalf("restore = %q, %v", content, err)
	}
	if _, err := nested.scopedPlanEngine("../../.."); err == nil {
		t.Fatal("workspace escape accepted")
	}
}

func TestAuditC302(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	plan := &PlanTree{Root: "missing", Nodes: []PlanNode{
		{ID: "missing", Commands: []PlanOp{{Tool: "read_file", Args: map[string]any{"path": "absent"}}}, Edges: []PlanEdge{{When: Condition{Kind: "always"}, To: "recovery"}}},
		{ID: "recovery", Commands: []PlanOp{{Tool: "list_dir", Args: map[string]any{"path": "."}}}},
	}}
	result, err := e.runPlanTree(context.Background(), plan)
	if err != nil || !planRunSucceeded(result) {
		t.Fatalf("handled recovery: %+v, %v", result, err)
	}
	if result.Transcript[0].Ops[0].OK {
		t.Fatal("failed operation evidence lost")
	}
	for _, reason := range []StopReason{StopNoEdgeMatch, StopAborted, StopResourceLimit, StopMutationBlocked, StopMaxDepth, StopError} {
		result.StoppedReason = reason
		if planRunSucceeded(result) {
			t.Fatalf("abnormal stop succeeded: %s", reason)
		}
	}
	result.StoppedReason = StopLeaf
	result.Transcript[1].Ops[0].OK = false
	if planRunSucceeded(result) {
		t.Fatal("failed terminal node succeeded")
	}
}

func TestAuditC303(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "text", "needle")
	plan := &PlanTree{Root: "read", Nodes: []PlanNode{
		{ID: "read", Commands: []PlanOp{{Tool: "read_file", Args: map[string]any{"path": "text"}}}, Edges: []PlanEdge{{When: Condition{Kind: "match", Stream: "stdout", Regex: "needle"}, To: "matched"}}},
		{ID: "matched", Commands: []PlanOp{{Tool: "list_dir", Args: map[string]any{"path": "."}}}},
	}}
	result, err := e.runPlanTree(context.Background(), plan)
	if err != nil || len(result.PathTaken) != 2 {
		t.Fatalf("tool matching = %+v, %v", result, err)
	}
	for _, stream := range []string{"stdout", "stderr"} {
		matched, err := e.evaluateCondition(PlanOpResult{Stdout: "out", Stderr: "err", Classification: "success"}, Condition{Kind: "match", Stream: stream, Regex: "outerr"})
		if err != nil || !matched {
			t.Fatalf("combined shell output %s = %v, %v", stream, matched, err)
		}
	}
}

func TestAuditC304(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"f() { rm -rf /fixture; }", "f() ( rm -rf /fixture )", "f() ( g() ( rm -rf /fixture ); g )", "f() ( rm -rf /fixture", "function f ( rm -rf /fixture )"} {
		if risk, _ := ClassifyCommand(command); risk != RiskDangerous {
			t.Errorf("%q risk = %v", command, risk)
		}
	}
	for _, command := range []string{"f() ( echo harmless )", "f() { echo harmless; }"} {
		if risk, _ := ClassifyCommand(command); risk == RiskDangerous {
			t.Errorf("%q risk = %v", command, risk)
		}
	}
}

func TestAuditC3I01(t *testing.T) {
	// Serial: isolates the process-wide memory configuration.
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	absent, _ := testEngine(t)
	path, err := memoryDBPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := absent.memDBReadConn(context.Background()); !os.IsNotExist(err) {
		t.Fatalf("absent lookup = %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("absent read created storage: %v", err)
	}
	writer, _ := testEngine(t)
	if _, err := writer.memoryTool(context.Background(), map[string]any{"action": "save", "key": "key", "value": "value", "scope": "global"}); err != nil {
		t.Fatal(err)
	}
	reader, _ := testEngine(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.memDBReadConn(canceled); err == nil || reader.memReadDB != nil {
		t.Fatalf("canceled open retained handle: %v", err)
	}
	first, err := reader.memDBReadConn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		db, err := reader.memDBReadConn(context.Background())
		if err != nil || db != first {
			t.Fatalf("read handle not reused: %v", err)
		}
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.PingContext(context.Background()); err == nil {
		t.Fatal("owned read DB remains open")
	}
}

func TestAuditC3I02(t *testing.T) {
	t.Parallel()
	value := strings.Repeat("a", 1023) + "界end"
	got := boundPlanOpResult(PlanOpResult{Result: value}).Result
	if !utf8.ValidString(got) || got != strings.Repeat("a", 1023)+"…" {
		t.Fatalf("invalid boundary %q", got[len(got)-10:])
	}
}

func TestAuditC3R01(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	for _, shorthand := range []string{`stdout =~ /a/b/`, `stdout =~ /a\/b/`, `stdout =~ /https://example\.com/path/`} {
		condition, err := coerceCondition(shorthand)
		if err != nil {
			t.Fatalf("slash shorthand %q: %v", shorthand, err)
		}
		matched, err := e.evaluateCondition(PlanOpResult{Result: "a/b https://example.com/path"}, condition)
		if err != nil || !matched {
			t.Fatalf("shorthand %q: %v, %v", shorthand, matched, err)
		}
	}
	condition, err := coerceCondition(`stderr !~ /a\/b/`)
	if err != nil || !condition.Negate || condition.Regex != "a/b" {
		t.Fatalf("negated escaped delimiter: %+v, %v", condition, err)
	}
	if got := unescapeConditionDelimiter(`a\\/b\d+`); got != `a\\/b\d+` {
		t.Fatalf("regex escaping changed: %q", got)
	}
	for _, malformed := range []string{`stdout =~ a/b/`, `stdout =~ /a/b`, `stderr ~~ /a/b/`} {
		if _, err := coerceCondition(malformed); err == nil {
			t.Errorf("accepted malformed shorthand %q", malformed)
		}
	}
}

func TestAuditC3R02(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"grep missing file", "FOO=1 grep missing file", `"/usr/bin/grep" missing file`, `'/path with spaces/grep' missing file`, `env FOO=1 grep missing file`} {
		class, _ := classifyExitCode(extractArgv0(command), 1)
		if class != ClassExpectedNonzero {
			t.Fatalf("simple command %q: %s", command, class)
		}
	}
	for _, command := range []string{"grep needle file && false", "grep needle file | false", "grep needle file; false"} {
		if class, _ := classifyExitCode(extractArgv0(command), 1); class != ClassError {
			t.Fatalf("compound command %q: %s", command, class)
		}
	}
	e, dir := testEngine(t)
	writeTestFile(t, dir, "empty", "")
	plan := &PlanTree{Root: "query", Nodes: []PlanNode{{ID: "query", Commands: []PlanOp{{Shell: "FOO=1 grep needle empty"}}}}}
	result, err := e.runPlanTree(context.Background(), plan)
	if err != nil || !planRunSucceeded(result) || result.Transcript[0].Ops[0].Classification != string(ClassExpectedNonzero) {
		t.Fatalf("scoped original command classification: %+v, %v", result, err)
	}
}

func TestAuditC3R03(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panic.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	var held *sql.Tx
	var panicValue any
	func() {
		defer func() { panicValue = recover() }()
		_ = transact(context.Background(), db, func(tx *sql.Tx) error { held = tx; panic("fixture") })
	}()
	if panicValue != "fixture" || held == nil {
		t.Fatalf("panic did not propagate unchanged: %v", panicValue)
	}
	if stats := db.Stats(); stats.InUse != 0 {
		t.Fatalf("panic retained connection: %+v", stats)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("operation after panic = %v", err)
	}
	if err := held.Rollback(); err != sql.ErrTxDone {
		t.Fatalf("transaction was not rolled back: %v", err)
	}
	callbackErr := errors.New("callback failure")
	if err := transact(ctx, db, func(*sql.Tx) error { return callbackErr }); err != callbackErr {
		t.Fatalf("callback error changed: %v", err)
	}
	if err := transact(ctx, db, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatalf("commit after panic/error: %v", err)
	}
}
