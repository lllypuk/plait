package cli_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/lllypuk/plait/internal/cli"
)

func TestSetupText(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := []byte("#!/bin/sh\necho 0.9.1\n")
	err := os.WriteFile(filepath.Join(bin, "qwen"), fake, 0o700) //nolint:gosec // test executable
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))

	setup := func(args ...string) (int, string) {
		var out bytes.Buffer
		code := cli.Run(cli.Options{
			Version:    "v0.1.0",
			Args:       append([]string{"setup"}, args...),
			Dir:        tmp,
			Stdout:     &out,
			Stderr:     io.Discard,
			IsTerminal: func(io.Writer) bool { return true },
		})
		return code, out.String()
	}

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "dry run",
			args: []string{"--dry-run"},
			want: []string{
				`(?m)^create +qwen +skill plait +\S+/home/\.qwen/skills/plait/SKILL\.md$`,
				`dry run: nothing written\n$`,
			},
		},
		{name: "install", want: []string{`(?m)^create +qwen +permissions +\S+settings\.json +deny `, `done; restart`}},
		{name: "repeat", want: []string{`(?m)^skip +qwen +skill plait-mr `, `nothing changed\n$`}},
	}
	for _, tt := range tests {
		code, out := setup(tt.args...)
		if code != 0 {
			t.Fatalf("%s: exit code = %d, out %q", tt.name, code, out)
		}
		for _, w := range tt.want {
			if !regexp.MustCompile(w).MatchString(out) {
				t.Errorf("%s: output does not match %s:\n%s", tt.name, w, out)
			}
		}
	}
}
