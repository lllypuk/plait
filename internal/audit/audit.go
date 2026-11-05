// Package audit appends one JSON line per run command to audit.jsonl: paths, hashes and decisions, never content.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lllypuk/plait/internal/agent"
)

// FileName is the audit log in the run directory.
const FileName = "audit.jsonl"

// MaxLine bounds one log line; longer lines lose list items and get Truncated.
const MaxLine = 64 << 10

// Reasons a file was left out of the package.
const (
	ReasonDenyGlob  = "deny_glob"
	ReasonDiffLimit = "diff_limit"
)

const agentUnknown = "unknown"

// File is a file the command read for the package, by its hash.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Excluded is a file the package left out.
type Excluded struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Entry is one line of audit.jsonl.
type Entry struct {
	TS            time.Time  `json:"ts"`
	Version       string     `json:"plait_version"`
	Cmd           string     `json:"cmd"`
	Run           string     `json:"run"`
	StepFrom      string     `json:"step_from"`
	StepTo        string     `json:"step_to"`
	Agent         string     `json:"agent"`
	Snapshot      int        `json:"snapshot,omitempty"`
	PackageSHA256 string     `json:"package_sha256,omitempty"`
	Context       []File     `json:"context,omitempty"`
	Excluded      []Excluded `json:"excluded,omitempty"`
	AnswerSHA256  string     `json:"answer_sha256,omitempty"`
	Error         string     `json:"error,omitempty"`
	Truncated     bool       `json:"truncated,omitempty"`
}

// SHA256 is the hex digest of b.
func SHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Agent names the coding agent from PLAIT_AGENT, else the first of agents whose session_env is 1.
func Agent(getenv func(string) string, agents []agent.Descriptor) string {
	if a := getenv("PLAIT_AGENT"); a != "" {
		return a
	}
	for _, d := range agents {
		if d.SessionEnv != "" && getenv(d.SessionEnv) == "1" {
			return d.Name
		}
	}
	return agentUnknown
}

// Append writes e as one line of dir/audit.jsonl; the caller holds the store lock, so lines never interleave.
func Append(dir string, e Entry) error {
	b, err := line(e)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, FileName)
	f, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	_, err = f.Write(b)
	if err = errors.Join(err, f.Close()); err != nil {
		return fmt.Errorf("append %s: %w", path, err)
	}
	return nil
}

// line encodes e with a trailing newline, halving the longer list until it fits MaxLine.
func line(e Entry) ([]byte, error) {
	for {
		b, err := json.Marshal(e)
		if err != nil {
			return nil, fmt.Errorf("encode audit entry: %w", err)
		}
		if len(b) < MaxLine {
			return append(b, '\n'), nil
		}
		switch {
		case len(e.Context) == 0 && len(e.Excluded) == 0:
			return nil, fmt.Errorf("audit entry of %s is %d bytes without lists", e.Cmd, len(b))
		case len(e.Context) >= len(e.Excluded):
			e.Context = e.Context[:len(e.Context)/2]
		default:
			e.Excluded = e.Excluded[:len(e.Excluded)/2]
		}
		e.Truncated = true
	}
}
