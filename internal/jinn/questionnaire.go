package jinn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Questionnaire limits are shared by the CLI, MCP schema, and answer parser.
const (
	MaxQuestions    = 4
	MinOptions      = 2
	MaxOptions      = 4
	MaxHeaderLength = 16
	MaxLabelLength  = 60
)

var reservedQuestionnaireLabels = map[string]struct{}{
	"Other": {}, "Type something.": {}, "Next": {},
}

// Questionnaire is the validated structured-question input shared by Jinn frontends.
type Questionnaire struct {
	Questions []Question `json:"questions"`
}

// Question describes one single- or multi-select prompt.
type Question struct {
	Question    string   `json:"question"`
	Header      string   `json:"header"`
	Options     []Option `json:"options"`
	MultiSelect bool     `json:"multiSelect,omitempty"`
}

// Option is one authored answer choice.
type Option struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Recommended bool   `json:"recommended,omitempty"`
}

// QuestionnaireChoice preserves the authored metadata for a selected option.
type QuestionnaireChoice struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Recommended bool   `json:"recommended"`
}

// QuestionnaireAnswer is one normalized answer from a questionnaire.
type QuestionnaireAnswer struct {
	QuestionIndex int                   `json:"questionIndex"`
	Question      string                `json:"question"`
	Kind          string                `json:"kind"`
	Answer        *string               `json:"answer"`
	Selected      []string              `json:"selected,omitempty"`
	Choices       []QuestionnaireChoice `json:"choices,omitempty"`
}

// QuestionnaireResult is the normalized questionnaire response.
type QuestionnaireResult struct {
	Answers   []QuestionnaireAnswer `json:"answers"`
	Cancelled bool                  `json:"cancelled"`
}

// DecodeQuestionnaire decodes and validates a questionnaire JSON document.
func DecodeQuestionnaire(data []byte) (Questionnaire, error) {
	var questionnaire Questionnaire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&questionnaire); err != nil {
		return Questionnaire{}, fmt.Errorf("decode questionnaire: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Questionnaire{}, errors.New("decode questionnaire: trailing JSON")
	}
	if err := ValidateQuestionnaire(questionnaire); err != nil {
		return Questionnaire{}, err
	}
	return questionnaire, nil
}

// ValidateQuestionnaire checks the bounds and uniqueness rules for a questionnaire.
func ValidateQuestionnaire(questionnaire Questionnaire) error {
	if len(questionnaire.Questions) < 1 || len(questionnaire.Questions) > MaxQuestions {
		return fmt.Errorf("questions must contain 1-%d entries", MaxQuestions)
	}
	seenQuestions := make(map[string]struct{}, len(questionnaire.Questions))
	for index, question := range questionnaire.Questions {
		if err := validateQuestion(index, question, seenQuestions); err != nil {
			return err
		}
	}
	return nil
}

func validateQuestion(index int, question Question, seenQuestions map[string]struct{}) error {
	if strings.TrimSpace(question.Question) == "" {
		return fmt.Errorf("questions[%d].question is required", index)
	}
	if _, exists := seenQuestions[question.Question]; exists {
		return errors.New("question text must be unique within an invocation")
	}
	seenQuestions[question.Question] = struct{}{}
	if strings.TrimSpace(question.Header) == "" {
		return fmt.Errorf("questions[%d].header is required", index)
	}
	if len([]rune(question.Header)) > MaxHeaderLength {
		return fmt.Errorf("questions[%d].header exceeds %d characters", index, MaxHeaderLength)
	}
	if len(question.Options) < MinOptions || len(question.Options) > MaxOptions {
		return fmt.Errorf("questions[%d].options must contain %d-%d entries", index, MinOptions, MaxOptions)
	}
	return validateQuestionOptions(index, question)
}

func validateQuestionOptions(index int, question Question) error {
	seenLabels := make(map[string]struct{}, len(question.Options))
	seenValues := make(map[string]struct{}, len(question.Options))
	recommendedCount := 0
	for optionIndex, option := range question.Options {
		if err := validateQuestionOption(index, optionIndex, option, seenLabels, seenValues); err != nil {
			return err
		}
		if option.Recommended {
			recommendedCount++
		}
	}
	if !question.MultiSelect && recommendedCount > 1 {
		return fmt.Errorf("single-select question %d must have at most one recommended option", index)
	}
	return nil
}

func validateQuestionOption(index, optionIndex int, option Option, seenLabels, seenValues map[string]struct{}) error {
	if strings.TrimSpace(option.Value) == "" || strings.TrimSpace(option.Label) == "" || strings.TrimSpace(option.Description) == "" {
		return fmt.Errorf("questions[%d].options[%d] requires value, label, and description", index, optionIndex)
	}
	if len([]rune(option.Label)) > MaxLabelLength {
		return fmt.Errorf("questions[%d].options[%d].label exceeds %d characters", index, optionIndex, MaxLabelLength)
	}
	if _, reserved := reservedQuestionnaireLabels[option.Label]; reserved {
		return fmt.Errorf("option label %q is reserved", option.Label)
	}
	if _, exists := seenLabels[option.Label]; exists {
		return fmt.Errorf("option labels must be unique within question %d", index)
	}
	seenLabels[option.Label] = struct{}{}
	value := strings.TrimSpace(option.Value)
	if _, exists := seenValues[value]; exists {
		return fmt.Errorf("option values must be unique within question %d", index)
	}
	seenValues[value] = struct{}{}
	return nil
}

// QuestionnaireElicitationSchema converts authored questions into an MCP form schema.
func QuestionnaireElicitationSchema(questionnaire Questionnaire) map[string]any {
	properties := make(map[string]any, len(questionnaire.Questions)*2)
	required := make([]string, 0, len(questionnaire.Questions))
	for index, question := range questionnaire.Questions {
		prefix := fmt.Sprintf("question_%d", index+1)
		labels := make([]string, 0, len(question.Options))
		descriptions := make([]string, 0, len(question.Options))
		for _, option := range question.Options {
			labels = append(labels, option.Label)
			descriptions = append(descriptions, option.Label+": "+option.Description)
		}
		description := question.Question + " " + strings.Join(descriptions, " ")
		if question.MultiSelect {
			key := prefix + "_selected"
			property := map[string]any{"type": "array", "title": question.Header, "description": description, "items": map[string]any{"type": "string", "enum": labels}, "uniqueItems": true}
			if defaults := recommendedLabels(question.Options); len(defaults) > 0 {
				property["default"] = defaults
			}
			properties[key] = property
			required = append(required, key)
		} else {
			key := prefix + "_choice"
			property := map[string]any{"type": "string", "title": question.Header, "description": description, "enum": append(labels, "Type something.")}
			if defaults := recommendedLabels(question.Options); len(defaults) == 1 {
				property["default"] = defaults[0]
			}
			properties[key] = property
			required = append(required, key)
		}
		properties[prefix+"_custom"] = map[string]any{"type": "string", "title": question.Header + " custom answer", "description": "Fill only when Type something. is selected or the authored choices do not fit."}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func recommendedLabels(options []Option) []string {
	labels := make([]string, 0, len(options))
	for _, option := range options {
		if option.Recommended {
			labels = append(labels, option.Label)
		}
	}
	return labels
}

// ChoiceForOption returns the authored metadata for an option.
func ChoiceForOption(option Option) QuestionnaireChoice {
	return QuestionnaireChoice{Value: option.Value, Label: option.Label, Recommended: option.Recommended}
}

// QuestionnaireAnswers validates elicited content and normalizes it to the public result shape.
func QuestionnaireAnswers(questionnaire Questionnaire, content map[string]any) (QuestionnaireResult, error) {
	answers := make([]QuestionnaireAnswer, 0, len(questionnaire.Questions))
	for index, question := range questionnaire.Questions {
		answer, err := questionnaireAnswer(index, question, content)
		if err != nil {
			return QuestionnaireResult{}, err
		}
		answers = append(answers, answer)
	}
	return QuestionnaireResult{Answers: answers, Cancelled: false}, nil
}

type questionnaireAnswerInput struct {
	index    int
	question Question
	prefix   string
	custom   string
	authored map[string]Option
	content  map[string]any
}

func questionnaireAnswer(index int, question Question, content map[string]any) (QuestionnaireAnswer, error) {
	prefix := fmt.Sprintf("question_%d", index+1)
	custom, _ := content[prefix+"_custom"].(string)
	input := questionnaireAnswerInput{index: index, question: question, prefix: prefix, custom: custom, authored: authoredOptions(question.Options), content: content}
	if question.MultiSelect {
		return multiSelectQuestionnaireAnswer(input)
	}
	return singleSelectQuestionnaireAnswer(input)
}

func authoredOptions(options []Option) map[string]Option {
	authored := make(map[string]Option, len(options))
	for _, option := range options {
		authored[option.Label] = option
	}
	return authored
}

func multiSelectQuestionnaireAnswer(input questionnaireAnswerInput) (QuestionnaireAnswer, error) {
	values, ok := input.content[input.prefix+"_selected"].([]any)
	if !ok {
		return QuestionnaireAnswer{}, fmt.Errorf("%s_selected must be an array", input.prefix)
	}
	selected := make([]string, 0, len(values))
	choices := make([]QuestionnaireChoice, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, item := range values {
		label, stringOK := item.(string)
		if !stringOK {
			return QuestionnaireAnswer{}, fmt.Errorf("%s_selected contains a non-string answer", input.prefix)
		}
		option, known := input.authored[label]
		if !known {
			return QuestionnaireAnswer{}, fmt.Errorf("%s_selected contains unknown option %q", input.prefix, label)
		}
		if _, duplicate := seen[label]; duplicate {
			return QuestionnaireAnswer{}, fmt.Errorf("%s_selected contains duplicate option %q", input.prefix, label)
		}
		seen[label] = struct{}{}
		selected = append(selected, label)
		choices = append(choices, ChoiceForOption(option))
	}
	if strings.TrimSpace(input.custom) != "" {
		selected = append(selected, input.custom)
	}
	return QuestionnaireAnswer{QuestionIndex: input.index, Question: input.question.Question, Kind: "multi", Selected: selected, Choices: choices}, nil
}

func singleSelectQuestionnaireAnswer(input questionnaireAnswerInput) (QuestionnaireAnswer, error) {
	choice, ok := input.content[input.prefix+"_choice"].(string)
	if !ok {
		return QuestionnaireAnswer{}, fmt.Errorf("%s_choice must be a string", input.prefix)
	}
	kind, answer := "option", choice
	var choices []QuestionnaireChoice
	if choice != "Type something." {
		option, known := input.authored[choice]
		if !known {
			return QuestionnaireAnswer{}, fmt.Errorf("%s_choice contains unknown option %q", input.prefix, choice)
		}
		choices = []QuestionnaireChoice{ChoiceForOption(option)}
	}
	if choice == "Type something." {
		kind, answer = "custom", strings.TrimSpace(input.custom)
		if answer == "" {
			return QuestionnaireAnswer{}, fmt.Errorf("%s_custom is required for a custom answer", input.prefix)
		}
	}
	return QuestionnaireAnswer{QuestionIndex: input.index, Question: input.question.Question, Kind: kind, Answer: &answer, Choices: choices}, nil
}
