// Package output renders results as JSON for agents or text for humans and maps errors to exit codes.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"golang.org/x/term"
)

// Mode is the output format.
type Mode int

// Output modes.
const (
	Text Mode = iota
	JSON
)

// SelectMode picks text only for a terminal without --json.
func SelectMode(forceJSON, tty bool) Mode {
	if forceJSON || !tty {
		return JSON
	}
	return Text
}

// IsTerminal reports whether w is a file attached to a terminal.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

type successEnvelope struct {
	OK   bool `json:"ok"`
	Data any  `json:"data"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

type errorEnvelope struct {
	OK    bool      `json:"ok"`
	Error errorBody `json:"error"`
}

// Printer writes results; in JSON mode errors also go to Out so the agent reads one stream.
type Printer struct {
	Out  io.Writer
	Err  io.Writer
	Mode Mode
}

// Success writes data as a JSON envelope or text for a human.
func (p Printer) Success(data any, text string) error {
	if p.Mode == JSON {
		return writeJSON(p.Out, successEnvelope{OK: true, Data: data})
	}
	return writeLine(p.Out, text)
}

// Fail renders err and returns the exit code; write failures are ignored as there is nowhere left to report them.
func (p Printer) Fail(err error) int {
	e := AsError(err)
	if e == nil {
		return ExitOK
	}
	if p.Mode == JSON {
		_ = writeJSON(p.Out, errorEnvelope{Error: errorBody{Code: e.Code, Message: e.Message, Fix: e.Fix}})
		return e.ExitCode()
	}
	_ = writeLine(p.Err, "error: "+e.Message)
	if e.Fix != "" {
		_ = writeLine(p.Err, "fix: "+e.Fix)
	}
	return e.ExitCode()
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

func writeLine(w io.Writer, s string) error {
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	if _, err := io.WriteString(w, s); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}
