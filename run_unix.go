//go:build !windows

package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// shellCommand runs the line with the person's own shell, as sshd does.
func shellCommand(ctx context.Context, line string) *exec.Cmd {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return exec.CommandContext(ctx, shell, "-c", line)
}

// withoutWindow has nothing to do here: only Windows opens a window for a
// helper program.
func withoutWindow(*exec.Cmd) {}

// takesTerminalSequences reports whether f is a terminal rather than a file
// or a pipe.
func takesTerminalSequences(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// killedCode is the exit code reported for a command ended by a signal:
// 128 plus the signal, as shells report it.
func killedCode(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return 1
}
