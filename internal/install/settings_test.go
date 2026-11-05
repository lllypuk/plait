package install_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/install"
	"github.com/lllypuk/plait/internal/output"
)

func cmds() []string { return []string{"plait approve", "plait reject"} }

func writer(t *testing.T, name string) install.PermissionWriter {
	t.Helper()
	w, ok := install.Writer(name)
	if !ok {
		t.Fatalf("no writer %s", name)
	}
	return w
}

func settingsFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func single(t *testing.T, ops []install.Op, err error, want install.Action) install.Op {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Action != want {
		t.Fatalf("ops = %+v, want one %s", ops, want)
	}
	return ops[0]
}

func TestSettingsRules(t *testing.T) {
	tests := []struct{ writer, want string }{
		{agent.WriterClaudeSettings, "{\n  \"permissions\": {\n    \"deny\": [\n" +
			"      \"Bash(plait approve:*)\",\n      \"Bash(plait reject:*)\"\n    ]\n  }\n}\n"},
		{agent.WriterQwenSettings, "{\n  \"permissions\": {\n    \"deny\": [\n" +
			"      \"Bash(plait approve *)\",\n      \"Bash(plait reject *)\"\n    ]\n  }\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.writer, func(t *testing.T) {
			path := settingsFile(t, "", 0)
			ops, err := writer(t, tt.writer).Plan(path, cmds())
			single(t, ops, err, install.ActionCreate)
			apply(t, ops)
			assertFile(t, path, []byte(tt.want))
		})
	}
}

const userSettings = `{
    "model": "x",
    "env": {"B": "1", "A": "a && b <c>"},
    "permissions": {
        "allow": ["Bash(go test:*)"],
        "deny": ["Bash(rm:*)", "Bash(plait reject:*)", [1]],
        "defaultMode": "plan"
    },
    "n": 1.50
}`

func TestSettingsKeepsUserKeys(t *testing.T) {
	path := settingsFile(t, userSettings, 0o600)
	w := writer(t, agent.WriterClaudeSettings)
	ops, err := w.Plan(path, cmds())
	op := single(t, ops, err, install.ActionUpdate)
	if op.Detail != "deny Bash(plait approve:*)" {
		t.Fatalf("detail = %q", op.Detail)
	}
	apply(t, ops)
	assertFile(t, path, []byte(`{
  "model": "x",
  "env": {
    "B": "1",
    "A": "a && b <c>"
  },
  "permissions": {
    "allow": [
      "Bash(go test:*)"
    ],
    "deny": [
      "Bash(rm:*)",
      "Bash(plait reject:*)",
      [
        1
      ],
      "Bash(plait approve:*)"
    ],
    "defaultMode": "plan"
  },
  "n": 1.50
}
`))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want the user's 0600", info.Mode().Perm())
	}

	ops, err = w.Plan(path, cmds())
	single(t, ops, err, install.ActionSkip)

	ops, err = w.Remove(path, cmds())
	single(t, ops, err, install.ActionUpdate)
	apply(t, ops)
	assertFile(t, path, []byte(`{
  "model": "x",
  "env": {
    "B": "1",
    "A": "a && b <c>"
  },
  "permissions": {
    "allow": [
      "Bash(go test:*)"
    ],
    "deny": [
      "Bash(rm:*)",
      [
        1
      ]
    ],
    "defaultMode": "plan"
  },
  "n": 1.50
}
`))
}

func TestSettingsRemove(t *testing.T) {
	w := writer(t, agent.WriterClaudeSettings)
	t.Run("only plait rules", func(t *testing.T) {
		path := settingsFile(t, `{"a": 1, "permissions": {"deny": ["Bash(plait approve:*)"]}}`, 0o644)
		ops, err := w.Remove(path, cmds())
		single(t, ops, err, install.ActionUpdate)
		apply(t, ops)
		assertFile(t, path, []byte("{\n  \"a\": 1\n}\n"))
	})
	for name, content := range map[string]string{
		"missing file": "",
		"no rules":     `{"permissions": {"deny": ["Bash(rm:*)"]}}`,
		"no deny":      `{"permissions": {}}`,
		"no perms":     `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := settingsFile(t, content, 0o644)
			ops, err := w.Remove(path, cmds())
			single(t, ops, err, install.ActionSkip)
		})
	}
}

func TestSettingsInvalid(t *testing.T) {
	for name, content := range map[string]string{
		"broken json":      `{"permissions": `,
		"trailing data":    `{} {}`,
		"not an object":    `[]`,
		"perms not object": `{"permissions": []}`,
		"deny not array":   `{"permissions": {"deny": "Bash(x)"}}`,
		"duplicate brace":  `{"a": 1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := settingsFile(t, content, 0o644)
			w := writer(t, agent.WriterClaudeSettings)
			_, err := w.Plan(path, cmds())
			e := output.AsError(err)
			if e == nil || e.Code != "settings_invalid" || e.ExitCode() != output.ExitInvalid {
				t.Fatalf("err = %v, want settings_invalid", err)
			}
			if e.Fix == "" || !containsPath(e.Message, path) {
				t.Fatalf("error = %q, fix = %q", e.Message, e.Fix)
			}
			if name == "deny not array" || name == "perms not object" {
				return // Remove finds no plait rules there and leaves the file alone
			}
			if _, err = w.Remove(path, cmds()); output.AsError(err) == nil {
				t.Fatal("Remove accepted an invalid file")
			}
			assertFile(t, path, []byte(content))
		})
	}
}

func containsPath(msg, path string) bool { return len(msg) >= len(path) && msg[:len(path)] == path }

func TestSettingsEmptyFile(t *testing.T) {
	path := settingsFile(t, "\n", 0o600)
	ops, err := writer(t, agent.WriterClaudeSettings).Plan(path, cmds())
	single(t, ops, err, install.ActionUpdate)
	apply(t, ops)
	assertFile(t, path, []byte(`{
  "permissions": {
    "deny": [
      "Bash(plait approve:*)",
      "Bash(plait reject:*)"
    ]
  }
}
`))
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", info, err)
	}
}

func TestSettingsSymlinkKept(t *testing.T) {
	target := settingsFile(t, `{"a": 1}`, 0o644)
	link := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	ops, err := writer(t, agent.WriterClaudeSettings).Plan(link, cmds())
	single(t, ops, err, install.ActionUpdate)
	apply(t, ops)
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("settings.json is no longer a symlink: %v", err)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Bash(plait approve:*)") {
		t.Fatalf("target = %s", b)
	}
}
