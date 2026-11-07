package jinn

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSearchRows_RanksWholeRecords(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "log.jsonl",
		`{"msg":"startup complete","level":"info"}`+"\n"+
			`{"msg":"database connection failed","level":"error"}`+"\n"+
			`{"msg":"database retry succeeded","level":"info"}`+"\n")

	out, err := e.searchRows(context.Background(), map[string]interface{}{
		"path":    "log.jsonl",
		"queries": []interface{}{"database"},
	})
	if err != nil {
		t.Fatalf("searchRows: %v", err)
	}
	var res searchRowsResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("rows error: %s", res.Error)
	}
	if res.TotalRows != 3 || res.MatchedRows != 2 {
		t.Errorf("total/matched = %d/%d, want 3/2", res.TotalRows, res.MatchedRows)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 ranked rows, got %+v", res.Rows)
	}
	if res.Rows[0].Line == 1 {
		t.Errorf("top row should not be the non-matching record: %+v", res.Rows)
	}
}

func TestSearchRows_RequiresPathAndQueries(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	if out, _ := e.searchRows(context.Background(), map[string]interface{}{"queries": []interface{}{"x"}}); out == "" {
		t.Fatal("expected error payload")
	}
	out, _ := e.searchRows(context.Background(), map[string]interface{}{"path": "missing.jsonl", "queries": []interface{}{"x"}})
	var res searchRowsResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Error == "" {
		t.Errorf("missing path must error, got: %s", out)
	}
}
