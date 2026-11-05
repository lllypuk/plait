package install

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/output"
)

// Repository files plait init writes, relative to the root.
const (
	AgentsFile = "AGENTS.md"
	ConfigFile = ".plait/config.yaml"
	ignoreFile = ".gitignore"
)

// Items of the repository operations.
const (
	ItemConfig    = "config"
	ItemSection   = "agents section"
	ItemImport    = "import"
	ItemGitignore = "gitignore"
)

const (
	sectionBegin = "<!-- plait:begin -->"
	sectionEnd   = "<!-- plait:end -->"
	importLine   = "@" + AgentsFile
)

// ignoreLines hide runs but keep the files the team commits; git cannot re-include them under an ignored directory,
// so the first line undoes an existing .plait/ rule.
func ignoreLines() []string {
	return []string{"!/.plait/", "/.plait/*", "!/" + ConfigFile, "!/.plait/rules.md"}
}

var (
	//go:embed templates/*.tmpl
	templateFiles embed.FS

	templates = template.Must(template.New("").Option("missingkey=error"). //nolint:gochecknoglobals // parsed once
			ParseFS(templateFiles, "templates/*.tmpl"))
)

// RepoPlan returns the operations that connect the repository at env.Root to plait for agents.
func RepoPlan(agents []agent.Descriptor, env Env, version string) ([]Op, error) {
	cfg, err := configOp(env.Root, version)
	if err != nil {
		return nil, err
	}
	aops, err := PlanAll(agents, ScopeProject, env, version, false)
	if err != nil {
		return nil, err
	}
	ops := append([]Op{cfg}, aops...)
	section, err := sectionOp(filepath.Join(env.Root, AgentsFile))
	if err != nil {
		return nil, err
	}
	ops = append(ops, section)
	for _, d := range agents {
		if d.ContextFile == AgentsFile {
			continue
		}
		path, pathErr := env.Path(d.Name, ScopeProject, d.ContextFile)
		if pathErr != nil {
			return nil, pathErr
		}
		op, importErr := importOp(path)
		if importErr != nil {
			return nil, importErr
		}
		op.Agent = d.Name
		ops = append(ops, op)
	}
	ignore, err := ignoreOp(filepath.Join(env.Root, ignoreFile))
	if err != nil {
		return nil, err
	}
	return Unique(append(ops, ignore)), nil
}

func render(name string, data any) ([]byte, error) {
	var b bytes.Buffer
	if err := templates.ExecuteTemplate(&b, name, data); err != nil {
		return nil, output.Internal(err)
	}
	return b.Bytes(), nil
}

// readFile returns the content and mode of path; a missing file is nil content and no error.
func readFile(path string) ([]byte, fs.FileMode, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fileMode, nil
	}
	if err != nil {
		return nil, 0, output.Internal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, output.Internal(err)
	}
	return b, info.Mode().Perm(), nil
}

// appendBlock separates block from the text before it with one blank line.
func appendBlock(old, block []byte) []byte {
	switch {
	case len(old) == 0:
		return block
	case bytes.HasSuffix(old, []byte("\n\n")):
	case bytes.HasSuffix(old, []byte("\n")):
		old = append(old, '\n')
	default:
		old = append(old, "\n\n"...)
	}
	return append(old, block...)
}

func configOp(root, version string) (Op, error) {
	path := filepath.Join(root, filepath.FromSlash(ConfigFile))
	op := Op{Action: ActionSkip, Path: path, Item: ItemConfig}
	if _, err := os.Lstat(path); err == nil {
		return op, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Op{}, output.Internal(err)
	}
	b, err := render("config.yaml.tmpl", struct{ Version string }{version})
	if err != nil {
		return Op{}, err
	}
	op.Action, op.content, op.mode = ActionCreate, b, fileMode
	return op, nil
}

func sectionOp(path string) (Op, error) {
	body, err := render("agents-section.md.tmpl", nil)
	if err != nil {
		return Op{}, err
	}
	block := []byte(sectionBegin + "\n" + string(body) + sectionEnd)
	line := []byte(string(block) + "\n")
	old, mode, err := readFile(path)
	if err != nil {
		return Op{}, err
	}
	op := Op{Action: ActionUpdate, Path: path, Item: ItemSection, mode: mode}
	begins, ends := bytes.Count(old, []byte(sectionBegin)), bytes.Count(old, []byte(sectionEnd))
	b, e := bytes.Index(old, []byte(sectionBegin)), bytes.Index(old, []byte(sectionEnd))
	switch {
	case old == nil:
		op.Action, op.content = ActionCreate, line
	case begins == 0 && ends == 0:
		op.content = appendBlock(old, line)
	case begins != 1 || ends != 1 || e < b:
		return Op{}, output.Invalid(
			"agents_section_broken",
			fmt.Sprintf(
				"%s: the plait section needs exactly one %s followed by one %s",
				path,
				sectionBegin,
				sectionEnd,
			),
			"leave one pair of the markers in "+AgentsFile+" or delete both, then rerun plait init",
		)
	default:
		op.content = bytes.Join([][]byte{old[:b], block, old[e+len(sectionEnd):]}, nil)
	}
	if bytes.Equal(op.content, old) {
		return Op{Action: ActionSkip, Path: path, Item: ItemSection}, nil
	}
	return op, nil
}

func importOp(path string) (Op, error) {
	old, mode, err := readFile(path)
	if err != nil {
		return Op{}, err
	}
	op := Op{Action: ActionUpdate, Path: path, Item: ItemImport, Detail: importLine, mode: mode}
	switch {
	case old == nil:
		op.Action, op.content = ActionCreate, []byte(importLine+"\n")
	case hasLine(old, importLine), hasLine(old, "@./"+AgentsFile):
		return Op{Action: ActionSkip, Path: path, Item: ItemImport}, nil
	default:
		op.content = appendBlock(old, []byte(importLine+"\n"))
	}
	return op, nil
}

func ignoreOp(path string) (Op, error) {
	old, mode, err := readFile(path)
	if err != nil {
		return Op{}, err
	}
	if ignoreEffective(old) {
		return Op{Action: ActionSkip, Path: path, Item: ItemGitignore}, nil
	}
	var add []string
	for _, l := range ignoreLines() {
		if !hasLine(old, l) {
			add = append(add, l)
		}
	}
	content := appendLines(old, add)
	if !ignoreEffective(content) {
		add = ignoreLines()
		content = appendLines(old, add)
	}
	op := Op{
		Action:  ActionUpdate,
		Path:    path,
		Item:    ItemGitignore,
		Detail:  "add " + strings.Join(add, ", "),
		content: content,
		mode:    mode,
	}
	if old == nil {
		op.Action = ActionCreate
	}
	return op, nil
}

// ignoreEffective reports whether the last occurrences of ignoreLines go in order
// and no later rule ignores .plait again; rules such as * are not recognised.
func ignoreEffective(b []byte) bool {
	want := ignoreLines()
	last := make([]int, len(want))
	for i := range last {
		last[i] = -1
	}
	var lines []string
	for l := range strings.Lines(string(b)) {
		l = strings.TrimSpace(l)
		if i := slices.Index(want, l); i >= 0 {
			last[i] = len(lines)
		}
		lines = append(lines, l)
	}
	for i, n := range last {
		if n < 0 || i > 0 && n <= last[i-1] {
			return false
		}
	}
	for _, l := range lines[last[0]+1:] {
		if strings.Contains(l, ".plait") && !strings.HasPrefix(l, "!") && !strings.HasPrefix(l, "#") &&
			!slices.Contains(want, l) {
			return false
		}
	}
	return true
}

func appendLines(old []byte, lines []string) []byte {
	b := slices.Clone(old)
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	return append(b, strings.Join(lines, "\n")+"\n"...)
}

func hasLine(b []byte, line string) bool {
	for l := range strings.Lines(string(b)) {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}
