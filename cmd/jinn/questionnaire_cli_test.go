package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotcommander/jinn/internal/jinn"
)

const questionnaireJSON = `{"questions":[{"question":"Implement this plan?","header":"Approval","options":[{"value":"approve","label":"Approve plan","description":"Begin.","recommended":true},{"value":"stop","label":"Do not implement","description":"Stop."}]}]}`

func TestQuestionnaireValidateCLI(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := runQuestionnaireCLI([]string{"--validate"}, strings.NewReader(questionnaireJSON), &output); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true {
		t.Fatalf("result = %v", result)
	}
}

func TestRunQuestionnaireOptionCustomAndCancel(t *testing.T) {
	t.Parallel()
	questionnaire, err := jinn.DecodeQuestionnaire([]byte(questionnaireJSON))
	if err != nil {
		t.Fatal(err)
	}
	var display bytes.Buffer
	result, err := runQuestionnaire(questionnaire, strings.NewReader("1\n"), &display)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cancelled || *result.Answers[0].Answer != "Approve plan" {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Answers[0].Choices) != 1 || result.Answers[0].Choices[0].Value != "approve" || !result.Answers[0].Choices[0].Recommended {
		t.Fatalf("choices = %#v", result.Answers[0].Choices)
	}
	if !strings.Contains(display.String(), "Approve plan (recommended)") {
		t.Fatalf("display = %q", display.String())
	}
	custom, err := runQuestionnaire(questionnaire, strings.NewReader("0\nUse SQLite\n"), &display)
	if err != nil || custom.Answers[0].Kind != "custom" || *custom.Answers[0].Answer != "Use SQLite" {
		t.Fatalf("custom = %#v, err=%v", custom, err)
	}
	cancelled, err := runQuestionnaire(questionnaire, strings.NewReader("q\n"), &display)
	if err != nil || !cancelled.Cancelled {
		t.Fatalf("cancelled = %#v, err=%v", cancelled, err)
	}
}
