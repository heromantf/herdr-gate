package main

import (
	"strings"
	"testing"
)

// The lines below have the shape herdr-core sends when it dials a machine
// (herdr-core src/machines.rs, `pipe`). They must reach cmd as typed: core
// tells the outcomes apart by the exit code and by this marker on stderr.
const notHere = "herdr-core is not installed here"

func TestCoreWindowsLineReachesCmdAsTyped(t *testing.T) {
	tg := startGate(t)
	client := tg.approved(t)

	missing := `cmd /d /c "if exist .\NoSuchFolder\herdr-core.exe (.\NoSuchFolder\herdr-core.exe connect --session s1) else (echo ` + notHere + ` 1>&2 & exit /b 127)"`
	if got := runCommand(t, client, missing, ""); got.code != 127 || !strings.Contains(got.stderr, notHere) {
		t.Fatalf("a machine without hc must answer 127 and the marker, got %+v", got)
	}

	found := `cmd /d /c "if exist C:\Windows\System32\cmd.exe (C:\Windows\System32\cmd.exe /c echo connect --session s1) else (echo ` + notHere + ` 1>&2 & exit /b 127)"`
	if got := runCommand(t, client, found, ""); got.code != 0 || strings.TrimSpace(got.stdout) != "connect --session s1" {
		t.Fatalf("the program that exists was not run with its arguments, got %+v", got)
	}
}

// Core tries its Unix line first. On Windows that must fail in a way core
// reads as "perhaps Windows": not 127 or the marker (hc missing), and not 255
// (ssh itself failed).
func TestCoreUnixLineFailsLikeWindows(t *testing.T) {
	tg := startGate(t)
	client := tg.approved(t)

	unix := `sh -c 'p=$(command -v herdr-core) || p=$HOME/.local/bin/herdr-core; [ -x "$p" ] || { echo "` + notHere + `" >&2; exit 127; }; HC_SESSION=gate-test-never-started exec "$p" connect'`
	got := runCommand(t, client, unix, "")
	if got.code == 0 || got.code == 127 || got.code == 255 || strings.Contains(got.stderr, notHere) {
		t.Fatalf("core would misread this as something other than a Windows machine: %+v", got)
	}
}
