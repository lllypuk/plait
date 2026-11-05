package cli_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/cli"
)

const versionCmd = "version"

func run(t *testing.T, version string, tty bool, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Run(cli.Options{
		Version:    version,
		Args:       args,
		Stdout:     &out,
		Stderr:     &errOut,
		IsTerminal: func(io.Writer) bool { return tty },
	})
	return code, out.String(), errOut.String()
}

func TestVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		tty     bool
		args    []string
		want    string
	}{
		{name: "ldflags tty", version: "v0.1.0-3-gabc1234", tty: true, args: []string{versionCmd},
			want: "v0.1.0-3-gabc1234\n"},
		{name: "no ldflags tty", tty: true, args: []string{versionCmd}, want: "dev\n"},
		{name: "not tty", version: "v1", args: []string{versionCmd},
			want: `{"ok":true,"data":{"version":"v1"}}` + "\n"},
		{name: "tty with --json", version: "v1", tty: true, args: []string{versionCmd, "--json"},
			want: `{"ok":true,"data":{"version":"v1"}}` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := run(t, tt.version, tt.tty, tt.args...)
			if code != 0 || errOut != "" {
				t.Fatalf("code = %d, stderr = %q", code, errOut)
			}
			if out != tt.want {
				t.Errorf("stdout = %q, want %q", out, tt.want)
			}
		})
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		tty  bool
		args []string
		want string
	}{
		{name: "unknown command", args: []string{"frobnicate"}, want: `"fix":"plait --help`},
		{name: "unknown command --json on tty", tty: true, args: []string{"frobnicate", "--json"},
			want: `"code":"usage"`},
		{name: "extra args", args: []string{versionCmd, "extra"}, want: `"fix":"plait version --help`},
		{name: "unknown flag", args: []string{versionCmd, "--nope"}, want: `"code":"usage"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, _ := run(t, "v1", tt.tty, tt.args...)
			if code != 2 {
				t.Errorf("code = %d, want 2", code)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("stdout = %q, want substring %q", out, tt.want)
			}
		})
	}
}

func TestUsageErrorText(t *testing.T) {
	code, out, errOut := run(t, "v1", true, "frobnicate")
	if code != 2 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	if !strings.Contains(errOut, "fix: plait --help") {
		t.Errorf("stderr = %q, want fix line", errOut)
	}
}
