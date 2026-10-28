package main

import (
	"errors"
	"os"
)

type questionnaireTerminal struct {
	input  *os.File
	output *os.File
}

func (terminal questionnaireTerminal) Close() error {
	if terminal.input == terminal.output {
		return terminal.input.Close()
	}
	return errors.Join(terminal.input.Close(), terminal.output.Close())
}

func openQuestionnaireTerminalFiles(inputName, outputName string) (questionnaireTerminal, error) {
	return openQuestionnaireConsoleFiles(os.OpenFile, inputName, outputName)
}

func openQuestionnaireConsoleFiles(open func(string, int, os.FileMode) (*os.File, error), inputName, outputName string) (questionnaireTerminal, error) {
	flags := os.O_RDONLY
	if inputName == outputName {
		flags = os.O_RDWR
	}
	input, err := open(inputName, flags, 0)
	if err != nil {
		return questionnaireTerminal{}, err
	}
	if inputName == outputName {
		return questionnaireTerminal{input: input, output: input}, nil
	}
	output, err := open(outputName, os.O_WRONLY, 0)
	if err != nil {
		return questionnaireTerminal{}, errors.Join(err, input.Close())
	}
	return questionnaireTerminal{input: input, output: output}, nil
}
