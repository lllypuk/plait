package agent_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/output"
)

// fakeAgent writes an executable shell script cmd into a new directory and returns the directory.
func fakeAgent(t *testing.T, cmd, body string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	err := os.WriteFile(filepath.Join(dir, cmd), []byte(script), 0o700) //nolint:gosec // test executable
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

const (
	versionFlag = "--version"
	fake        = "fake"
)

func descriptor() agent.Descriptor {
	return agent.Descriptor{Name: fake, Cmd: fake, VersionArgs: []string{versionFlag}}
}

func TestDetectVersion(t *testing.T) {
	t.Setenv("PATH", fakeAgent(t, fake, `[ "$1" = --version ] && printf '2.1.0 (Fake Code)\nextra\n'`))
	got, err := descriptor().Detect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Installed || got.Version != "2.1.0 (Fake Code)" || filepath.Base(got.Path) != fake {
		t.Fatalf("Detect = %+v", got)
	}
}

func TestDetectNotInPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	got, err := descriptor().Detect(t.Context())
	if err != nil || got.Installed {
		t.Fatalf("Detect = %+v, %v; want not installed, no error", got, err)
	}
}

func TestDetectFails(t *testing.T) {
	t.Setenv("PATH", fakeAgent(t, fake, "echo boom >&2; exit 3"))
	got, err := descriptor().Detect(t.Context())
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.Code != "agent_detect" || !strings.Contains(e.Message, "boom") || e.Fix == "" {
		t.Fatalf("err = %#v", err)
	}
	if !got.Installed {
		t.Fatal("a failing version command still means the agent is installed")
	}
}

func TestDetectTimeout(t *testing.T) {
	t.Setenv("PATH", fakeAgent(t, fake, "sleep 30")+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := descriptor().Detect(ctx)
	if time.Since(start) > 5*time.Second {
		t.Fatalf("Detect waited %v for a hung agent", time.Since(start))
	}
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.Code != "agent_detect" || !strings.Contains(e.Message, "did not finish") ||
		!strings.Contains(e.Fix, "agents.fake.") {
		t.Fatalf("err = %#v", err)
	}
}
