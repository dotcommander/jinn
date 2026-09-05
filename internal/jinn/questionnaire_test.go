package jinn

import (
	"strings"
	"testing"
)

func validQuestionnaire() Questionnaire {
	return Questionnaire{Questions: []Question{{
		Question: "Implement this plan?", Header: "Approval",
		Options: []Option{
			{Value: "approve", Label: "Approve plan", Description: "Begin.", Recommended: true},
			{Value: "stop", Label: "Do not implement", Description: "Stop."},
		},
	}}}
}

func TestValidateQuestionnaireCoreContract(t *testing.T) {
	t.Parallel()
	if err := ValidateQuestionnaire(validQuestionnaire()); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		mutate   func(*Questionnaire)
		contains string
	}{
		{"empty", func(q *Questionnaire) { q.Questions = nil }, "1-4"},
		{"duplicate question", func(q *Questionnaire) { q.Questions = append(q.Questions, q.Questions[0]) }, "unique"},
		{"blank header", func(q *Questionnaire) { q.Questions[0].Header = " " }, "header"},
		{"long header", func(q *Questionnaire) { q.Questions[0].Header = strings.Repeat("x", 17) }, "header"},
		{"few options", func(q *Questionnaire) { q.Questions[0].Options = q.Questions[0].Options[:1] }, "2-4"},
		{"reserved", func(q *Questionnaire) { q.Questions[0].Options[0].Label = "Other" }, "reserved"},
		{"duplicate label", func(q *Questionnaire) { q.Questions[0].Options[1].Label = q.Questions[0].Options[0].Label }, "unique"},
		{"blank value", func(q *Questionnaire) { q.Questions[0].Options[0].Value = " " }, "requires value"},
		{"duplicate value", func(q *Questionnaire) { q.Questions[0].Options[1].Value = q.Questions[0].Options[0].Value }, "values must be unique"},
		{"multiple recommended", func(q *Questionnaire) { q.Questions[0].Options[1].Recommended = true }, "at most one recommended"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := validQuestionnaire()
			tc.mutate(&q)
			if err := ValidateQuestionnaire(q); err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestQuestionnaireElicitationAndAnswers(t *testing.T) {
	t.Parallel()
	q := validQuestionnaire()
	schema := QuestionnaireElicitationSchema(q)
	properties := schema["properties"].(map[string]any)
	choice := properties["question_1_choice"].(map[string]any)
	if choice["title"] != "Approval" {
		t.Fatalf("title = %v", choice["title"])
	}
	if choice["default"] != "Approve plan" {
		t.Fatalf("default = %v", choice["default"])
	}
	result, err := QuestionnaireAnswers(q, map[string]any{"question_1_choice": "Approve plan"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Answers) != 1 || result.Answers[0].Kind != "option" || *result.Answers[0].Answer != "Approve plan" {
		t.Fatalf("result = %#v", result)
	}
	choices := result.Answers[0].Choices
	if len(choices) != 1 || choices[0].Value != "approve" || choices[0].Label != "Approve plan" || !choices[0].Recommended {
		t.Fatalf("choices = %#v", choices)
	}
}

func TestQuestionnaireMultiSelectDefaultsAndChoices(t *testing.T) {
	t.Parallel()
	q := validQuestionnaire()
	q.Questions[0].MultiSelect = true
	q.Questions[0].Options[1].Recommended = true
	schema := QuestionnaireElicitationSchema(q)
	properties := schema["properties"].(map[string]any)
	selected := properties["question_1_selected"].(map[string]any)
	defaults := selected["default"].([]string)
	if len(defaults) != 2 || defaults[0] != "Approve plan" || defaults[1] != "Do not implement" {
		t.Fatalf("defaults = %#v", defaults)
	}
	result, err := QuestionnaireAnswers(q, map[string]any{"question_1_selected": []any{"Approve plan", "Do not implement"}})
	if err != nil {
		t.Fatal(err)
	}
	choices := result.Answers[0].Choices
	if len(choices) != 2 || choices[0].Value != "approve" || choices[1].Value != "stop" || !choices[0].Recommended || !choices[1].Recommended {
		t.Fatalf("choices = %#v", choices)
	}
	_, err = QuestionnaireAnswers(q, map[string]any{"question_1_selected": []any{"Approve plan", "Approve plan"}})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate selection error = %v", err)
	}
}

func TestDecodeQuestionnaireRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	_, err := DecodeQuestionnaire([]byte(`{"questions":[{"question":"Q?","header":"H","options":[{"value":"a","label":"A","description":"a"},{"value":"b","label":"B","description":"b"}],"preview":"no"}]}`))
	if err == nil {
		t.Fatal("expected unknown-field error")
	}
}

func TestDecodeQuestionnaireRejectsTrailingJSON(t *testing.T) {
	t.Parallel()
	data := []byte(`{"questions":[{"question":"Q?","header":"H","options":[{"value":"a","label":"A","description":"a"},{"value":"b","label":"B","description":"b"}]}]} null`)
	_, err := DecodeQuestionnaire(data)
	if err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("error = %v", err)
	}
}
