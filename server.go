package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// How long a connection may take to log in. Longer than the approval
// question, so a device waiting for its yes is not cut off first.
const loginTimeout = 90 * time.Second

// How often logged-in devices are checked against the list, so a device
// taken off it loses the connection it already has.
const defaultRevokeEvery = 3 * time.Second

type gate struct {
	devices  *deviceList
	approver *approver
	out      io.Writer
	outMu    sync.Mutex
	home     string // where commands start, as an ssh login would
	// The account commands run as, and so the only user name a device may
	// log in with (in any letter case). Empty: unknown, any name is taken.
	user string

	revokeEvery time.Duration // 0: the default
	liveMu      sync.Mutex
	live        map[*ssh.ServerConn]connectionInfo // the logged-in connections
	wrongUser   map[string]bool                    // connections already told about, by remote address

	// Where the logged-in connections are written each time they change, for
	// `-info` and so for a control panel. Empty: nowhere.
	statePath string
	publishMu sync.Mutex

	// The gate's own folder, where the switch for approving the person's own
	// Tailscale devices without the question lies (see autoApproveOn).
	dir string
	// Asks Tailscale whether a connection comes from one of the person's own
	// devices. Nil: ownTailscaleDevice.
	ownDevice func(local, remote string) (name string, own bool)
	ownMu     sync.Mutex

	// The commands in progress, for a gate that is stopped to end them rather
	// than leave them behind (see endCommandsWhenStopped).
	commandsMu sync.Mutex
	commands   map[*exec.Cmd]bool
	stopping   bool // no command starts any more

	serving sync.WaitGroup // the connections being served, for whoever must wait them out
}

// start starts a command and keeps it among those in progress until forget.
func (g *gate) start(cmd *exec.Cmd) error {
	g.commandsMu.Lock()
	defer g.commandsMu.Unlock()
	if g.stopping {
		return errors.New("herdr-gate is stopping")
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if g.commands == nil {
		g.commands = map[*exec.Cmd]bool{}
	}
	g.commands[cmd] = true
	return nil
}

func (g *gate) forget(cmd *exec.Cmd) {
	g.commandsMu.Lock()
	defer g.commandsMu.Unlock()
	delete(g.commands, cmd)
}

// connectionInfo is one device connected right now.
type connectionInfo struct {
	Key         string `json:"key"` // as in the device list (keyText)
	Fingerprint string `json:"fingerprint"`
	From        string `json:"from"`  // the address it connects from, without the port
	Since       string `json:"since"` // RFC 3339
}

// publish writes who is connected now. The file is replaced whole, so a
// reader sees the list before the change or after it.
func (g *gate) publish() {
	if g.statePath == "" {
		return
	}
	g.publishMu.Lock()
	defer g.publishMu.Unlock()
	g.liveMu.Lock()
	list := make([]connectionInfo, 0, len(g.live))
	for _, c := range g.live {
		list = append(list, c)
	}
	g.liveMu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Since < list[j].Since })
	data, err := json.MarshalIndent(struct {
		Connections []connectionInfo `json:"connections"`
	}{list}, "", "  ")
	if err != nil {
		return
	}
	tmp := g.statePath + ".new"
	if os.WriteFile(tmp, data, 0o600) != nil || os.Rename(tmp, g.statePath) != nil {
		os.Remove(tmp)
	}
}

// refuseUser says once per connection why a login under another name fails:
// to the device it looks like any refused key.
func (g *gate) refuseUser(conn ssh.ConnMetadata) {
	remote := conn.RemoteAddr().String()
	g.liveMu.Lock()
	told := g.wrongUser[remote]
	if g.wrongUser == nil {
		g.wrongUser = map[string]bool{}
	}
	g.wrongUser[remote] = true
	g.liveMu.Unlock()
	if !told {
		g.logf(T("%s logged in as %s, but this machine's account is %s: refused", "%s 用的用户名是 %s，不是本机账号 %s，已拒绝"), remote, conn.User(), g.user)
	}
}

func (g *gate) logf(format string, args ...any) {
	g.outMu.Lock()
	defer g.outMu.Unlock()
	fmt.Fprintf(g.out, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

func (g *gate) config(hostKey ssh.Signer) *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{
		ServerVersion: "SSH-2.0-herdr-gate_" + version,
		// A key the list does not know may still try: the question is asked
		// only once the device has proved it holds the key (below).
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if g.user != "" && !strings.EqualFold(conn.User(), g.user) {
				g.refuseUser(conn)
				return nil, errors.New("not this machine's account")
			}
			// With the switch on, a key nobody can be asked about may still
			// turn out to be one of the person's own devices (below).
			if !g.devices.has(key) && g.approver.refused(key) && !autoApproveOn(g.dir) {
				return nil, errors.New("device not approved")
			}
			return &ssh.Permissions{Extensions: map[string]string{"fingerprint": ssh.FingerprintSHA256(key), "key": keyText(key)}}, nil
		},
		VerifiedPublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey, perms *ssh.Permissions, _ string) (*ssh.Permissions, error) {
			if g.devices.has(key) || g.approvedAsOwn(conn, key) || g.approver.approve(conn.RemoteAddr().String(), key, g.devices) {
				return perms, nil
			}
			return nil, errors.New("device not approved")
		},
	}
	cfg.AddHostKey(hostKey)
	return cfg
}

// approvedAsOwn lets a new device in without the question, and adds it to
// the list, when the person switched that on and Tailscale says the device is
// signed in with this machine's own account. The device has already proved it
// holds the key. Anything else is left to the question.
func (g *gate) approvedAsOwn(conn ssh.ConnMetadata, key ssh.PublicKey) bool {
	if !autoApproveOn(g.dir) || g.approver.turnedAway(key) {
		return false
	}
	local, remote := hostOf(conn.LocalAddr()), hostOf(conn.RemoteAddr())
	own := g.ownDevice
	if own == nil {
		own = ownTailscaleDevice
	}
	name, ok := own(local, remote)
	if !ok {
		return false
	}
	// One at a time: a device that opens two connections is listed once.
	g.ownMu.Lock()
	defer g.ownMu.Unlock()
	if g.devices.has(key) {
		return true
	}
	note := fmt.Sprintf("herdr-gate %s from=%s tailscale=%s", time.Now().Format("2006-01-02"), remote, plainName(name))
	if err := g.devices.add(key, note); err != nil {
		g.logf(T("cannot write the device list: %v", "无法写入设备名单: %v"), err)
		return false
	}
	g.logf(T("%s approved without asking: %s is one of your Tailscale devices (fingerprint %s)", "%s 已自动批准：%s 是你的 Tailscale 设备（指纹 %s）"), remote, plainName(name), ssh.FingerprintSHA256(key))
	return true
}

// hostOf is an address without its port, and an IPv4 address as one even
// when the system reports it inside an IPv6 one.
func hostOf(addr net.Addr) string {
	host := addr.String()
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Unmap().String()
	}
	return host
}

func (g *gate) serve(l net.Listener, hostKey ssh.Signer) error {
	cfg := g.config(hostKey)
	stopped := make(chan struct{})
	defer close(stopped)
	go g.dropRevoked(stopped)
	g.publish() // nobody yet: whatever a gate that was cut short left behind is wrong now
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		g.serving.Add(1)
		go func() {
			defer g.serving.Done()
			g.connection(c, cfg)
		}()
	}
}

func (g *gate) connection(c net.Conn, cfg *ssh.ServerConfig) {
	defer c.Close()
	remote := c.RemoteAddr().String()
	defer func() {
		g.liveMu.Lock()
		delete(g.wrongUser, remote)
		g.liveMu.Unlock()
	}()
	_ = c.SetDeadline(time.Now().Add(loginTimeout))
	conn, channels, requests, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		g.logf(T("%s could not log in: %v", "%s 未能登录: %v"), remote, err)
		return
	}
	_ = c.SetDeadline(time.Time{})
	g.logf(T("%s logged in (fingerprint %s)", "%s 已登录（指纹 %s）"), remote, conn.Permissions.Extensions["fingerprint"])
	defer g.logf(T("%s disconnected", "%s 已断开"), remote)
	from := remote
	if host, _, err := net.SplitHostPort(remote); err == nil {
		from = host
	}
	g.liveMu.Lock()
	if g.live == nil {
		g.live = map[*ssh.ServerConn]connectionInfo{}
	}
	g.live[conn] = connectionInfo{
		Key:         conn.Permissions.Extensions["key"],
		Fingerprint: conn.Permissions.Extensions["fingerprint"],
		From:        from,
		Since:       time.Now().Format(time.RFC3339),
	}
	g.liveMu.Unlock()
	g.publish()
	defer func() {
		g.liveMu.Lock()
		delete(g.live, conn)
		g.liveMu.Unlock()
		g.publish()
	}()

	// Keepalives and forwarding requests: answered "no", which is all a
	// keepalive needs.
	go ssh.DiscardRequests(requests)
	for incoming := range channels {
		if incoming.ChannelType() != "session" {
			_ = incoming.Reject(ssh.Prohibited, "herdr-gate only runs commands")
			continue
		}
		channel, requests, err := incoming.Accept()
		if err != nil {
			continue
		}
		go g.session(channel, requests, remote)
	}
}

// dropRevoked closes the connections of devices that are no longer on the
// list: revoking a device ends what it is doing, not only its next login.
func (g *gate) dropRevoked(stopped <-chan struct{}) {
	every := g.revokeEvery
	if every <= 0 {
		every = defaultRevokeEvery
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-stopped:
			return
		case <-tick.C:
		}
		g.liveMu.Lock()
		live := make(map[*ssh.ServerConn]string, len(g.live))
		for conn, c := range g.live {
			live[conn] = c.Key
		}
		g.liveMu.Unlock()
		if len(live) == 0 {
			continue
		}
		approved := map[string]bool{}
		for _, d := range g.devices.devices() {
			approved[keyText(d.key)] = true
		}
		for conn, key := range live {
			if !approved[key] {
				g.logf(T("%s: its device was revoked, connection closed", "%s 的设备已被撤销，断开它的连接"), conn.RemoteAddr())
				_ = conn.Close()
			}
		}
	}
}

// session serves one command. Everything else a client may ask for on a
// session (a terminal, environment variables, a subsystem such as sftp) is
// refused.
func (g *gate) session(channel ssh.Channel, requests <-chan *ssh.Request, remote string) {
	// Ends the command when the client goes away.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer channel.Close()
	started := false
	for request := range requests {
		switch {
		case request.Type == "exec" && !started:
			var payload struct{ Command string }
			if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
				_ = request.Reply(false, nil)
				continue
			}
			started = true
			_ = request.Reply(true, nil)
			go g.run(ctx, channel, payload.Command, remote)
		case request.Type == "shell" && !started:
			started = true
			_ = request.Reply(true, nil)
			_, _ = io.WriteString(channel.Stderr(), T("herdr-gate: connected, and this device is approved. Only commands run here: there is no interactive terminal.\r\n", "herdr-gate: 已连接，这台设备已获批准。这里只执行命令，不提供交互终端。\r\n"))
			finish(channel, 0)
		default:
			if request.WantReply {
				_ = request.Reply(false, nil)
			}
		}
	}
}

// run starts the command as this system's shell would and joins its input
// and output to the channel, untouched.
func (g *gate) run(ctx context.Context, channel ssh.Channel, command, remote string) {
	g.logf(T("%s runs: %s", "%s 执行: %s"), remote, short(command))
	cmd := shellCommand(ctx, command)
	cmd.Dir = g.home
	cmd.Stdout = channel
	cmd.Stderr = channel.Stderr()
	// Bounds the wait for output once the command is gone, in case something
	// it started still holds the pipes.
	cmd.WaitDelay = 3 * time.Second
	stdin, err := cmd.StdinPipe()
	if err == nil {
		err = g.start(cmd)
	}
	if err != nil {
		if ctx.Err() != nil {
			g.logf(T("%s left before its command started", "%s 已离开，命令没有启动"), remote)
			return
		}
		fmt.Fprintf(channel.Stderr(), T("herdr-gate: cannot start the command: %v\n", "herdr-gate: 无法启动命令: %v\n"), err)
		g.logf(T("%s: cannot start the command: %v", "%s 无法启动命令: %v"), remote, err)
		finish(channel, 126)
		return
	}
	defer g.forget(cmd)
	go func() {
		_, _ = io.Copy(stdin, channel)
		_ = stdin.Close()
	}()
	_ = cmd.Wait()
	code := cmd.ProcessState.ExitCode()
	if code < 0 {
		code = killedCode(cmd.ProcessState)
	}
	g.logf(T("%s finished: exit code %d", "%s 结束: 退出码 %d"), remote, code)
	finish(channel, code)
}

// finish reports the exit code and closes the channel.
func finish(channel ssh.Channel, code int) {
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
	_ = channel.CloseWrite()
	_ = channel.Close()
}

func short(s string) string {
	const max = 160
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
