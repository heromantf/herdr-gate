package main

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// What `-info` prints: what a control panel needs to show how to reach this
// machine and which devices may. The herdr-app mod reads it.
type gateInfo struct {
	Version string `json:"version"`
	// The language the gate speaks here, "en" or "zh", for a panel to speak too.
	Lang        string   `json:"lang"`
	User        string   `json:"user"`        // the account commands run as
	Addresses   []string `json:"addresses"`   // this machine's IPv4 addresses, Tailscale's first
	Tailscale   []string `json:"tailscale"`   // those of them Tailscale says are its own
	Fingerprint string   `json:"fingerprint"` // of this machine; empty before the first start
	Dir         string   `json:"dir"`
	// Whether new devices signed in to Tailscale with this machine's own
	// account are let in without the question.
	AutoApprove bool         `json:"autoApprove"`
	Devices     []deviceInfo `json:"devices"`
	// Who was connected when the gate last wrote it down. True while a gate
	// runs; one that was cut short leaves its last list behind, so a reader
	// that knows the gate is not running should ignore it.
	Connections []connectionInfo `json:"connections"`
}

// The file a running gate keeps its logged-in connections in (see publish).
const connectionsFile = "connections.json"

type deviceInfo struct {
	Key         string `json:"key"` // base64; `-revoke` takes it, or the fingerprint
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	Note        string `json:"note"`
}

func printInfo(dir string, out io.Writer) error {
	addresses, tailscale := localAddresses()
	info := gateInfo{Version: version, Lang: language, User: currentUser(), Addresses: addresses, Tailscale: tailscale, Dir: dir, AutoApprove: autoApproveOn(dir), Devices: []deviceInfo{}, Connections: []connectionInfo{}}
	if data, err := os.ReadFile(filepath.Join(dir, connectionsFile)); err == nil {
		var state struct {
			Connections []connectionInfo `json:"connections"`
		}
		if json.Unmarshal(data, &state) == nil && state.Connections != nil {
			info.Connections = state.Connections
		}
	}
	// Read, never made: asking about a gate that has not run yet changes nothing.
	if data, err := os.ReadFile(filepath.Join(dir, "host_key")); err == nil {
		if hostKey, err := ssh.ParsePrivateKey(data); err == nil {
			info.Fingerprint = ssh.FingerprintSHA256(hostKey.PublicKey())
		}
	}
	list := &deviceList{path: filepath.Join(dir, "authorized_keys")}
	for _, d := range list.devices() {
		info.Devices = append(info.Devices, deviceInfo{Key: keyText(d.key), Type: d.key.Type(), Fingerprint: ssh.FingerprintSHA256(d.key), Note: d.note})
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(info)
}

// currentUser is the account's short name: Windows reports it as DOMAIN\name.
func currentUser() string {
	name := os.Getenv("USER")
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// localAddresses lists this machine's IPv4 addresses, so the person knows
// what to type on the other device, and says which of them are Tailscale's
// (see tailscale.go). Those come first: they work from anywhere.
func localAddresses() (all, tailscale []string) {
	var found []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil {
			found = append(found, ip4.String())
		}
	}
	return tailscaleFirst(found, tailscaleAddresses())
}
