package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dotcommander/jinn/internal/jinn"
)

func TestAuditC101(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, origin, site, contentType, host string
		status                                int
	}{
		{name: "native", contentType: "application/json", status: 200},
		{name: "same origin", origin: "http://localhost:8787", site: "same-origin", contentType: "application/json; charset=utf-8", status: 200},
		{name: "foreign", origin: "https://evil.example", contentType: "application/json", status: 403},
		{name: "opaque", origin: "null", contentType: "application/json", status: 403},
		{name: "wrong scheme", origin: "https://localhost:8787", contentType: "application/json", status: 403},
		{name: "wrong port", origin: "http://localhost:8788", contentType: "application/json", status: 403},
		{name: "cross site", site: "cross-site", contentType: "application/json", status: 403},
		{name: "same site", site: "same-site", contentType: "application/json", status: 403},
		{name: "simple post", origin: "https://evil.example", contentType: "text/plain", status: 415},
		{name: "missing type", status: 415},
		{name: "foreign host", host: "evil.example", contentType: "application/json", status: 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			engine := jinn.New(root, "audit")
			t.Cleanup(func() { _ = engine.Close() })
			request := httptest.NewRequest(http.MethodPost, "http://localhost:8787/api/run", strings.NewReader(`{"tool":"write_file","args":{"path":"probe.txt","content":"probe","if_absent":true}}`))
			if test.host != "" {
				request.Host = test.host
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			request.Header.Set("Sec-Fetch-Site", test.site)
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			newInspectorHandler(engine, "audit").ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d: %s", response.Code, test.status, response.Body)
			}
			_, err := os.Stat(filepath.Join(root, "probe.txt"))
			if test.status == 200 && err != nil {
				t.Fatalf("accepted request did not dispatch: %v: %s", err, response.Body)
			}
			if test.status != 200 && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected request invoked mutation: %v", err)
			}
		})
	}
	t.Run("default port", func(t *testing.T) {
		t.Parallel()
		request := httptest.NewRequest("POST", "http://localhost/api/run", nil)
		request.Header.Set("Origin", "http://localhost:80")
		request.Header.Set("Content-Type", "application/json")
		if !inspectorRunAllowed(httptest.NewRecorder(), request) {
			t.Fatal("equivalent default port rejected")
		}
	})
}

func TestAuditC102(t *testing.T) {
	t.Parallel()
	if doctorModeMismatch("windows", 0o666, 0o600) || doctorModeMismatch("windows", 0o777, 0o700) {
		t.Fatal("Windows mode bits interpreted as ACL failures")
	}
	if !doctorModeMismatch("linux", 0o644, 0o600) || doctorModeMismatch("darwin", 0o600, 0o600) {
		t.Fatal("POSIX permission checks changed")
	}
}

func TestAuditC103(t *testing.T) {
	t.Parallel()
	for _, paths := range [][2]string{{"/dev/tty", "/dev/tty"}, {"CONIN$", "CONOUT$"}} {
		t.Run(paths[0], func(t *testing.T) {
			t.Parallel()
			inputPath := filepath.Join(t.TempDir(), "input")
			outputPath := filepath.Join(t.TempDir(), "output")
			if err := os.WriteFile(inputPath, []byte("1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(outputPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			var names []string
			terminal, err := openQuestionnaireConsoleFiles(func(name string, flag int, mode os.FileMode) (*os.File, error) {
				names = append(names, name)
				path := inputPath
				if name == "CONOUT$" {
					path = outputPath
				}
				return os.OpenFile(path, flag, mode)
			}, paths[0], paths[1])
			if err != nil {
				t.Fatal(err)
			}
			if paths[0] != paths[1] {
				questionnaire, err := jinn.DecodeQuestionnaire([]byte(questionnaireJSON))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := runQuestionnaire(questionnaire, terminal.input, terminal.output); err != nil {
					t.Fatal(err)
				}
			}
			if err := terminal.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := terminal.input.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("input was not closed: %v", err)
			}
			if _, err := terminal.output.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("output was not closed: %v", err)
			}
			if len(names) == 0 || names[0] != paths[0] {
				t.Fatalf("opened %v", names)
			}
		})
	}
}

func TestAuditC104(t *testing.T) {
	t.Parallel()
	value := map[string]string{"text": "newline\nbackslash\\quote\"Unicode日本語\x1b[31m\u202e\U0001d173"}
	encoded, err := renderMCPExplorerHuman(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("human JSON invalid: %v: %s", err, encoded)
	}
	if decoded["text"] != value["text"] {
		t.Fatalf("value changed: got=%q want=%q", decoded["text"], value["text"])
	}
	for _, raw := range []string{"\x1b", "\u202e", "\U0001d173"} {
		if bytes.Contains(encoded, []byte(raw)) {
			t.Fatalf("raw terminal control %q", raw)
		}
	}
}

func TestAuditC107(t *testing.T) {
	t.Parallel()
	command := "go"
	if err := doctorCommandExecutable(command); err != nil {
		t.Fatalf("PATH command: %v", err)
	}
	if err := doctorCommandExecutable("jinn-audit-command-that-does-not-exist"); err == nil || !strings.Contains(err.Error(), "lookup") {
		t.Fatalf("missing command error=%v", err)
	}
	path := filepath.Join(t.TempDir(), "not-executable")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := doctorCommandExecutable(path); err == nil || !strings.Contains(err.Error(), "not executable") {
			t.Fatalf("explicit path error=%v", err)
		}
	}
}

func TestAuditC1I01(t *testing.T) {
	t.Parallel()
	questionnaire, err := jinn.DecodeQuestionnaire([]byte(questionnaireJSON))
	if err != nil {
		t.Fatal(err)
	}
	questionnaire.Questions = append(questionnaire.Questions, questionnaire.Questions[0])
	var output bytes.Buffer
	result, err := runQuestionnaire(questionnaire, strings.NewReader("1\nbad\n0\n\n2\n"), &output)
	if err != nil || len(result.Answers) != 2 || *result.Answers[0].Answer != "Approve plan" || *result.Answers[1].Answer != "Do not implement" {
		t.Fatalf("answers=%#v error=%v", result, err)
	}
	if !strings.Contains(output.String(), "invalid choice") || !strings.Contains(output.String(), "custom answer is required") {
		t.Fatalf("missing validation feedback: %s", &output)
	}
	if _, err := runQuestionnaire(questionnaire, strings.NewReader("1\n"), io.Discard); !errors.Is(err, io.EOF) {
		t.Fatalf("EOF error=%v", err)
	}
	permanent := errors.New("closed terminal")
	if _, err := runQuestionnaire(questionnaire, auditBrokenReader{permanent}, io.Discard); !errors.Is(err, permanent) {
		t.Fatalf("permanent error=%v", err)
	}
	result, err = runQuestionnaire(questionnaire, strings.NewReader("1\nbad\nq\n"), io.Discard)
	if err != nil || !result.Cancelled || len(result.Answers) != 1 {
		t.Fatalf("cancel=%#v error=%v", result, err)
	}
}

type auditBrokenReader struct{ err error }

func (reader auditBrokenReader) Read([]byte) (int, error) { return 0, reader.err }
