package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/dotcommander/jinn/internal/jinn"
)

func runQuestionnaireCLI(args []string, input io.Reader, output io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(input, 1<<20))
	if err != nil {
		return fmt.Errorf("read questionnaire: %w", err)
	}
	questionnaire, err := jinn.DecodeQuestionnaire(data)
	if err != nil {
		return err
	}
	if len(args) == 1 && args[0] == "--validate" {
		return json.NewEncoder(output).Encode(map[string]any{mcpDoctorStatusOK: true, "questionnaire": questionnaire})
	}
	if len(args) != 0 {
		return errors.New("usage: jinn ask [--validate]")
	}
	terminal, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return errors.New("interactive questionnaire requires a terminal; use jinn ask --validate for non-interactive validation")
	}
	result, runErr := runQuestionnaire(questionnaire, terminal, terminal)
	closeErr := terminal.Close()
	if runErr != nil {
		return runErr
	}
	if closeErr != nil {
		return fmt.Errorf("close questionnaire terminal: %w", closeErr)
	}
	return json.NewEncoder(output).Encode(result)
}

func runQuestionnaire(questionnaire jinn.Questionnaire, input io.Reader, output io.Writer) (jinn.QuestionnaireResult, error) {
	reader := bufio.NewReader(input)
	answers := make([]jinn.QuestionnaireAnswer, 0, len(questionnaire.Questions))
	for index, question := range questionnaire.Questions {
		if err := writeQuestionPrompt(output, question); err != nil {
			return jinn.QuestionnaireResult{}, err
		}
		answer, cancelled, err := readQuestionAnswer(reader, output, index, question)
		if err != nil {
			return jinn.QuestionnaireResult{}, err
		}
		if cancelled {
			return jinn.QuestionnaireResult{Answers: answers, Cancelled: true}, nil
		}
		answers = append(answers, answer)
	}
	return jinn.QuestionnaireResult{Answers: answers, Cancelled: false}, nil
}

func writeQuestionPrompt(output io.Writer, question jinn.Question) error {
	if _, err := fmt.Fprintf(output, "\n[%s] %s\n", question.Header, question.Question); err != nil {
		return err
	}
	for optionIndex, option := range question.Options {
		recommended := ""
		if option.Recommended {
			recommended = " (recommended)"
		}
		if _, err := fmt.Fprintf(output, "  %d) %s%s — %s\n", optionIndex+1, option.Label, recommended, option.Description); err != nil {
			return err
		}
	}
	prompt := "Enter one choice, 0 for custom text, or q to cancel:"
	if question.MultiSelect {
		prompt = "Enter comma-separated choices, 0 for custom text, or q to cancel:"
	}
	_, err := fmt.Fprintln(output, prompt)
	return err
}

func readQuestionAnswer(reader *bufio.Reader, output io.Writer, index int, question jinn.Question) (jinn.QuestionnaireAnswer, bool, error) {
	line, err := readQuestionLine(reader)
	if err != nil {
		return jinn.QuestionnaireAnswer{}, false, err
	}
	if strings.EqualFold(line, "q") {
		return jinn.QuestionnaireAnswer{}, true, nil
	}
	if line == "0" {
		if _, err := fmt.Fprintln(output, "Type your answer:"); err != nil {
			return jinn.QuestionnaireAnswer{}, false, err
		}
		custom, err := readQuestionLine(reader)
		if err != nil {
			return jinn.QuestionnaireAnswer{}, false, err
		}
		if custom == "" {
			return jinn.QuestionnaireAnswer{}, false, errors.New("custom answer is required")
		}
		return jinn.QuestionnaireAnswer{QuestionIndex: index, Question: question.Question, Kind: "custom", Answer: &custom}, false, nil
	}
	return parseQuestionChoices(index, question, line)
}

func readQuestionLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func parseQuestionChoices(index int, question jinn.Question, line string) (jinn.QuestionnaireAnswer, bool, error) {
	parts := []string{line}
	if question.MultiSelect {
		parts = strings.Split(line, ",")
	}
	selected := make([]string, 0, len(parts))
	choices := make([]jinn.QuestionnaireChoice, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		choice, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || choice < 1 || choice > len(question.Options) {
			return jinn.QuestionnaireAnswer{}, false, fmt.Errorf("invalid choice %q for question %d", part, index+1)
		}
		option := question.Options[choice-1]
		if _, duplicate := seen[option.Label]; duplicate {
			return jinn.QuestionnaireAnswer{}, false, fmt.Errorf("duplicate choice %q for question %d", part, index+1)
		}
		seen[option.Label] = struct{}{}
		selected = append(selected, option.Label)
		choices = append(choices, jinn.ChoiceForOption(option))
	}
	if question.MultiSelect {
		return jinn.QuestionnaireAnswer{QuestionIndex: index, Question: question.Question, Kind: "multi", Selected: selected, Choices: choices}, false, nil
	}
	answer := selected[0]
	return jinn.QuestionnaireAnswer{QuestionIndex: index, Question: question.Question, Kind: "option", Answer: &answer, Choices: choices}, false, nil
}
