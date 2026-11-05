// Package agent describes coding agents as data: built-in descriptors and agents.<name> from the config.
package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/lllypuk/plait/internal/output"
)

// DetectTimeout bounds the version command of an agent.
const DetectTimeout = 5 * time.Second

const waitDelay = time.Second

// Descriptor is an agent in plait-agent/v1 after base inheritance is resolved.
type Descriptor struct {
	Name             string         `json:"name"`
	Display          string         `json:"display,omitempty"`
	Cmd              string         `json:"cmd"`
	VersionArgs      []string       `json:"version_args"`
	SkillDirs        Scopes         `json:"skill_dirs"`
	ContextFile      string         `json:"context_file"`
	SkillFrontmatter map[string]any `json:"skill_frontmatter,omitempty"`
	Permissions      Permissions    `json:"permissions"`
	TelemetryOff     []Condition    `json:"telemetry_off,omitempty"`
	ModelEndpointEnv string         `json:"model_endpoint_env,omitempty"`
}

// Scopes holds a path per install scope: User starts with ~/, Project is relative to the repository root.
type Scopes struct {
	User    string `json:"user"`
	Project string `json:"project"`
}

// Permissions says how to deny commands to the agent.
type Permissions struct {
	Writer  string   `json:"writer"`
	User    string   `json:"user,omitempty"`
	Project string   `json:"project,omitempty"`
	Deny    []string `json:"deny,omitempty"`
	Source  string   `json:"source,omitempty"`
}

// Condition is one sign that telemetry is off: exactly one of Env and JSON is set.
type Condition struct {
	Env  string         `json:"env,omitempty"`
	JSON *JSONCondition `json:"json,omitempty"`
}

// JSONCondition checks the value at a dotted Path of File; a nil Equals accepts any truthy value.
type JSONCondition struct {
	File   string `json:"file"`
	Path   string `json:"path"`
	Equals any    `json:"equals,omitempty"`
}

// Detection is what Detect found; an agent missing from PATH is not installed, not an error.
type Detection struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
}

// Detect looks the agent up in PATH and runs its version command for at most DetectTimeout.
func (d Descriptor) Detect(ctx context.Context) (Detection, error) {
	path, err := exec.LookPath(d.Cmd)
	if err != nil {
		return Detection{}, nil
	}
	det := Detection{Installed: true, Path: path}
	ctx, cancel := context.WithTimeout(ctx, DetectTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, d.VersionArgs...)
	// A child of a killed shell script keeps stdout open; without WaitDelay Wait blocks until it exits.
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	run := strings.TrimSpace(path + " " + strings.Join(d.VersionArgs, " "))
	fix := fmt.Sprintf("check that %q prints a version, or set agents.%s.cmd and agents.%s.version_args "+
		"in .plait/config.yaml, then rerun the command", run, d.Name, d.Name)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return det, detectError(fmt.Sprintf("agent %s: %q did not finish", d.Name, run), fix, ctx.Err())
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return det, detectError(fmt.Sprintf("agent %s: %q failed: %s", d.Name, run, msg), fix, err)
	}
	det.Version, _, _ = strings.Cut(strings.TrimSpace(stdout.String()), "\n")
	return det, nil
}

func detectError(msg, fix string, err error) error {
	e := output.Invalid("agent_detect", msg, fix)
	e.Err = err
	return e
}
