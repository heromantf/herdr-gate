package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// Which of this machine's addresses are Tailscale's is asked of Tailscale
// itself, through its command line. Nothing is guessed from how an address
// looks: without an answer, no address is called Tailscale's.

// tailscaleProgram finds Tailscale's command line: on the PATH, or where its
// installers put it. A program started from the macOS desktop gets a short
// PATH with none of those places in it, and the macOS apps keep the command
// line inside the app. "" when Tailscale is not installed.
func tailscaleProgram() string {
	if path, err := exec.LookPath("tailscale"); err == nil {
		return path
	}
	var places []string
	switch runtime.GOOS {
	case "darwin":
		places = []string{"/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale", "/Applications/Tailscale.app/Contents/MacOS/Tailscale"}
	case "windows":
		if programs := os.Getenv("ProgramFiles"); programs != "" {
			places = []string{filepath.Join(programs, "Tailscale", "tailscale.exe")}
		}
	default:
		places = []string{"/usr/bin/tailscale", "/usr/sbin/tailscale", "/usr/local/bin/tailscale"}
	}
	for _, place := range places {
		if info, err := os.Stat(place); err == nil && !info.IsDir() {
			return place
		}
	}
	return ""
}

// tailscaleAddresses asks Tailscale which IPv4 addresses it gave this
// machine. None when it is not installed, not signed in, or slow to answer.
func tailscaleAddresses() []string {
	program := tailscaleProgram()
	if program == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "ip", "-4")
	// On macOS the same program is the app: this makes it answer as the
	// command line instead of opening a window.
	cmd.Env = append(os.Environ(), "TAILSCALE_BE_CLI=1")
	cmd.WaitDelay = time.Second
	withoutWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var found []string
	for _, word := range strings.Fields(string(out)) {
		if ip := net.ParseIP(word).To4(); ip != nil {
			found = append(found, ip.String())
		}
	}
	return found
}

// tailscaleFirst puts the addresses Tailscale reported ahead of the others,
// and names them. One it reported that no interface has now is left out:
// nothing would answer on it.
func tailscaleFirst(addresses, reported []string) (ordered, tailscale []string) {
	ordered, tailscale = []string{}, []string{}
	for _, address := range addresses {
		if slices.Contains(reported, address) {
			tailscale = append(tailscale, address)
		}
	}
	ordered = append(ordered, tailscale...)
	for _, address := range addresses {
		if !slices.Contains(tailscale, address) {
			ordered = append(ordered, address)
		}
	}
	return ordered, tailscale
}
