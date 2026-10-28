//go:build windows

package main

func openQuestionnaireTerminal() (questionnaireTerminal, error) {
	return openQuestionnaireTerminalFiles("CONIN$", "CONOUT$")
}
