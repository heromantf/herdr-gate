package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type testGate struct {
	addr     string
	answers  chan string
	out      *safeBuffer
	devices  *deviceList
	approver *approver
	dir      string
	gate     *gate
}

// startGate runs a gate on a loopback port with its own scratch folder. What
// the person would type goes into answers.
func startGate(t *testing.T) *testGate {
	t.Helper()
	dir := t.TempDir()
	hostKey, err := loadOrCreateHostKey(filepath.Join(dir, "host_key"))
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	tg := &testGate{
		dir:     dir,
		answers: make(chan string, 4),
		out:     &safeBuffer{},
		devices: &deviceList{path: filepath.Join(dir, "authorized_keys")},
	}
	tg.approver = &approver{answers: tg.answers, out: tg.out, timeout: 2 * time.Second, rejected: map[string]bool{}}
	g := &gate{
		devices:  tg.devices,
		approver: tg.approver,
		out:      tg.out,
		user:     "Tester",
		// Fast, so a test of revoking does not wait seconds.
		revokeEvery: 50 * time.Millisecond,
		statePath:   filepath.Join(dir, connectionsFile),
		home:        home,
		dir:         dir,
		// No test asks the real Tailscale: one that needs an answer gives its own.
		ownDevice: func(string, string) (string, bool) { return "", false },
	}
	tg.gate = g
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Runs before the scratch folder is removed: the gate writes there until
	// its last connection is gone.
	t.Cleanup(func() {
		l.Close()
		done := make(chan struct{})
		go func() { g.serving.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})
	go g.serve(l, hostKey)
	tg.addr = l.Addr().String()
	return tg
}

func (tg *testGate) questions() int { return strings.Count(tg.out.String(), "[y/N]") }

// answerNext types the answer once the gate has asked its next question.
func (tg *testGate) answerNext(answer string) {
	before := tg.questions()
	go func() {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if tg.questions() > before {
				tg.answers <- answer
				return
			}
		}
	}()
}

func (tg *testGate) waitFor(t *testing.T, text string, within time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if strings.Contains(tg.out.String(), text) {
			return
		}
	}
	t.Fatalf("the gate never printed %q; it printed:\n%s", text, tg.out.String())
}

func newDeviceKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// dial logs in as the gate's account, written in another letter case.
func (tg *testGate) dial(key ssh.Signer) (*ssh.Client, error) {
	return tg.dialAs("tester", key)
}

func (tg *testGate) dialAs(user string, key ssh.Signer) (*ssh.Client, error) {
	return ssh.Dial("tcp", tg.addr, &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(key)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
}

// approved returns a client whose key is already on the list.
func (tg *testGate) approved(t *testing.T) *ssh.Client {
	t.Helper()
	key := newDeviceKey(t)
	if err := tg.devices.add(key.PublicKey(), "test"); err != nil {
		t.Fatal(err)
	}
	client, err := tg.dial(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

type result struct {
	stdout, stderr string
	code           int
}

func runCommand(t *testing.T, client *ssh.Client, command, stdin string) result {
	t.Helper()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	var stdout, stderr bytes.Buffer
	session.Stdout, session.Stderr = &stdout, &stderr
	session.Stdin = strings.NewReader(stdin)
	r := result{}
	if err := session.Run(command); err != nil {
		var exit *ssh.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("%q: %v", command, err)
		}
		r.code = exit.ExitStatus()
	}
	r.stdout = strings.ReplaceAll(stdout.String(), "\r\n", "\n")
	r.stderr = strings.ReplaceAll(stderr.String(), "\r\n", "\n")
	return r
}

func TestNewDeviceIsAskedOnceThenRemembered(t *testing.T) {
	tg := startGate(t)
	key := newDeviceKey(t)

	tg.answerNext("y")
	client, err := tg.dial(key)
	if err != nil {
		t.Fatalf("an approved device could not log in: %v\n%s", err, tg.out.String())
	}
	if got := runCommand(t, client, "echo hello", ""); strings.TrimSpace(got.stdout) != "hello" || got.code != 0 {
		t.Fatalf("echo hello gave %+v", got)
	}
	client.Close()

	again, err := tg.dial(key) // nobody answers this time
	if err != nil {
		t.Fatalf("a remembered device could not log in again: %v", err)
	}
	again.Close()
	if tg.questions() != 1 || tg.devices.count() != 1 {
		t.Fatalf("asked %d times, %d devices on the list; want 1 and 1", tg.questions(), tg.devices.count())
	}
}

// An agent in a herdr terminal answers with `hc send`, and hc wraps what an
// agent sends in a tag naming the sender. The answer is what the tag holds.
func TestAnswerWrappedByHcIsTheAnswerInside(t *testing.T) {
	tg := startGate(t)

	key := newDeviceKey(t)
	tg.answerNext(`<hc from="t7">y</hc>`)
	client, err := tg.dial(key)
	if err != nil {
		t.Fatalf("a yes sent by an agent was not taken as one: %v\n%s", err, tg.out.String())
	}
	client.Close()

	tg.answerNext(`<hc from="t7 lead" reach="hc send t7 when done">n</hc>`)
	if client, err := tg.dial(newDeviceKey(t)); err == nil {
		client.Close()
		t.Fatal("a no sent by an agent let the device in")
	}

	for line, want := range map[string]string{
		"y":                               "y",
		"  Yes ":                          "yes",
		`<hc from="t7">y</hc>`:            "y",
		`<hc from="t7"> YES </hc>`:        "yes",
		`<hc from="a>b" reach="x">是</hc>`: "是",
		`<hc>y</hc>`:                      "y",
		// Not what hc writes: taken as typed, which is not a yes.
		`<hc from="t7">y`:       `<hc from="t7">y`,
		`<b>y</b>`:              `<b>y</b>`,
		`<hcx from="t7">y</hc>`: `<hcx from="t7">y</hc>`,
	} {
		if got := answerIn(line); got != want {
			t.Errorf("answerIn(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestRefusedDeviceIsNotAskedAgain(t *testing.T) {
	tg := startGate(t)
	key := newDeviceKey(t)

	tg.answerNext("") // Enter alone means no
	if client, err := tg.dial(key); err == nil {
		client.Close()
		t.Fatal("a refused device logged in")
	}
	if client, err := tg.dial(key); err == nil {
		client.Close()
		t.Fatal("a refused device logged in on its second try")
	}
	if tg.questions() != 1 || tg.devices.count() != 0 {
		t.Fatalf("asked %d times, %d devices on the list; want 1 and 0", tg.questions(), tg.devices.count())
	}
}

func TestUnansweredQuestionRefusesButAsksAgain(t *testing.T) {
	tg := startGate(t)
	key := newDeviceKey(t)

	if client, err := tg.dial(key); err == nil { // the question times out
		client.Close()
		t.Fatal("a device nobody approved logged in")
	}
	tg.answerNext("yes")
	client, err := tg.dial(key)
	if err != nil {
		t.Fatalf("the second question was not asked or not honored: %v\n%s", err, tg.out.String())
	}
	client.Close()
}

func TestNobodyThereRefusesWithoutWaiting(t *testing.T) {
	tg := startGate(t)
	close(tg.answers)
	start := time.Now()
	if client, err := tg.dial(newDeviceKey(t)); err == nil {
		client.Close()
		t.Fatal("a device logged in with nobody there to approve it")
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("refusing took %v", took)
	}
}

func TestCommandInputOutputAndExitCode(t *testing.T) {
	tg := startGate(t)
	client := tg.approved(t)

	if got := runCommand(t, client, "sort", "pear\napple\n"); got.stdout != "apple\npear\n" || got.code != 0 {
		t.Fatalf("sort gave %+v", got)
	}
	if got := runCommand(t, client, "echo oops 1>&2", ""); strings.TrimSpace(got.stderr) != "oops" || got.stdout != "" {
		t.Fatalf("stderr was not kept apart: %+v", got)
	}
	if got := runCommand(t, client, "exit 3", ""); got.code != 3 {
		t.Fatalf("exit 3 gave code %d", got.code)
	}
	if tg.questions() != 0 {
		t.Fatal("a device already on the list was asked about")
	}
}

func TestOnlyCommandsAreServed(t *testing.T) {
	tg := startGate(t)
	client := tg.approved(t)

	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err == nil {
		t.Error("a terminal was granted")
	}
	if err := session.RequestSubsystem("sftp"); err == nil {
		t.Error("file transfer was granted")
	}
	session.Close()

	if conn, err := client.Dial("tcp", "127.0.0.1:9"); err == nil {
		conn.Close()
		t.Error("port forwarding was granted")
	}

	session, err = client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	var stderr bytes.Buffer
	session.Stderr = &stderr
	if err := session.Shell(); err != nil {
		t.Fatal(err)
	}
	if err := session.Wait(); err != nil {
		t.Fatalf("a login without a command should end cleanly: %v", err)
	}
	if !strings.Contains(stderr.String(), "herdr-gate") {
		t.Fatalf("a login without a command got no explanation: %q", stderr.String())
	}
}

func TestCommandEndsWhenTheClientLeaves(t *testing.T) {
	tg := startGate(t)
	client := tg.approved(t)
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	long := "echo started; sleep 20"
	if runtime.GOOS == "windows" {
		long = "echo started & ping -n 20 127.0.0.1 >nul"
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Start(long); err != nil {
		t.Fatal(err)
	}
	// Its first output proves the command is running, not merely asked for.
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "started" {
		t.Fatalf("the command did not start: %q %v", line, err)
	}
	client.Close()
	tg.waitFor(t, "finished: exit code", 8*time.Second)
}

// A gate started in a herdr terminal is told which terminal that is. A
// device's command is not in it: one that started a terminal with hc would
// find it made a child of the gate's, ending when the gate does.
func TestCommandsAreNotInTheGatesHerdrTerminal(t *testing.T) {
	t.Setenv("HC_TERMINAL_ID", "t99")
	t.Setenv("HERDR_GATE_TEST_KEPT", "kept")
	tg := startGate(t)
	client := tg.approved(t)

	line := `echo "[$HC_TERMINAL_ID] [$HERDR_GATE_TEST_KEPT]"`
	if runtime.GOOS == "windows" {
		line = `echo [%HC_TERMINAL_ID%] [%HERDR_GATE_TEST_KEPT%]`
	}
	got := runCommand(t, client, line, "")
	if strings.Contains(got.stdout, "t99") {
		t.Fatalf("the command was told it runs in the gate's terminal: %+v", got)
	}
	if !strings.Contains(got.stdout, "[kept]") {
		t.Fatalf("the rest of the gate's environment did not reach the command: %+v", got)
	}
}

// The exact bytes herdr-core's own encoder writes for such a report
// (herdr-core src/status.rs, `encode`), so its parser reads ours.
func TestStatusSequencesAreWhatHerdrParses(t *testing.T) {
	want := "\x1b]7501;state=blocked:kind=permission:app=herdr-gate:title=UnVuIHRlc3Rz:msg=QXBwbHk/\x1b\\"
	if got := blockedSequence("Run tests", "Apply?"); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	var none *statusReporter // outside herdr
	none.blocked("x", "y")
	none.clear()
}

func TestWaitingQuestionIsReportedThenWithdrawn(t *testing.T) {
	tg := startGate(t)
	reports := &safeBuffer{}
	tg.approver.status = &statusReporter{out: reports}

	tg.answerNext("y")
	client, err := tg.dial(newDeviceKey(t))
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	if client, err := tg.dial(newDeviceKey(t)); err == nil { // this question times out
		client.Close()
		t.Fatal("a device nobody approved logged in")
	}

	got := reports.String()
	if strings.Count(got, "state=blocked") != 2 || strings.Count(got, clearSequence) != 2 || !strings.HasSuffix(got, clearSequence) {
		t.Fatalf("each question must be reported once and withdrawn once, answered or not: %q", got)
	}
	if first := strings.Index(got, clearSequence); first < strings.Index(got, "state=blocked") {
		t.Fatalf("withdrawn before it was reported: %q", got)
	}
}

func TestOnlyTheMachinesOwnAccountMayLogIn(t *testing.T) {
	tg := startGate(t)
	key := newDeviceKey(t)
	if err := tg.devices.add(key.PublicKey(), "phone"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tester", "TESTER", "Tester"} {
		client, err := tg.dialAs(name, key)
		if err != nil {
			t.Fatalf("the account's own name, written %q, was refused: %v", name, err)
		}
		client.Close()
	}
	for _, name := range []string{"root", "tester2", ""} {
		if client, err := tg.dialAs(name, key); err == nil {
			client.Close()
			t.Fatalf("an approved device logged in as %q", name)
		}
	}
	// Under another name even a new device is not worth a question.
	if client, err := tg.dialAs("root", newDeviceKey(t)); err == nil {
		client.Close()
		t.Fatal("a new device logged in as root")
	}
	if tg.questions() != 0 {
		t.Fatal("a login under another name was asked about")
	}
	if said := strings.Count(tg.out.String(), "but this machine's account is"); said != 4 {
		t.Fatalf("each refused connection should be explained once, got %d:\n%s", said, tg.out.String())
	}
}

func TestWhoIsConnectedIsWrittenDownAsItChanges(t *testing.T) {
	tg := startGate(t)
	// connected waits until `-info` reports this many connections, and returns them.
	connected := func(want int) []connectionInfo {
		t.Helper()
		var info gateInfo
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			var out bytes.Buffer
			if err := printInfo(tg.dir, &out); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(out.Bytes(), &info); err != nil {
				t.Fatalf("%v in %s", err, out.String())
			}
			if len(info.Connections) == want {
				return info.Connections
			}
		}
		t.Fatalf("want %d connections, info says %+v", want, info.Connections)
		return nil
	}

	connected(0)
	phone, laptop := newDeviceKey(t), newDeviceKey(t)
	for _, key := range []ssh.Signer{phone, laptop} {
		if err := tg.devices.add(key.PublicKey(), "device"); err != nil {
			t.Fatal(err)
		}
	}
	first, err := tg.dial(phone)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if now := connected(1); now[0].Key != keyText(phone.PublicKey()) || now[0].From != "127.0.0.1" || now[0].Since == "" {
		t.Fatalf("%+v", now)
	}
	second, err := tg.dial(laptop) // another device at the same time
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	third, err := tg.dial(phone) // and the first device a second time
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	connected(3)
	first.Close()
	third.Close()
	if left := connected(1); left[0].Fingerprint != ssh.FingerprintSHA256(laptop.PublicKey()) {
		t.Fatalf("the wrong connection was left: %+v", left)
	}
	second.Close()
	connected(0)
}

func TestRevokedDeviceLosesItsConnection(t *testing.T) {
	tg := startGate(t)
	key := newDeviceKey(t)
	if err := tg.devices.add(key.PublicKey(), "phone"); err != nil {
		t.Fatal(err)
	}
	client, err := tg.dial(key)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	gone := make(chan error, 1)
	go func() { gone <- client.Wait() }()

	if removed, err := tg.devices.remove(ssh.FingerprintSHA256(key.PublicKey())); err != nil || removed != 1 {
		t.Fatalf("removed %d devices, %v", removed, err)
	}
	select {
	case <-gone:
	case <-time.After(3 * time.Second):
		t.Fatal("a revoked device kept the connection it had")
	}
	close(tg.answers) // nobody to ask: a revoked device is a new device again
	if again, err := tg.dial(key); err == nil {
		again.Close()
		t.Fatal("a revoked device logged in again without being approved")
	}
}

func TestRemoveTakesOneDeviceAndLeavesTheRest(t *testing.T) {
	list := &deviceList{path: filepath.Join(t.TempDir(), "authorized_keys")}
	phone, laptop, stranger := newDeviceKey(t).PublicKey(), newDeviceKey(t).PublicKey(), newDeviceKey(t).PublicKey()
	if err := os.WriteFile(list.path, []byte("# my devices\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string]ssh.PublicKey{"phone": phone, "laptop": laptop} {
		if err := list.add(key, name); err != nil {
			t.Fatal(err)
		}
	}
	if removed, err := list.remove(ssh.FingerprintSHA256(stranger)); err != nil || removed != 0 {
		t.Fatalf("removing a device that is not there: %d, %v", removed, err)
	}
	if removed, err := list.remove(ssh.FingerprintSHA256(phone)); err != nil || removed != 1 {
		t.Fatalf("by fingerprint: %d, %v", removed, err)
	}
	if list.has(phone) || !list.has(laptop) {
		t.Fatal("the wrong device went")
	}
	if removed, err := list.remove(laptop.Type() + " " + keyText(laptop)); err != nil || removed != 1 {
		t.Fatalf("by key: %d, %v", removed, err)
	}
	if text, _ := os.ReadFile(list.path); string(text) != "# my devices\n" {
		t.Fatalf("lines that name no device must stay: %q", text)
	}
}

func TestInfoDescribesTheGateWithoutChangingIt(t *testing.T) {
	dir := t.TempDir()
	read := func() gateInfo {
		t.Helper()
		var out bytes.Buffer
		if err := printInfo(dir, &out); err != nil {
			t.Fatal(err)
		}
		var info gateInfo
		if err := json.Unmarshal(out.Bytes(), &info); err != nil {
			t.Fatalf("%v in %s", err, out.String())
		}
		return info
	}

	before := read()
	if before.Version != version || before.Fingerprint != "" || len(before.Devices) != 0 {
		t.Fatalf("a gate that never ran: %+v", before)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("asking about the gate created files")
	}

	hostKey, err := loadOrCreateHostKey(filepath.Join(dir, "host_key"))
	if err != nil {
		t.Fatal(err)
	}
	phone := newDeviceKey(t).PublicKey()
	list := &deviceList{path: filepath.Join(dir, "authorized_keys")}
	if err := list.add(phone, "herdr-gate 2026-11-10 from=100.1.2.3:5 user=me"); err != nil {
		t.Fatal(err)
	}
	after := read()
	if after.Fingerprint != ssh.FingerprintSHA256(hostKey.PublicKey()) || len(after.Devices) != 1 {
		t.Fatalf("%+v", after)
	}
	if d := after.Devices[0]; d.Key != keyText(phone) || d.Fingerprint != ssh.FingerprintSHA256(phone) || !strings.Contains(d.Note, "from=100.1.2.3:5") {
		t.Fatalf("%+v", d)
	}
	// What info names is what remove takes.
	if removed, err := list.remove(after.Devices[0].Key); err != nil || removed != 1 {
		t.Fatalf("removed %d, %v", removed, err)
	}
}

func TestDeviceListIgnoresCommentsAndBrokenLines(t *testing.T) {
	list := &deviceList{path: filepath.Join(t.TempDir(), "authorized_keys")}
	known, other := newDeviceKey(t).PublicKey(), newDeviceKey(t).PublicKey()
	if list.has(known) || list.count() != 0 {
		t.Fatal("a missing list is not empty")
	}
	if err := os.WriteFile(list.path, []byte("# my devices\n\nnot a key at all\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := list.add(known, "phone\nwith a\tbroken note"); err != nil {
		t.Fatal(err)
	}
	if !list.has(known) || list.has(other) || list.count() != 1 {
		t.Fatalf("has(known)=%v has(other)=%v count=%d", list.has(known), list.has(other), list.count())
	}
	text, _ := os.ReadFile(list.path)
	if lines := strings.Split(strings.TrimSpace(string(text)), "\n"); len(lines) != 4 || !strings.HasSuffix(lines[3], "phone with a broken note") {
		t.Fatalf("the list is not one device per line:\n%s", text)
	}
}

func TestHostKeyStaysTheSame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host_key")
	first, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if ssh.FingerprintSHA256(first.PublicKey()) != ssh.FingerprintSHA256(second.PublicKey()) {
		t.Fatal("the machine's fingerprint changed between two starts")
	}
}

// The tests read what the gate prints, so they fix the language it prints in.
func TestMain(m *testing.M) {
	language = "en"
	os.Exit(m.Run())
}

func TestLanguageFollowsTheSystemAndFallsBackToEnglish(t *testing.T) {
	for locale, want := range map[string]string{
		"zh-CN": "zh", "zh_CN.UTF-8": "zh", "zh-Hans-CN": "zh", "ZH": "zh", "zh.UTF-8": "zh",
		"en-US": "en", "en_GB.UTF-8": "en", "ja_JP.UTF-8": "en", "C": "en", "": "en", "zhuang": "en",
	} {
		if got := languageOf(locale); got != want {
			t.Errorf("languageOf(%q) = %q, want %q", locale, got, want)
		}
	}
	// macOS prints its preferred languages as a list; the first one counts.
	for list, want := range map[string]string{
		"(\n    \"zh-Hans-CN\",\n    \"en-CN\"\n)\n": "zh-Hans-CN",
		"(\n    en,\n    \"zh-Hans\"\n)\n":           "en",
		"":                                           "",
	} {
		if got := firstLanguage(list); got != want {
			t.Errorf("firstLanguage(%q) = %q, want %q", list, got, want)
		}
	}
	defer func(was string) { language = was }(language)
	language = "zh"
	if T("hello", "你好") != "你好" {
		t.Error("a Chinese gate answered in English")
	}
	language = "en"
	if T("hello", "你好") != "hello" {
		t.Error("an English gate answered in Chinese")
	}
}

func TestLanguageCanBeChosenOnTheCommandLine(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-lang", "zh"}, "zh"},
		{[]string{"--lang=en"}, "en"},
		{[]string{"-listen", ":7522", "-lang", "zh"}, "zh"}, // as the mod starts it
		{[]string{"-lang", "zh", "-info"}, "zh"},
		{[]string{"-info"}, ""},
		{[]string{"-revoke", "lang"}, ""}, // another flag's value, not the flag
		{[]string{"-lang"}, ""},
		{nil, ""},
	} {
		if got := languageFlag(c.args); got != c.want {
			t.Errorf("languageFlag(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestOnlyWhatTailscaleReportsIsCalledTailscales(t *testing.T) {
	onThisMachine := []string{"192.168.1.5", "100.64.0.1", "10.0.0.1"}
	for _, c := range []struct {
		reported                 []string
		wantOrder, wantTailscale string
	}{
		// Tailscale's own comes first and is named.
		{[]string{"100.64.0.1"}, "100.64.0.1 192.168.1.5 10.0.0.1", "100.64.0.1"},
		// No answer from Tailscale: nothing is guessed from how an address looks.
		{nil, "192.168.1.5 100.64.0.1 10.0.0.1", ""},
		// An address no interface has now is not offered.
		{[]string{"100.99.9.9"}, "192.168.1.5 100.64.0.1 10.0.0.1", ""},
	} {
		ordered, tailscale := tailscaleFirst(onThisMachine, c.reported)
		if got := strings.Join(ordered, " "); got != c.wantOrder {
			t.Errorf("reported %q: order %q, want %q", c.reported, got, c.wantOrder)
		}
		if got := strings.Join(tailscale, " "); got != c.wantTailscale {
			t.Errorf("reported %q: Tailscale's %q, want %q", c.reported, got, c.wantTailscale)
		}
		// A panel reads both as lists, also when they are empty.
		if ordered == nil || tailscale == nil {
			t.Errorf("reported %q: a list came back as nothing", c.reported)
		}
	}
}
