//go:build ignore

// Builds herdr-gate for every system it runs on, into the mod's bin/, which
// is where the mod looks for it. From this folder:
//
//	go run build.go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

var targets = []struct{ os, arch string }{
	{"windows", "amd64"},
	{"darwin", "arm64"},
	{"darwin", "amd64"},
	{"linux", "amd64"},
	{"linux", "arm64"},
}

func main() {
	if _, err := os.Stat("go.mod"); err != nil {
		fmt.Fprintln(os.Stderr, "run this from herdr-gate's own folder: go run build.go")
		os.Exit(1)
	}
	out := filepath.Join("mods", "herdr-gate", "bin")
	for _, t := range targets {
		name := "herdr-gate-" + t.os + "-" + t.arch
		if t.os == "windows" {
			name += ".exe"
		}
		path := filepath.Join(out, name)
		// "go" is the one running this file: go run puts its own first in PATH.
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", path, ".")
		cmd.Env = append(os.Environ(), "GOOS="+t.os, "GOARCH="+t.arch, "CGO_ENABLED=0")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "%s/%s: %v\n", t.os, t.arch, err)
			os.Exit(1)
		}
		fmt.Println(path)
	}
}
