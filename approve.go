package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// approver asks the person at this machine whether a new device may connect.
// One question at a time; a device they refuse is not asked about again until
// the gate restarts.
type approver struct {
	mu       sync.Mutex
	answers  <-chan string // lines typed in the gate's window; closed when nobody can answer
	out      io.Writer
	timeout  time.Duration
	rejected map[string]bool
	closed   bool
	status   *statusReporter // tells herdr a question is waiting; nil outside herdr
}

func newApprover(in io.Reader, out io.Writer, status *statusReporter) *approver {
	answers := make(chan string, 16)
	go func() {
		defer close(answers)
		lines := bufio.NewScanner(in)
		for lines.Scan() {
			select {
			case answers <- lines.Text():
			default: // nobody asked: drop what was typed
			}
		}
	}()
	return &approver{answers: answers, out: out, timeout: 60 * time.Second, rejected: map[string]bool{}, status: status}
}

// The switch for letting in, without the question, new devices that are
// signed in to Tailscale with this machine's own account (see
// approvedAsOwn). It is on while this file is in the gate's folder:
// `-auto-approve on` puts it there and `off` takes it away. A running gate
// looks each time a new device arrives, so the switch works at once.
const autoApproveFile = "auto_approve_tailscale"

func autoApproveOn(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, autoApproveFile))
	return err == nil
}

func setAutoApprove(dir string, on bool) error {
	path := filepath.Join(dir, autoApproveFile)
	if !on {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("on\n"), 0o600)
}

// turnedAway reports whether the person said no to this key since the gate
// started. That no stands, whatever else would let the device in.
func (a *approver) turnedAway(key ssh.PublicKey) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rejected[ssh.FingerprintSHA256(key)]
}

// refused reports whether asking about this key is pointless: it was refused
// already, or nobody is there to answer.
func (a *approver) refused(key ssh.PublicKey) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed || a.rejected[ssh.FingerprintSHA256(key)]
}

// approve asks about a device that has proved it holds the key, and adds it
// to the list on a yes. It returns whether the device may connect now.
func (a *approver) approve(remote string, key ssh.PublicKey, devices *deviceList) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if devices.has(key) { // approved while this connection waited its turn
		return true
	}
	fingerprint := ssh.FingerprintSHA256(key)
	if a.closed || a.rejected[fingerprint] {
		return false
	}
	// Anything typed before the question was not an answer to it.
	for drained := false; !drained; {
		select {
		case _, open := <-a.answers:
			if !open {
				a.closed = true
				return false
			}
		default:
			drained = true
		}
	}
	from := remote
	if host, _, err := net.SplitHostPort(remote); err == nil {
		from = host
	}
	fmt.Fprintf(a.out, T("\n[%s] A new device wants to connect\n  From:        %s\n  Fingerprint: %s\n  Let it connect from now on? [y/N] ", "\n[%s] 新设备请求连接\n  来源: %s\n  指纹: %s\n  允许它以后连接吗？[y/N] "),
		time.Now().Format("15:04:05"), from, fingerprint)
	a.status.blocked(T("A new device wants to connect", "新设备请求连接"), fmt.Sprintf(T("From %s, fingerprint %s", "来自 %s，指纹 %s"), from, fingerprint))
	defer a.status.clear()
	select {
	case answer, open := <-a.answers:
		if !open {
			a.closed = true
			fmt.Fprintln(a.out, T("\n  Nobody is here to answer: refused.", "\n  没有人能回答，已拒绝。"))
			return false
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes", "是":
			note := fmt.Sprintf("herdr-gate %s from=%s", time.Now().Format("2006-01-02"), from)
			if err := devices.add(key, note); err != nil {
				fmt.Fprintf(a.out, T("  Cannot write the device list: %v\n", "  无法写入设备名单: %v\n"), err)
				return false
			}
			fmt.Fprintln(a.out, T("  Approved.", "  已批准。"))
			return true
		default:
			a.rejected[fingerprint] = true
			fmt.Fprintln(a.out, T("  Refused. It is not asked about again until herdr-gate restarts.", "  已拒绝，重启前不再询问它。"))
			return false
		}
	case <-time.After(a.timeout):
		fmt.Fprintln(a.out, T("\n  No answer in time: refused. It is asked about again when it reconnects.", "\n  超时未回答，已拒绝。它再连时会重新询问。"))
		return false
	}
}
