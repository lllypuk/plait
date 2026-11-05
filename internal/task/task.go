// Package task normalizes the task description of a run into task.md.
package task

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
)

// FileName is the normalized task inside the run directory.
const FileName = "task.md"

// DefaultSource is the --source of a task described locally.
const DefaultSource = "local"

// Sources lists the accepted values of --source.
func Sources() []string {
	return []string{DefaultSource, "jira", "agent"}
}

const (
	criteriaHeading = "Критерии приёмки"
	gapMarker       = "<!-- plait:gap acceptance_criteria -->"
	gapText         = "Не заданы."
	saveFix         = "plait ctx save < task.md"
)

var (
	headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*?)(?:\s+#+)?\s*$`)
	markerRe  = regexp.MustCompile(`^\s*<!--\s*plait:.*-->\s*$`)
	fenceRe   = regexp.MustCompile("^\\s*(```|~~~)")
	itemRe    = regexp.MustCompile(`^ {0,3}(?:[-*+]|\d{1,9}[.)])\s+(?:\[[ xX]\](?:\s+|$))?(.*)$`)
)

// Input is what ctx save receives.
type Input struct {
	Text   []byte
	Key    string
	Source string
}

// Doc is a normalized task.md.
type Doc struct {
	Markdown    []byte
	HasCriteria bool
}

// Result describes what ctx save did.
type Result struct {
	Run         string   `json:"run"`
	Path        string   `json:"path"`
	Source      string   `json:"source"`
	Key         string   `json:"key,omitempty"`
	HasCriteria bool     `json:"has_criteria"`
	Changed     bool     `json:"changed"`
	Step        run.Step `json:"step"`
}

// Normalize builds task.md: title, description, then the acceptance criteria or an explicit gap; it never invents criteria.
func Normalize(in Input) (Doc, error) {
	if !slices.Contains(Sources(), in.Source) {
		return Doc{}, output.Invalid("invalid_source",
			fmt.Sprintf("source %q must be one of %s", in.Source, strings.Join(Sources(), ", ")),
			saveFix+" --source local")
	}
	if in.Key != "" && !run.ValidKey(in.Key) {
		return Doc{}, output.Invalid("invalid_key", fmt.Sprintf("task key %q must look like PROJ-123", in.Key),
			"plait ctx save PROJ-123 < task.md, or drop the key")
	}
	p := parse(string(in.Text))
	if p.title == "" && len(p.body) == 0 && len(p.criteria) == 0 {
		return Doc{}, output.Invalid("empty_task", "the task description is empty",
			"pipe the task Markdown with its acceptance criteria: "+saveFix)
	}
	if err := p.takeTitle(); err != nil {
		return Doc{}, err
	}
	title := p.title
	if in.Key != "" && !strings.HasPrefix(title, in.Key+":") {
		title = in.Key + ": " + title
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n<!-- plait:source %s -->\n", title, in.Source)
	if len(p.body) > 0 {
		fmt.Fprintf(&b, "\n%s\n", strings.Join(p.body, "\n"))
	}
	fmt.Fprintf(&b, "\n## %s\n\n", criteriaHeading)
	hasCriteria := len(p.criteria) > 0
	if hasCriteria {
		b.WriteString(strings.Join(p.criteria, "\n"))
	} else {
		b.WriteString(gapMarker + "\n" + gapText)
	}
	b.WriteString("\n")
	return Doc{Markdown: []byte(b.String()), HasCriteria: hasCriteria}, nil
}

// Save normalizes the task into task.md of the current run; the same input again changes nothing.
func Save(s *run.Store, in Input) (Result, error) {
	doc, err := Normalize(in)
	if err != nil {
		return Result{}, err
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
	path := filepath.Join(s.RunDir(st.ID), FileName)
	old, err := os.ReadFile(filepath.Clean(path))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Result{}, output.Internal(fmt.Errorf("read %s: %w", path, err))
	}
	meta := run.Task{Source: in.Source, Key: in.Key, HasCriteria: doc.HasCriteria}
	res := Result{Run: st.ID, Path: path, Source: in.Source, Key: in.Key, HasCriteria: doc.HasCriteria}
	if bytes.Equal(old, doc.Markdown) && st.Task != nil && *st.Task == meta && st.Step != run.StepNew {
		res.Step = st.Step
		return res, nil
	}
	if err = run.WriteFile(path, doc.Markdown); err != nil {
		return Result{}, output.Internal(err)
	}
	st.Task = &meta
	// A changed task makes earlier snapshots and reviews outdated, so the run goes back to export.
	st.Step = run.StepTaskSaved
	if err = s.Save(st); err != nil {
		return Result{}, err
	}
	res.Changed, res.Step = true, st.Step
	return res, nil
}

// Read returns the title and the acceptance criteria items of a task.md written by Save.
func Read(md []byte) (string, []string) {
	p := parse(string(md))
	return p.title, items(p.criteria)
}

// items splits the criteria section into list items; an indented line continues the item above it.
func items(lines []string) []string {
	var out []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		m := itemRe.FindStringSubmatch(l)
		switch {
		case t == "":
		case m != nil:
			if it := strings.TrimSpace(m[1]); it != "" {
				out = append(out, it)
			}
		case len(out) > 0 && strings.TrimLeft(l, " \t") != l:
			out[len(out)-1] += " " + t
		default:
			out = append(out, t)
		}
	}
	return out
}

type parsed struct {
	title    string
	body     []string
	criteria []string

	inFence    bool
	inCriteria bool
	level      int
	gap        bool
}

func parse(text string) parsed {
	var p parsed
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		p.line(line)
	}
	p.body = trimBlank(p.body)
	p.criteria = trimBlank(p.criteria)
	if p.gap {
		p.criteria = nil
	}
	return p
}

func (p *parsed) line(line string) {
	if fenceRe.MatchString(line) {
		p.inFence = !p.inFence
	}
	if !p.inFence && p.special(line) {
		return
	}
	if p.inCriteria {
		p.criteria = append(p.criteria, line)
	} else {
		p.body = append(p.body, line)
	}
}

// special consumes plait markers, the title and the criteria heading.
func (p *parsed) special(line string) bool {
	if markerRe.MatchString(line) {
		p.gap = p.gap || (p.inCriteria && strings.TrimSpace(line) == gapMarker)
		return true
	}
	m := headingRe.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	if p.inCriteria && len(m[1]) <= p.level {
		p.inCriteria = false
	}
	switch {
	case p.inCriteria:
		return false
	case isCriteriaHeading(m[2]):
		p.inCriteria, p.level = true, len(m[1])
		return true
	case p.title == "" && len(m[1]) == 1:
		p.title = m[2]
		return true
	}
	return false
}

func isCriteriaHeading(h string) bool {
	switch strings.ToLower(strings.TrimSuffix(h, ":")) {
	case "критерии приёмки", "критерии приемки", "acceptance criteria":
		return true
	}
	return false
}

// takeTitle falls back to the first body line when the input has no level-one heading.
func (p *parsed) takeTitle() error {
	if p.title != "" {
		return nil
	}
	if len(p.body) == 0 || headingRe.MatchString(p.body[0]) || fenceRe.MatchString(p.body[0]) {
		return output.Invalid("no_title", "the task has no title",
			"start the Markdown with '# <task summary>', then: "+saveFix)
	}
	p.title = strings.TrimSpace(p.body[0])
	p.body = trimBlank(p.body[1:])
	return nil
}

func trimBlank(lines []string) []string {
	blank := func(s string) bool { return strings.TrimSpace(s) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	return lines
}
