package main

import "testing"

func TestParseMCPSnapshotCommandsRequireRegisteredAlias(t *testing.T) {
	if alias, err := parseMCPSnapshotArgs([]string{"@local", "--accept"}); err != nil || alias != "local" {
		t.Fatalf("parseMCPSnapshotArgs = %q, %v", alias, err)
	}
	if _, err := parseMCPSnapshotArgs([]string{"https://example.test", "--accept"}); err == nil {
		t.Fatal("snapshot accepted direct endpoint")
	}
	if alias, err := parseMCPSchemaDiffArgs([]string{"@local"}); err != nil || alias != "local" {
		t.Fatalf("parseMCPSchemaDiffArgs = %q, %v", alias, err)
	}
	if _, err := parseMCPSchemaDiffArgs([]string{"@local", "--accept"}); err == nil {
		t.Fatal("schema-diff accepted extra option")
	}
}

func TestCLIExitStatus(t *testing.T) {
	if got := cliExitStatus(mcpSchemaDiffReportedError{}); got != 2 {
		t.Fatalf("schema diff exit status = %d", got)
	}
	if got := cliExitStatus(mcpDoctorReportedError{}); got != 1 {
		t.Fatalf("doctor exit status = %d", got)
	}
	if got := cliExitStatus(mcpDoctorDriftReportedError{}); got != 2 {
		t.Fatalf("doctor drift exit status = %d", got)
	}
}
