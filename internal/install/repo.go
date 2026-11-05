package install

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// ignoreLines hide runs but keep the files the team commits; git cannot re-include them under an ignored directory.
func ignoreLines() []string { return []string{"/.plait/*", "!/" + ConfigFile, "!/.plait/rules.md"} }

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
	ops := []Op{cfg}
	// A derived agent shares paths with its base; the first agent owns the file.
	seen := map[string]bool{}
	add := func(op Op) {
		key := op.Item + "\x00" + op.Path
		if op.Path == "" || !seen[key] {
			seen[key] = true
			ops = append(ops, op)
		}
	}
	for _, d := range agents {
		aops, planErr := Plan(d, ScopeProject, env, version, false)
		if planErr != nil {
			return nil, planErr
		}
		for _, op := range aops {
			add(op)
		}
	}
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
		add(op)
	}
	ignore, err := ignoreOp(filepath.Join(env.Root, ignoreFile))
	if err != nil {
		return nil, err
	}
	return append(ops, ignore), nil
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
	var missing []string
	for _, l := range ignoreLines() {
		if !hasLine(old, l) {
			missing = append(missing, l)
		}
	}
	if len(missing) == 0 {
		return Op{Action: ActionSkip, Path: path, Item: ItemGitignore}, nil
	}
	op := Op{
		Action: ActionUpdate,
		Path:   path,
		Item:   ItemGitignore,
		Detail: "add " + strings.Join(missing, ", "),
		mode:   mode,
	}
	if old == nil {
		op.Action = ActionCreate
	}
	op.content = old
	if len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
		op.content = append(op.content, '\n')
	}
	op.content = append(op.content, strings.Join(missing, "\n")+"\n"...)
	return op, nil
}

func hasLine(b []byte, line string) bool {
	for l := range strings.Lines(string(b)) {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}
