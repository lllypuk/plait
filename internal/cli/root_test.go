package cli_test

import (
	"bytes"
	"testing"

	"github.com/lllypuk/plait/internal/cli"
)

func TestVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "ldflags", version: "v0.1.0-3-gabc1234", want: "v0.1.0-3-gabc1234\n"},
		{name: "no ldflags", version: "", want: "dev\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := cli.NewRoot(tt.version)
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs([]string{"version"})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got := out.String(); got != tt.want {
				t.Errorf("version output = %q, want %q", got, tt.want)
			}
		})
	}
}
