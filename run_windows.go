package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

// shellCommand runs the line with cmd, as Windows' own ssh server does by
// default. cmd reads the rest of its command line itself: with /s it drops
// the outer pair of quotes and takes everything between them as typed, so the
// line goes in raw, never quoted as an argument. /d skips AutoRun commands.
func shellCommand(ctx context.Context, line string) *exec.Cmd {
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		comspec = `C:\Windows\System32\cmd.exe`
	}
	cmd := exec.CommandContext(ctx, comspec)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `cmd.exe /d /s /c "` + line + `"`,
		// Its own hidden console: nothing it prints or reads touches the
		// gate's window.
		CreationFlags: createNoWindow,
	}
	return cmd
}

// endCommandsWhenStopped has nothing to do here: what happens to the commands
// in progress when the gate is stopped is left as Windows has it.
func (g *gate) endCommandsWhenStopped() {}

// withoutWindow keeps a helper program from opening a console window of its
// own when the gate has none to share with it (as when the mod asks -info).
func withoutWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}

// takesTerminalSequences reports whether escape sequences written to f reach
// a terminal, turning that on for a Windows console: without it the console
// would print the sequence as text.
func takesTerminalSequences(f *os.File) bool {
	handle := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) != nil {
		return false // not a console: a file or a pipe
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}

// killedCode is the exit code reported for a command that was ended rather
// than exiting. Windows reports no signals.
func killedCode(*os.ProcessState) int { return 1 }
