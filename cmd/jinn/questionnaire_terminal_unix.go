//go:build !windows

package main

func openQuestionnaireTerminal() (questionnaireTerminal, error) {
	return openQuestionnaireTerminalFiles("/dev/tty", "/dev/tty")
}
