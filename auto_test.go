package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// tailscaleSays makes the gate's question to Tailscale answer this, and
// counts how often it was asked and about which addresses.
type tailscaleSays struct {
	mu            sync.Mutex
	name          string
	own           bool
	asked         int
	local, remote string
}

func (s *tailscaleSays) answer(local, remote string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked++
	s.local, s.remote = local, remote
	return s.name, s.own
}

func (s *tailscaleSays) times() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked
}

func switchOn(t *testing.T, tg *testGate) {
	t.Helper()
	if err := setAutoApprove(tg.dir, true); err != nil {
		t.Fatal(err)
	}
}

func TestOwnTailscaleDeviceIsLetInWithoutTheQuestion(t *testing.T) {
	tg := startGate(t)
	says := &tailscaleSays{name: "my-phone", own: true}
	tg.gate.ownDevice = says.answer
	switchOn(t, tg)
	key := newDeviceKey(t)

	client, err := tg.dial(key)
	if err != nil {
		t.Fatalf("one of the person's own devices was not let in: %v\n%s", err, tg.out.String())
	}
	client.Close()
	if tg.questions() != 0 {
		t.Fatalf("the question was asked anyway:\n%s", tg.out.String())
	}
	if says.local != "127.0.0.1" || says.remote != "127.0.0.1" {
		t.Fatalf("Tailscale was asked about %q and %q, not about the two ends of the connection", says.local, says.remote)
	}
	tg.waitFor(t, "approved without asking: my-phone", 2*time.Second)
	devices := tg.devices.devices()
	if len(devices) != 1 || !strings.Contains(devices[0].note, "from=127.0.0.1") || !strings.Contains(devices[0].note, "tailscale=my-phone") {
		t.Fatalf("the device list after it: %+v", devices)
	}

	// From then on it is a device on the list like any other.
	again, err := tg.dial(key)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	if says.times() != 1 || tg.devices.count() != 1 {
		t.Fatalf("Tailscale asked %d times, %d devices listed; want 1 and 1", says.times(), tg.devices.count())
	}
}

func TestSwitchedOffEveryNewDeviceIsAskedAbout(t *testing.T) {
	tg := startGate(t)
	says := &tailscaleSays{name: "my-phone", own: true}
	tg.gate.ownDevice = says.answer
	key := newDeviceKey(t)

	tg.answerNext("y")
	client, err := tg.dial(key)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	if tg.questions() != 1 || says.times() != 0 {
		t.Fatalf("%d questions, Tailscale asked %d times; want 1 and 0", tg.questions(), says.times())
	}

	// On and off again is off.
	switchOn(t, tg)
	if err := setAutoApprove(tg.dir, false); err != nil {
		t.Fatal(err)
	}
	tg.answerNext("n")
	if _, err := tg.dial(newDeviceKey(t)); err == nil {
		t.Fatal("a refused device got in")
	}
	if tg.questions() != 2 || says.times() != 0 {
		t.Fatalf("%d questions, Tailscale asked %d times; want 2 and 0", tg.questions(), says.times())
	}
}

func TestDeviceTailscaleDoesNotVouchForIsStillAskedAbout(t *testing.T) {
	tg := startGate(t)
	says := &tailscaleSays{own: false}
	tg.gate.ownDevice = says.answer
	switchOn(t, tg)

	tg.answerNext("n")
	if _, err := tg.dial(newDeviceKey(t)); err == nil {
		t.Fatal("a refused device got in")
	}
	if tg.questions() != 1 || says.times() != 1 || tg.devices.count() != 0 {
		t.Fatalf("%d questions, Tailscale asked %d times, %d devices; want 1, 1 and 0", tg.questions(), says.times(), tg.devices.count())
	}
}

func TestNoFromThePersonStandsAgainstTheSwitch(t *testing.T) {
	tg := startGate(t)
	says := &tailscaleSays{name: "my-phone", own: true}
	tg.gate.ownDevice = says.answer
	key := newDeviceKey(t)

	tg.answerNext("n")
	if _, err := tg.dial(key); err == nil {
		t.Fatal("a refused device got in")
	}
	switchOn(t, tg)
	if _, err := tg.dial(key); err == nil {
		t.Fatal("the switch let in a device the person had refused")
	}
	if tg.questions() != 1 || tg.devices.count() != 0 {
		t.Fatalf("%d questions, %d devices; want 1 and 0", tg.questions(), tg.devices.count())
	}
}

func TestOwnDeviceIsLetInEvenWithNobodyToAsk(t *testing.T) {
	tg := startGate(t)
	says := &tailscaleSays{own: false}
	tg.gate.ownDevice = says.answer
	switchOn(t, tg)
	close(tg.answers)

	// A stranger first, so the gate has learned that nobody answers.
	if _, err := tg.dial(newDeviceKey(t)); err == nil {
		t.Fatal("a device nobody approved got in")
	}
	says.mu.Lock()
	says.name, says.own = "my-laptop", true
	says.mu.Unlock()
	client, err := tg.dial(newDeviceKey(t))
	if err != nil {
		t.Fatalf("one of the person's own devices was not let in: %v\n%s", err, tg.out.String())
	}
	client.Close()
}

func TestNameFromTailscaleCannotAddLinesToTheDeviceList(t *testing.T) {
	tg := startGate(t)
	stranger := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(newDeviceKey(t).PublicKey())))
	says := &tailscaleSays{name: "phone\n" + stranger + " planted", own: true}
	tg.gate.ownDevice = says.answer
	switchOn(t, tg)

	client, err := tg.dial(newDeviceKey(t))
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	data, err := os.ReadFile(tg.devices.path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "\n"); lines != 1 || tg.devices.count() != 1 {
		t.Fatalf("%d lines, %d devices; want 1 and 1:\n%s", lines, tg.devices.count(), data)
	}
}

func TestOnlyTheSameTailscaleAccountCountsAsOwn(t *testing.T) {
	const self = `{"Node":{"User":7,"Addresses":["100.64.0.1/32","fd7a:115c:a1e0::1/128"],"ComputedName":"my-desktop"},"UserProfile":{"ID":7}}`
	who := func(user string, addresses, extra string) string {
		return `{"Node":{"User":` + user + `,"Addresses":[` + addresses + `],"ComputedName":"my-phone"` + extra + `}}`
	}
	const phone = `"100.64.0.2/32","fd7a:115c:a1e0::2/128"`
	for _, c := range []struct {
		why                       string
		self, local, peer, remote string
		want                      bool
	}{
		{"the same account at both ends", self, "100.64.0.1", who("7", phone, ""), "100.64.0.2", true},
		{"an IPv4 address reported inside an IPv6 one", self, "100.64.0.1", who("7", phone, ""), "::ffff:100.64.0.2", true},
		{"over IPv6", self, "fd7a:115c:a1e0::1", who("7", phone, ""), "fd7a:115c:a1e0::2", true},
		{"somebody else's device on the same tailnet", self, "100.64.0.1", who("8", phone, ""), "100.64.0.2", false},
		{"a tagged device, which is nobody's", self, "100.64.0.1", who("7", phone, `,"Tags":["tag:server"]`), "100.64.0.2", false},
		{"something behind a device that routes for it", self, "100.64.0.1", who("7", phone, ""), "192.168.1.50", false},
		{"a connection that came in on another of this machine's addresses", self, "192.168.1.5", who("7", phone, ""), "100.64.0.2", false},
		{"no account", strings.ReplaceAll(self, `"User":7`, `"User":0`), "100.64.0.1", who("0", phone, ""), "100.64.0.2", false},
		{"an answer that is not Tailscale's", self, "100.64.0.1", "peer not found", "100.64.0.2", false},
	} {
		name, got := sameTailscaleAccount([]byte(c.self), c.local, []byte(c.peer), c.remote)
		if got != c.want {
			t.Errorf("%s: own = %v, want %v", c.why, got, c.want)
		}
		if got && name != "my-phone" {
			t.Errorf("%s: name %q, want my-phone", c.why, name)
		}
	}
}

func TestSwitchIsKeptInTheGatesFolderAndInfoTellsIt(t *testing.T) {
	dir := t.TempDir()
	read := func() bool {
		t.Helper()
		var out bytes.Buffer
		if err := printInfo(dir, &out); err != nil {
			t.Fatal(err)
		}
		var info gateInfo
		if err := json.Unmarshal(out.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		return info.AutoApprove
	}
	if read() {
		t.Fatal("on before anyone switched it on")
	}
	for _, on := range []bool{true, true, false, false} { // each twice: no harm in repeating
		if err := setAutoApprove(dir, on); err != nil {
			t.Fatal(err)
		}
		if read() != on {
			t.Fatalf("switched to %v, info says %v", on, read())
		}
	}
}
