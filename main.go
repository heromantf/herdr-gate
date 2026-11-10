// herdr-gate is a small SSH server for one person's own machines. It lets
// herdr's desktop app and phone app reach this machine as an ordinary SSH
// host without turning on the system's sshd.
//
// It accepts public keys only, asks in its own window before trusting a new
// device, and runs the command the client sends the way this system's shell
// would. It offers no interactive terminal, file transfer or port forwarding.
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

const version = "0.1.0"

func main() {
	// Before anything is said: the flags below are described in the language.
	if chosen := languageFlag(os.Args[1:]); chosen == "en" || chosen == "zh" {
		language = chosen
	}
	lang := flag.String("lang", "", T("language to speak, en or zh (default: the system's)", "使用的语言，en 或 zh（默认跟随系统）"))
	listen := flag.String("listen", ":7522", T("address to listen on; by default port 7522 on every interface", "监听地址，默认所有网卡的 7522 端口"))
	dirFlag := flag.String("dir", "", T("folder for this machine's key and the device list (default ~/.config/herdr-gate)", "存放本机指纹密钥和设备名单的文件夹（默认 ~/.config/herdr-gate）"))
	info := flag.Bool("info", false, T("print this machine's addresses, fingerprint, approved devices and current connections as JSON, then exit", "输出本机地址、指纹、已批准的设备和当前连接（JSON），然后退出"))
	revoke := flag.String("revoke", "", T("revoke an approved device, named by its fingerprint or public key, then exit", "撤销一台已批准的设备（给出它的指纹或公钥），然后退出"))
	showVersion := flag.Bool("version", false, T("print the version", "显示版本"))
	flag.Parse()
	if *lang != "" && *lang != "en" && *lang != "zh" {
		fmt.Fprintf(os.Stderr, T("herdr-gate: unknown language %q: use en or zh\n", "herdr-gate: 不认识的语言 %q，请用 en 或 zh\n"), *lang)
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println("herdr-gate", version)
		return
	}
	if err := run(*listen, *dirFlag, *info, *revoke); err != nil {
		fmt.Fprintln(os.Stderr, "herdr-gate:", err)
		os.Exit(1)
	}
}

func run(listen, dir string, info bool, revoke string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf(T("cannot find the home folder: %w", "找不到用户主目录: %w"), err)
	}
	if dir == "" {
		dir = os.Getenv("HERDR_GATE_HOME")
	}
	if dir == "" {
		dir = filepath.Join(home, ".config", "herdr-gate")
	}
	devices := &deviceList{path: filepath.Join(dir, "authorized_keys")}
	if info {
		return printInfo(dir, os.Stdout)
	}
	if revoke != "" {
		removed, err := devices.remove(revoke)
		if err != nil {
			return fmt.Errorf(T("cannot change the device list: %w", "无法修改设备名单: %w"), err)
		}
		if removed == 0 {
			return fmt.Errorf(T("no such device on the list: %s", "名单里没有这台设备: %s"), revoke)
		}
		fmt.Println(T("Revoked. A running herdr-gate closes its connections within seconds.", "已撤销。正在运行的 herdr-gate 会在几秒内断开它的连接。"))
		return nil
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	hostKey, err := loadOrCreateHostKey(filepath.Join(dir, "host_key"))
	if err != nil {
		return fmt.Errorf(T("this machine's key: %w", "本机指纹密钥: %w"), err)
	}
	l, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf(T("cannot listen on %s: %w", "无法监听 %s: %w"), listen, err)
	}

	status := newStatusReporter(os.Stdout)
	g := &gate{
		devices:  devices,
		approver: newApprover(os.Stdin, os.Stdout, status),
		out:      os.Stdout,
		home:     home,
		user:     currentUser(),

		statePath: filepath.Join(dir, connectionsFile),
	}
	fmt.Printf("herdr-gate %s\n", version)
	fmt.Printf(T("Listening:   %s\n", "监听:     %s\n"), l.Addr())
	if g.user != "" {
		fmt.Printf(T("User name:   %s (other devices log in with it; letter case does not matter)\n", "用户名:   %s（别的设备登录时用它，不分大小写）\n"), g.user)
	}
	if addrs, _ := localAddresses(); len(addrs) > 0 {
		fmt.Printf(T("Addresses:   %s\n", "本机地址: %s\n"), strings.Join(addrs, "  "))
	}
	fmt.Printf(T("Fingerprint: %s (%s)\n", "本机指纹: %s (%s)\n"), ssh.FingerprintSHA256(hostKey.PublicKey()), hostKey.PublicKey().Type())
	fmt.Printf(T("Devices:     %s (%d approved)\n", "设备名单: %s（已批准 %d 台）\n"), devices.path, devices.count())
	if status != nil {
		fmt.Println(T("Running in a herdr terminal: herdr is told when a new device waits for approval.", "正在 herdr 的终端里运行：有新设备等待批准时会通知 herdr。"))
	}
	fmt.Println(T("A new device is asked about here the first time it connects. Ctrl+C stops.", "新设备第一次连接时会在这里询问。按 Ctrl+C 停止。"))
	fmt.Println()
	return g.serve(l, hostKey)
}
