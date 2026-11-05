// Package check stores the results of checks the agent ran itself, bound to the working tree.
package check

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/vcs"
)

// Schema versions checks/<name>.json.
const Schema = "plait-check/v1"

// Limits of Record.OutputTail.
const (
	MaxTailLines = 200
	MaxTailBytes = 16 << 10
)

// Dir is the checks directory inside the run directory.
const Dir = "checks"

const dirPerm = 0o750

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	secretRe = regexp.MustCompile(
		`(?i)([\w.\-"']*(?:token|secret|password|passwd|api_key|apikey|private_key)[\w.\-"']*[ \t]*[:=][ \t]*)\S.*`)
)

// Input is what check save receives.
type Input struct {
	Name     string
	ExitCode int
	Output   []byte
}

// Record is checks/<name>.json.
type Record struct {
	Schema       string    `json:"schema"`
	Name         string    `json:"name"`
	ExitCode     int       `json:"exit_code"`
	Tree         string    `json:"tree"`
	SavedAt      time.Time `json:"saved_at"`
	OutputSHA256 string    `json:"output_sha256"`
	OutputTail   string    `json:"output_tail"`
}

// Result describes what check save did.
type Result struct {
	Run      string   `json:"run"`
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	ExitCode int      `json:"exit_code"`
	Tree     string   `json:"tree"`
	Changed  bool     `json:"changed"`
	Step     run.Step `json:"step"`
}

// Save writes the check result of the current run; the same output, exit code and tree again change nothing.
func Save(ctx context.Context, s *run.Store, repo vcs.Repo, in Input) (Result, error) {
	if !nameRe.MatchString(in.Name) {
		return Result{}, output.Invalid("invalid_check_name",
			fmt.Sprintf("check name %q must match %s", in.Name, nameRe),
			"name the check in lower case, for example: plait check save test --exit-code $? < test.log")
	}
	tree, err := repo.CurrentTree(ctx)
	if err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(in.Output)
	rec := Record{
		Schema:       Schema,
		Name:         in.Name,
		ExitCode:     in.ExitCode,
		Tree:         tree,
		OutputSHA256: hex.EncodeToString(sum[:]),
		OutputTail:   Tail([]byte(Redact(string(in.Output)))),
	}

	unlock, err := s.Lock()
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	st, err := s.Current()
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Join(s.RunDir(st.ID), Dir)
	path := filepath.Join(dir, in.Name+".json")
	res := Result{Run: st.ID, Name: in.Name, Path: path, ExitCode: in.ExitCode, Tree: tree, Step: st.Step}
	if old, ok := read(path); ok && old.ExitCode == rec.ExitCode && old.Tree == rec.Tree &&
		old.OutputSHA256 == rec.OutputSHA256 {
		return res, nil
	}
	rec.SavedAt = s.Now().UTC()
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return Result{}, output.Internal(fmt.Errorf("encode check: %w", err))
	}
	if err = os.MkdirAll(dir, dirPerm); err != nil {
		return Result{}, output.Internal(fmt.Errorf("create %s: %w", dir, err))
	}
	if err = run.WriteFile(path, append(b, '\n')); err != nil {
		return Result{}, output.Internal(err)
	}
	res.Changed = true
	return res, nil
}

// read treats a missing or unreadable record as absent, so save overwrites it.
func read(path string) (Record, bool) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Record{}, false
	}
	var r Record
	if json.Unmarshal(b, &r) != nil {
		return Record{}, false
	}
	return r, true
}

// Redact replaces the value of each "name=value" or "name: value" whose name looks like a secret, up to the end of the line.
func Redact(s string) string {
	return secretRe.ReplaceAllString(s, "${1}[redacted]")
}

// Tail keeps the last MaxTailLines lines within MaxTailBytes, dropping a cut-off first line when a whole one follows.
func Tail(out []byte) string {
	s := string(out)
	body := strings.TrimSuffix(s, "\n")
	lines := 0
	for i := len(body) - 1; i >= 0; i-- {
		if body[i] != '\n' {
			continue
		}
		if lines++; lines == MaxTailLines {
			s = s[i+1:]
			break
		}
	}
	if len(s) <= MaxTailBytes {
		return s
	}
	cut := len(s) - MaxTailBytes
	if s[cut-1] != '\n' {
		if i := strings.IndexByte(s[cut:], '\n'); i >= 0 && cut+i+1 < len(s) {
			return s[cut+i+1:]
		}
	}
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return s[cut:]
}
