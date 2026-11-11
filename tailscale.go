package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
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

// tailscale runs Tailscale's command line and returns what it prints. An
// error when it is not installed, refuses, or is slow to answer.
func tailscale(args ...string) ([]byte, error) {
	program := tailscaleProgram()
	if program == "" {
		return nil, errors.New("tailscale is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	// On macOS the same program is the app: this makes it answer as the
	// command line instead of opening a window.
	cmd.Env = append(os.Environ(), "TAILSCALE_BE_CLI=1")
	cmd.WaitDelay = time.Second
	withoutWindow(cmd)
	return cmd.Output()
}

// tailscaleAddresses asks Tailscale which IPv4 addresses it gave this
// machine. None when it is not installed, not signed in, or slow to answer.
func tailscaleAddresses() []string {
	out, err := tailscale("ip", "-4")
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

// What `tailscale whois --json <address>` says about the device at an
// address: the account it is signed in with, and its own addresses.
type tailscaleWho struct {
	Node struct {
		User         int64    `json:"User"` // the account; the same number on all of one person's devices
		Tags         []string `json:"Tags"` // a tagged device belongs to no person
		Addresses    []string `json:"Addresses"`
		ComputedName string   `json:"ComputedName"`
	} `json:"Node"`
}

// ownTailscaleDevice reports whether a connection from remote to local comes
// from a device signed in to Tailscale with this machine's own account, and
// that device's name there. Tailscale is asked about both ends: local has to
// be this machine's own Tailscale address, and remote a device it knows.
func ownTailscaleDevice(local, remote string) (name string, own bool) {
	self, err := tailscale("whois", "--json", local)
	if err != nil {
		return "", false
	}
	peer, err := tailscale("whois", "--json", remote)
	if err != nil {
		return "", false
	}
	return sameTailscaleAccount(self, local, peer, remote)
}

// sameTailscaleAccount reads Tailscale's two answers. Each address has to be
// the device's own: Tailscale also answers for addresses a device only
// routes for, and whatever sits behind such a router is not that device.
func sameTailscaleAccount(self []byte, local string, peer []byte, remote string) (name string, same bool) {
	var here, there tailscaleWho
	if json.Unmarshal(self, &here) != nil || json.Unmarshal(peer, &there) != nil {
		return "", false
	}
	if here.Node.User == 0 || here.Node.User != there.Node.User || len(here.Node.Tags) > 0 || len(there.Node.Tags) > 0 {
		return "", false
	}
	if !hasAddress(here.Node.Addresses, local) || !hasAddress(there.Node.Addresses, remote) {
		return "", false
	}
	return plainName(there.Node.ComputedName), true
}

// hasAddress reports whether one of a device's addresses, written as
// Tailscale writes them (100.64.0.7/32), is this address.
func hasAddress(addresses []string, address string) bool {
	want, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	for _, text := range addresses {
		if prefix, err := netip.ParsePrefix(text); err == nil && prefix.IsSingleIP() && prefix.Addr() == want.Unmap() {
			return true
		}
	}
	return false
}

// plainName keeps of a device's name what is safe to write into one line of
// the device list and into the log: letters, digits, dots and dashes.
func plainName(name string) string {
	var out strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			out.WriteRune(r)
		default:
			out.WriteRune('-')
		}
		if out.Len() >= 63 {
			break
		}
	}
	if out.Len() == 0 {
		return "unnamed"
	}
	return out.String()
}
