package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// loadOrCreateHostKey reads this machine's identity, making one on first run.
// Clients remember its fingerprint and refuse to connect if it changes.
func loadOrCreateHostKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		block, err := ssh.MarshalPrivateKey(private, "herdr-gate host key")
		if err != nil {
			return nil, err
		}
		data = pem.EncodeToMemory(block)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}

// deviceList is the approved devices: one public key per line, in the usual
// authorized_keys format. It is read again on every check, so a device taken
// off the list, by `-revoke` or by hand, is refused from then on.
type deviceList struct {
	path string
	mu   sync.Mutex
}

// device is one line of the list.
type device struct {
	key  ssh.PublicKey
	note string // when and from where it was approved
}

// lines returns the file's lines, each with the device it names, if any:
// comments, blank lines and lines that are not a key have none.
func (d *deviceList) lines() (lines []string, devices []*device) {
	f, err := os.Open(d.path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		line := scan.Text()
		var entry *device
		if text := strings.TrimSpace(line); text != "" && !strings.HasPrefix(text, "#") {
			if key, note, _, _, err := ssh.ParseAuthorizedKey([]byte(text)); err == nil {
				entry = &device{key: key, note: note}
			}
		}
		lines, devices = append(lines, line), append(devices, entry)
	}
	return lines, devices
}

func (d *deviceList) devices() []*device {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*device
	_, all := d.lines()
	for _, entry := range all {
		if entry != nil {
			out = append(out, entry)
		}
	}
	return out
}

func (d *deviceList) has(key ssh.PublicKey) bool {
	want := key.Marshal()
	for _, entry := range d.devices() {
		if bytes.Equal(entry.key.Marshal(), want) {
			return true
		}
	}
	return false
}

func (d *deviceList) count() int { return len(d.devices()) }

// add appends a device with a note saying when and from where it was approved.
func (d *deviceList) add(key ssh.PublicKey, note string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	f, err := os.OpenFile(d.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	note = strings.Join(strings.Fields(note), " ")
	if _, err := fmt.Fprintf(f, "%s %s\n", line, note); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// keyText is a key as the list and `-info` write it: base64 of its wire form.
func keyText(key ssh.PublicKey) string {
	return base64.StdEncoding.EncodeToString(key.Marshal())
}

// remove takes the device named by want off the list and reports how many
// lines went. want is its fingerprint (SHA256:...) or its key in base64, with
// or without the key type in front. Every other line stays as it was.
func (d *deviceList) remove(want string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fields := strings.Fields(want)
	if len(fields) == 0 {
		return 0, errors.New("no device named")
	}
	want = fields[len(fields)-1] // "ssh-ed25519 AAAA..." names the same key as "AAAA..."
	lines, devices := d.lines()
	var kept []string
	removed := 0
	for i, line := range lines {
		if entry := devices[i]; entry != nil && (ssh.FingerprintSHA256(entry.key) == want || keyText(entry.key) == want) {
			removed++
			continue
		}
		kept = append(kept, line)
	}
	if removed == 0 {
		return 0, nil
	}
	// Replaced whole: a gate reading the list sees the old one or the new one.
	text := strings.Join(kept, "\n")
	if len(kept) > 0 {
		text += "\n"
	}
	tmp := filepath.Join(filepath.Dir(d.path), ".authorized_keys.new")
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, d.path); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	return removed, nil
}
