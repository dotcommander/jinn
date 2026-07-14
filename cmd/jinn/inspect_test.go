package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/dotcommander/jinn/internal/jinn"
)

func TestInspector_ListToolsAndSchema(t *testing.T) {
	t.Parallel()

	engine := jinn.New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	handler := newInspectorHandler(engine, "test")

	schemaReq, err := http.NewRequest(http.MethodGet, "/api/schema", nil)
	if err != nil {
		t.Fatalf("new schema request: %v", err)
	}
	schemaReq.Host = "127.0.0.1"
	schemaResp := httptest.NewRecorder()
	handler.ServeHTTP(schemaResp, schemaReq)
	if schemaResp.Code != http.StatusOK {
		t.Fatalf("schema status = %d", schemaResp.Code)
	}
	var schema []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.NewDecoder(schemaResp.Body).Decode(&schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	schemaNames := make([]string, 0, len(schema))
	for _, tool := range schema {
		schemaNames = append(schemaNames, tool.Function.Name)
	}
	wantNames, err := jinn.SchemaToolNames()
	if err != nil {
		t.Fatalf("schema tool names: %v", err)
	}
	if !reflect.DeepEqual(schemaNames, wantNames) {
		t.Fatalf("inspector schema tools = %v, want %v", schemaNames, wantNames)
	}

	capsReq, err := http.NewRequest(http.MethodGet, "/api/list_tools", nil)
	if err != nil {
		t.Fatalf("new list_tools request: %v", err)
	}
	capsReq.Host = "127.0.0.1"
	capsResp := httptest.NewRecorder()
	handler.ServeHTTP(capsResp, capsReq)
	if capsResp.Code != http.StatusOK {
		t.Fatalf("list_tools status = %d", capsResp.Code)
	}
	var caps jinn.ToolCapabilities
	if err := json.NewDecoder(capsResp.Body).Decode(&caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if !reflect.DeepEqual(caps.Tools, wantNames) {
		t.Fatalf("capability tools = %v, want %v", caps.Tools, wantNames)
	}
}

func TestInspector_RunTool(t *testing.T) {
	t.Parallel()

	engine := jinn.New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	handler := newInspectorHandler(engine, "test")

	req, err := http.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"tool":"list_dir","args":{"path":"."},"request_id":"inspect-test"}`))
	if err != nil {
		t.Fatalf("new run request: %v", err)
	}
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("run status = %d", resp.Code)
	}
	var got jinn.Response
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if !got.OK {
		t.Fatalf("expected ok response, got error %q", got.Error)
	}
	if got.RequestID != "inspect-test" {
		t.Fatalf("request id = %q", got.RequestID)
	}
	if got.Result == "" {
		t.Fatal("expected non-empty list_dir result")
	}
}

func TestValidateInspectorAddrRequiresLoopback(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{"127.0.0.1:8787", "localhost:8787", "[::1]:8787"} {
		if err := validateInspectorAddr(addr); err != nil {
			t.Fatalf("expected %q to be valid: %v", addr, err)
		}
	}
	for _, addr := range []string{":8787", "0.0.0.0:8787", "192.0.2.10:8787", "127.0.0.1"} {
		if err := validateInspectorAddr(addr); err == nil {
			t.Fatalf("expected %q to be rejected", addr)
		}
	}
}
