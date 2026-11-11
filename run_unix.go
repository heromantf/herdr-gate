//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// shellCommand runs the line with the person's own shell, as sshd does, and
// like sshd in a session of its own. That makes the shell and whatever it
// starts one process group, which is ended as one (endCommand), and leaves
// none of them the gate's own terminal to read from or write to.
func shellCommand(ctx context.Context, line string) *exec.Cmd {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.CommandContext(ctx, shell, "-c", line)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// When the client goes away. Ending the shell alone would leave what it
	// started running.
	cmd.Cancel = func() error { return endCommand(cmd) }
	return cmd
}

// endCommand ends a started command together with everything it started.
func endCommand(cmd *exec.Cmd) error {
	// A shell that has been waited for has given its number back, and the
	// group under that number may be somebody else's by now.
	if errors.Is(cmd.Process.Signal(syscall.Signal(0)), os.ErrProcessDone) {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// endCommands ends every command in progress and lets no other start: the
// gate is being stopped.
func (g *gate) endCommands() {
	g.commandsMu.Lock()
	defer g.commandsMu.Unlock()
	g.stopping = true
	for cmd := range g.commands {
		_ = endCommand(cmd)
	}
}

// endCommandsWhenStopped has the gate end the commands in progress when it is
// stopped itself: by Ctrl+C, by its terminal closing, or by being told to
// end. They are in sessions of their own, so none of that reaches them.
func (g *gate) endCommandsWhenStopped() {
	var stops []os.Signal
	for _, s := range []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		// One the gate was started to ignore (nohup) stays ignored.
		if !signal.Ignored(s) {
			stops = append(stops, s)
		}
	}
	if len(stops) == 0 {
		return
	}
	got := make(chan os.Signal, 1)
	signal.Notify(got, stops...)
	go func() {
		s := <-got
		g.endCommands()
		// Then the gate ends the way the signal would have ended it.
		signal.Reset(s)
		if number, ok := s.(syscall.Signal); ok {
			_ = syscall.Kill(os.Getpid(), number)
			time.Sleep(time.Second)
		}
		os.Exit(1)
	}()
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
