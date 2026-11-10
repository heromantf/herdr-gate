package main

import (
	"encoding/base64"
	"io"
	"os"
)

// statusReporter tells herdr when the gate is waiting for the person, in the
// Program Status Protocol (OSC 7501) that herdr reads from every terminal it
// runs. Herdr then shows the gate as needing attention, the way it does for
// an agent asking permission, so nobody has to watch the gate's window.
//
// A nil reporter says nothing: outside herdr the sequence means nothing to
// the terminal showing it.
type statusReporter struct {
	out io.Writer
}

// newStatusReporter returns a reporter only when the gate runs in a herdr
// terminal that will take the sequences.
func newStatusReporter(out *os.File) *statusReporter {
	if os.Getenv("HC_TERMINAL_ID") == "" || !takesTerminalSequences(out) {
		return nil
	}
	return &statusReporter{out: out}
}

// blocked reports that the gate waits for a yes or no about a device.
func (s *statusReporter) blocked(title, message string) {
	if s == nil {
		return
	}
	_, _ = io.WriteString(s.out, blockedSequence(title, message))
}

// clear withdraws the report: the gate is not an agent the rest of the time,
// and herdr stops listing it as one.
func (s *statusReporter) clear() {
	if s == nil {
		return
	}
	_, _ = io.WriteString(s.out, clearSequence)
}

const clearSequence = "\x1b]7501;state=clear\x1b\\"

// Title and message travel base64-encoded; the other fields are plain words.
func blockedSequence(title, message string) string {
	encode := base64.StdEncoding.EncodeToString
	return "\x1b]7501;state=blocked:kind=permission:app=herdr-gate" +
		":title=" + encode([]byte(title)) + ":msg=" + encode([]byte(message)) + "\x1b\\"
}
