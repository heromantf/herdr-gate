//go:build !windows

package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// startsAChild runs a command whose shell starts another program and waits
// for it, and returns the file that program keeps writing to while it runs.
// The shell cannot become the program: more is left for it to do afterwards.
//
// Whether the program still runs is read from that file, not from its
// process id: a program that was ended stays in the process table until
// something collects it, which in a container may be never.
func startsAChild(t *testing.T, client *ssh.Client) (beats string) {
	t.Helper()
	beats = filepath.Join(t.TempDir(), "beats")
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// Ends by itself after a minute, whatever the test finds.
	line := `sh -c 'echo started; n=0; while [ $n -lt 600 ]; do echo . >> "$0"; n=$((n+1)); sleep 0.1; done' '` + beats + `'; echo done`
	if err := session.Start(line); err != nil {
		t.Fatal(err)
	}
	if said, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(said) != "started" {
		t.Fatalf("the command did not start: %q %v", said, err)
	}
	for deadline := time.Now().Add(5 * time.Second); size(beats) == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the program the command started never wrote")
		}
	}
	return beats
}

func size(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// waitUntilStill returns once the file has stopped growing: nothing wrote to
// it for several times as long as the program takes between two writes.
func waitUntilStill(t *testing.T, beats string) {
	t.Helper()
	last, since := size(beats), time.Now()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if now := size(beats); now != last {
			last, since = now, time.Now()
		} else if time.Since(since) > 700*time.Millisecond {
			return
		}
	}
	t.Fatal("the program a command had started still runs")
}

// Ending the shell alone would leave what it started running, with nobody
// to read its output.
func TestWhatACommandStartedEndsWhenTheClientLeaves(t *testing.T) {
	tg := startGate(t)
	client := tg.approved(t)
	beats := startsAChild(t, client)
	client.Close()
	waitUntilStill(t, beats)
}

// The commands are in sessions of their own, so the Ctrl+C or the hangup
// that stops the gate does not reach them: the gate ends them itself.
func TestCommandsInProgressEndWhenTheGateIsStopped(t *testing.T) {
	tg := startGate(t)
	client := tg.approved(t)
	beats := startsAChild(t, client)
	tg.gate.endCommands()
	waitUntilStill(t, beats)
	if got := runCommand(t, client, "echo late", ""); got.code != 126 || got.stdout != "" {
		t.Fatalf("a command started while the gate was being stopped: %+v", got)
	}
}

// A command has no terminal, as under sshd: not the one the gate itself runs
// in either, where it could read what is typed for the gate.
func TestCommandsHaveNoTerminal(t *testing.T) {
	tg := startGate(t)
	client := tg.approved(t)
	got := runCommand(t, client, `sh -c 'if (: < /dev/tty) 2>/dev/null; then echo terminal; else echo none; fi'`, "")
	if strings.TrimSpace(got.stdout) != "none" {
		t.Fatalf("a command could open a terminal: %+v", got)
	}
}
