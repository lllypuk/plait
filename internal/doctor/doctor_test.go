package doctor_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/doctor"
	"github.com/lllypuk/plait/internal/install"
)

var update = flag.Bool( //nolint:gochecknoglobals // go test -update flag
	"update", false, "rewrite golden files")

const (
	version         = "v0.1.0"
	agentClaude     = "agent claude"
	skillsClaude    = "skills claude"
	denyClaude      = "deny rules claude"
	telemetryClaude = "telemetry claude"
	telemetryQwen   = "telemetry qwen"
	proxyCheck      = "proxy"
	configCheck     = "config"
	ignoreCheck     = ".gitignore"
	dotPlait        = ".plait"
	setupFix        = `^run plait setup --agent claude$`
)

type fixture struct {
	t       *testing.T
	home    string
	root    string
	bin     string
	environ []string
	agents  []string
	net     bool
}

// newFixture isolates PATH to fake agents and git, so the agents of the developer are never found.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not in PATH")
	}
	tmp := t.TempDir()
	f := &fixture{
		t:    t,
		home: filepath.Join(tmp, "home"),
		root: filepath.Join(tmp, "repo"),
		bin:  filepath.Join(tmp, "bin"),
	}
	for _, d := range []string{f.home, f.root, f.bin} {
		if err = os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Symlink(git, filepath.Join(f.bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", f.bin)
	cmd := exec.CommandContext(t.Context(), "git", "init", "-q", f.root) //nolint:gosec // test repository
	if out, gerr := cmd.CombinedOutput(); gerr != nil {
		t.Fatalf("git init: %v: %s", gerr, out)
	}
	return f
}

func (f *fixture) agent(name, ver string) {
	f.write(filepath.Join(f.bin, name), "#!/bin/sh\necho '"+ver+"'\n")
	if err := os.Chmod(filepath.Join(f.bin, name), 0o700); err != nil { //nolint:gosec // test executable
		f.t.Fatal(err)
	}
}

func (f *fixture) write(path, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) install(name string, scope install.Scope, ver string) {
	f.t.Helper()
	reg, err := agent.Load(nil)
	if err != nil {
		f.t.Fatal(err)
	}
	d, _ := reg.Get(name)
	ops, err := install.Plan(d, scope, install.Env{Home: f.home, Root: f.root}, ver, false)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = install.Apply(ops); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) plaitDir() string { return filepath.Join(f.root, ".plait") }

func (f *fixture) run() []doctor.Check {
	return doctor.Run(f.t.Context(), doctor.Env{
		Version: version,
		Home:    f.home,
		Root:    f.root,
		Sources: config.Sources{Repo: filepath.Join(f.plaitDir(), "config.yaml"), Environ: f.environ},
		Agents:  f.agents,
		Net:     f.net,
	})
}

func TestChecks(t *testing.T) { //nolint:funlen // one table for every check
	tests := []struct {
		name   string
		prep   func(f *fixture)
		check  string
		status doctor.Status
		detail string
		fix    string
		absent string
	}{
		{name: "plait version", check: "plait", status: doctor.StatusOK, detail: `^v0\.1\.0$`},
		{name: "config defaults", check: configCheck, status: doctor.StatusOK, detail: "built-in defaults only"},
		{
			name:  "config from the repository",
			prep:  func(f *fixture) { f.write(filepath.Join(f.plaitDir(), "config.yaml"), "review:\n  base: main\n") },
			check: configCheck, status: doctor.StatusOK, detail: `read .*/\.plait/config\.yaml$`,
		},
		{
			name:  "config broken",
			prep:  func(f *fixture) { f.write(filepath.Join(f.plaitDir(), "config.yaml"), "review: [\n") },
			check: configCheck, status: doctor.StatusFail, detail: `config .*config\.yaml`, fix: `^fix .*config\.yaml`,
		},
		{
			name: "agents broken",
			prep: func(f *fixture) {
				f.write(filepath.Join(f.plaitDir(), "config.yaml"), "agents:\n  mine:\n    base: nope\n")
			},
			check:  "agents",
			status: doctor.StatusFail,
			detail: `agents\.mine: unknown base "nope"`,
			fix:    `agents\.mine`,
		},
		{
			name:  "agent not in PATH",
			check: agentClaude, status: doctor.StatusWarn, detail: "claude is not in PATH", fix: `agents\.claude\.cmd`,
			absent: skillsClaude,
		},
		{
			name:  "agent version",
			prep:  func(f *fixture) { f.agent("claude", "2.1.0 (Claude Code)") },
			check: agentClaude, status: doctor.StatusOK, detail: `^2\.1\.0 \(Claude Code\) at .*/bin/claude$`,
		},
		{
			name:  "agents filter",
			prep:  func(f *fixture) { f.agents = []string{"qwen"} },
			check: "agent qwen", status: doctor.StatusWarn,
			absent: agentClaude,
		},
		{
			name:  "skills missing",
			prep:  func(f *fixture) { f.agent("claude", "2.1.0") },
			check: skillsClaude, status: doctor.StatusFail,
			detail: `no plait skills in .*/home/\.claude/skills or .*/repo/\.claude/skills$`,
			fix:    setupFix,
		},
		{
			name: "skills in user scope",
			prep: func(f *fixture) {
				f.agent("claude", "2.1.0")
				f.install("claude", install.ScopeUser, version)
			},
			check: skillsClaude, status: doctor.StatusOK, detail: `^user: plait v0\.1\.0$`,
		},
		{
			name: "skills in project scope",
			prep: func(f *fixture) {
				f.agent("claude", "2.1.0")
				f.install("claude", install.ScopeProject, version)
			},
			check: skillsClaude, status: doctor.StatusOK, detail: `^project: plait v0\.1\.0$`,
		},
		{
			name: "skills stale",
			prep: func(f *fixture) {
				f.agent("claude", "2.1.0")
				f.install("claude", install.ScopeUser, "v0.0.9")
			},
			check: skillsClaude, status: doctor.StatusWarn,
			detail: `^user: differ from plait v0\.1\.0: .*plait/SKILL\.md \(plait-version v0\.0\.9 -> v0\.1\.0\)`,
			fix:    setupFix,
		},
		{
			name: "skills stale in project",
			prep: func(f *fixture) {
				f.agent("claude", "2.1.0")
				f.install("claude", install.ScopeUser, version)
				f.install("claude", install.ScopeProject, "v0.0.9")
			},
			check: skillsClaude, status: doctor.StatusWarn, detail: `^user: plait v0\.1\.0; project: differ`,
			fix: `^run plait init$`,
		},
		{
			name: "skill missing from a scope",
			prep: func(f *fixture) {
				f.agent("claude", "2.1.0")
				f.install("claude", install.ScopeUser, version)
				if err := os.Remove(filepath.Join(f.home, ".claude/skills/plait-mr/SKILL.md")); err != nil {
					f.t.Fatal(err)
				}
			},
			check: skillsClaude, status: doctor.StatusWarn, detail: `^user: missing .*plait-mr/SKILL\.md$`,
		},
		{
			name: "skill file not written by plait",
			prep: func(f *fixture) {
				f.agent("claude", "2.1.0")
				f.write(filepath.Join(f.home, ".claude/skills/plait/SKILL.md"), "my own\n")
			},
			check: skillsClaude, status: doctor.StatusFail, detail: `^user: not written by plait: .*plait/SKILL\.md$`,
			fix: `^move these files away, then run plait setup --agent claude$`,
		},
		{
			name:  "deny rules missing",
			prep:  func(f *fixture) { f.agent("claude", "2.1.0") },
			check: denyClaude, status: doctor.StatusFail,
			detail: `^plait approve, plait reject not denied in .*/home/\.claude/settings\.json or .*/repo/`,
			fix:    setupFix,
		},
		{
			name: "deny rules installed",
			prep: func(f *fixture) {
				f.agent("qwen", "0.9.1")
				f.install("qwen", install.ScopeProject, version)
			},
			check: "deny rules qwen", status: doctor.StatusOK, detail: `^project: .*/repo/\.qwen/settings\.json$`,
		},
		{
			name: "deny rules settings broken",
			prep: func(f *fixture) {
				f.agent("claude", "2.1.0")
				f.write(filepath.Join(f.home, ".claude/settings.json"), "{")
			},
			check: denyClaude, status: doctor.StatusFail, detail: `settings\.json is not valid JSON`,
			fix: `by hand`,
		},
		{
			name: "deny rules not supported",
			prep: func(f *fixture) {
				f.agent("claude", "2.1.0")
				f.write(filepath.Join(f.plaitDir(), "config.yaml"),
					"agents:\n  claude:\n    permissions: { writer: none }\n")
			},
			check: denyClaude, status: doctor.StatusWarn, detail: "no setting to deny commands",
		},
		{
			name:   "telemetry on",
			prep:   func(f *fixture) { f.agent("claude", "2.1.0") },
			check:  telemetryClaude,
			status: doctor.StatusWarn,
			detail: "^telemetry is on$",
			fix:    `^turn it off with one of: DISABLE_TELEMETRY=1 in the environment; .*env\.DISABLE_TELEMETRY: true in ~/\.claude/settings\.json`,
		},
		{
			name: "telemetry off by env",
			prep: func(f *fixture) {
				f.agent("claude", "2.1.0")
				f.environ = []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			},
			check:  telemetryClaude,
			status: doctor.StatusOK,
			detail: "^off: CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1$",
		},
		{
			name: "telemetry env set to zero",
			prep: func(f *fixture) {
				f.agent("claude", "2.1.0")
				f.environ = []string{"DISABLE_TELEMETRY=0"}
			},
			check: telemetryClaude, status: doctor.StatusWarn,
		},
		{
			name: "telemetry off in settings",
			prep: func(f *fixture) {
				f.agent("claude", "2.1.0")
				f.write(filepath.Join(f.home, ".claude/settings.json"), `{"env": {"DISABLE_TELEMETRY": "1"}}`)
			},
			check:  telemetryClaude,
			status: doctor.StatusOK,
			detail: `^off: env\.DISABLE_TELEMETRY in .*/home/\.claude/settings\.json$`,
		},
		{
			name: "telemetry off by equals",
			prep: func(f *fixture) {
				f.agent("qwen", "0.9.1")
				f.write(filepath.Join(f.home, ".qwen/settings.json"), `{"privacy": {"usageStatisticsEnabled": false}}`)
			},
			check: telemetryQwen, status: doctor.StatusOK, detail: `^off: privacy\.usageStatisticsEnabled in `,
		},
		{
			name: "telemetry on by equals",
			prep: func(f *fixture) {
				f.agent("qwen", "0.9.1")
				f.write(filepath.Join(f.home, ".qwen/settings.json"), `{"privacy": {"usageStatisticsEnabled": true}}`)
			},
			check:  telemetryQwen,
			status: doctor.StatusWarn,
			fix:    `privacy\.usageStatisticsEnabled: false in ~/\.qwen/`,
		},
		{
			name: "telemetry on by env over settings",
			prep: func(f *fixture) {
				f.agent("qwen", "0.9.1")
				f.write(filepath.Join(f.home, ".qwen/settings.json"), `{"privacy": {"usageStatisticsEnabled": false}}`)
				f.environ = []string{"QWEN_USAGE_STATISTICS_ENABLED=true"}
			},
			check:  telemetryQwen,
			status: doctor.StatusWarn,
			fix:    `QWEN_USAGE_STATISTICS_ENABLED=false in the environment`,
		},
		{
			name: "telemetry off by env over settings",
			prep: func(f *fixture) {
				f.agent("qwen", "0.9.1")
				f.write(filepath.Join(f.home, ".qwen/settings.json"), `{"privacy": {"usageStatisticsEnabled": true}}`)
				f.environ = []string{"QWEN_USAGE_STATISTICS_ENABLED=0"}
			},
			check: telemetryQwen, status: doctor.StatusOK, detail: `^off: QWEN_USAGE_STATISTICS_ENABLED=0$`,
		},
		{
			name: "telemetry off by env Qwen does not parse as true",
			prep: func(f *fixture) {
				f.agent("qwen", "0.9.1")
				f.write(filepath.Join(f.home, ".qwen/settings.json"), `{"privacy": {"usageStatisticsEnabled": true}}`)
				f.environ = []string{"QWEN_USAGE_STATISTICS_ENABLED=TRUE"}
			},
			check: telemetryQwen, status: doctor.StatusOK, detail: `^off: QWEN_USAGE_STATISTICS_ENABLED=TRUE$`,
		},
		{
			name: "telemetry off by empty env",
			prep: func(f *fixture) {
				f.agent("qwen", "0.9.1")
				f.write(filepath.Join(f.home, ".qwen/settings.json"), `{"privacy": {"usageStatisticsEnabled": true}}`)
				f.environ = []string{"QWEN_USAGE_STATISTICS_ENABLED="}
			},
			check: telemetryQwen, status: doctor.StatusOK, detail: `^off: QWEN_USAGE_STATISTICS_ENABLED=$`,
		},
		{
			name: "telemetry signs unknown",
			prep: func(f *fixture) {
				f.agent("claude", "2.1.0")
				f.write(filepath.Join(f.plaitDir(), "config.yaml"), "agents:\n  claude:\n    telemetry_off: []\n")
			},
			check: telemetryClaude, status: doctor.StatusWarn, fix: `agents\.claude\.telemetry_off`,
		},
		{
			name:  "model endpoint unset",
			prep:  func(f *fixture) { f.agent("claude", "2.1.0") },
			check: "model endpoint claude", status: doctor.StatusOK, detail: "^ANTHROPIC_BASE_URL is unset",
		},
		{
			name: "model endpoint set",
			prep: func(f *fixture) {
				f.agent("qwen", "0.9.1")
				f.environ = []string{"OPENAI_BASE_URL=https://bot:secret@llm.example.internal:8443/v1"}
			},
			check:  "model endpoint qwen",
			status: doctor.StatusOK,
			detail: `^OPENAI_BASE_URL=https://bot:xxxxx@llm\.example\.internal:8443/v1 \(host llm\.example\.internal:8443\)$`,
		},
		{
			name: "model endpoint broken",
			prep: func(f *fixture) {
				f.agent("qwen", "0.9.1")
				f.environ = []string{"OPENAI_BASE_URL=llm.example.internal"}
			},
			check: "model endpoint qwen", status: doctor.StatusFail, fix: "^set OPENAI_BASE_URL to the base URL",
		},
		{
			name:  "not in a repository",
			prep:  func(f *fixture) { f.root = "" },
			check: "repository", status: doctor.StatusWarn, fix: "from the repository",
			absent: dotPlait,
		},
		{
			name: ".plait missing", check: dotPlait, status: doctor.StatusWarn, fix: "^run plait init",
			absent: ignoreCheck,
		},
		{
			name:  ".plait writable",
			prep:  func(f *fixture) { f.write(filepath.Join(f.plaitDir(), "config.yaml"), "") },
			check: dotPlait, status: doctor.StatusOK, detail: "is writable$",
		},
		{
			name: ".plait read-only",
			prep: func(f *fixture) {
				if os.Geteuid() == 0 {
					f.t.Skip("root writes to a read-only directory")
				}
				f.write(filepath.Join(f.plaitDir(), "config.yaml"), "")
				if err := os.Chmod(f.plaitDir(), 0o500); err != nil { //nolint:gosec // read-only directory under test
					f.t.Fatal(err)
				}
				f.t.Cleanup(func() { _ = os.Chmod(f.plaitDir(), 0o700) }) //nolint:gosec // restore for TempDir cleanup
			},
			check: dotPlait, status: doctor.StatusFail, detail: "is not writable", fix: "sandbox allow writes",
		},
		{
			name:  "runs not ignored",
			prep:  func(f *fixture) { f.write(filepath.Join(f.plaitDir(), "config.yaml"), "") },
			check: ignoreCheck, status: doctor.StatusFail, detail: "runs would be committed", fix: `/\.plait/\*`,
		},
		{
			name: "runs ignored",
			prep: func(f *fixture) {
				f.write(filepath.Join(f.plaitDir(), "config.yaml"), "")
				f.write(filepath.Join(f.root, ignoreCheck), "/.plait/*\n!/.plait/config.yaml\n")
			},
			check: ignoreCheck, status: doctor.StatusOK,
		},
		{name: "no proxy", check: proxyCheck, status: doctor.StatusOK, detail: "^no proxy"},
		{
			name: "proxy set",
			prep: func(f *fixture) {
				f.environ = []string{"HTTPS_PROXY=http://me:secret@proxy.example.internal:3128", "no_proxy=.internal"}
			},
			check: proxyCheck, status: doctor.StatusOK,
			detail: `^HTTPS_PROXY=http://me:xxxxx@proxy\.example\.internal:3128; no_proxy=\.internal$`,
		},
		{
			name:  "proxy without a scheme",
			prep:  func(f *fixture) { f.environ = []string{"http_proxy=proxy.example.internal:3128"} },
			check: proxyCheck, status: doctor.StatusOK, detail: `^http_proxy=http://proxy\.example\.internal:3128$`,
		},
		{
			name:  "proxy broken",
			prep:  func(f *fixture) { f.environ = []string{"HTTPS_PROXY=http://[::1"} },
			check: proxyCheck, status: doctor.StatusFail, fix: "^set HTTPS_PROXY to the proxy URL",
		},
		{name: "system CA", check: "ca", status: doctor.StatusOK, detail: "^system certificate pool$"},
		{
			name:  "CA file missing",
			prep:  func(f *fixture) { f.environ = []string{"SSL_CERT_FILE=" + filepath.Join(f.home, "nope.pem")} },
			check: "ca", status: doctor.StatusFail, detail: "^SSL_CERT_FILE: ", fix: `network\.ca_file`,
		},
		{
			name: "CA file empty",
			prep: func(f *fixture) {
				f.write(filepath.Join(f.home, "ca.pem"), "")
				f.environ = []string{"SSL_CERT_FILE=" + filepath.Join(f.home, "ca.pem")}
			},
			check: "ca", status: doctor.StatusFail, detail: "holds no PEM certificate$", fix: `network\.ca_file`,
		},
		{
			name: "CA file broken",
			prep: func(f *fixture) {
				f.write(
					filepath.Join(f.home, "ca.pem"),
					"-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n",
				)
				f.environ = []string{"SSL_CERT_FILE=" + filepath.Join(f.home, "ca.pem")}
			},
			check: "ca", status: doctor.StatusFail, detail: "certificate 1 is broken",
		},
		{
			name: "CA file from the environment",
			prep: func(f *fixture) {
				f.write(filepath.Join(f.home, "ca.pem"), selfSigned(f.t))
				f.environ = []string{"SSL_CERT_FILE=" + filepath.Join(f.home, "ca.pem")}
			},
			check: "ca", status: doctor.StatusOK, detail: `^SSL_CERT_FILE .*/home/ca\.pem: 1 certificates$`,
		},
		{
			name: "CA file from the config wins",
			prep: func(f *fixture) {
				f.write(filepath.Join(f.root, "certs/ca.pem"), selfSigned(f.t)+selfSigned(f.t))
				f.write(filepath.Join(f.plaitDir(), "config.yaml"), "network:\n  ca_file: certs/ca.pem\n")
				f.environ = []string{"SSL_CERT_FILE=" + filepath.Join(f.home, "nope.pem")}
			},
			check: "ca", status: doctor.StatusOK, detail: `^network\.ca_file .*/repo/certs/ca\.pem: 2 certificates$`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if tt.prep != nil {
				tt.prep(f)
			}
			assertCheck(t, f.run(), tt.check, tt.status, tt.detail, tt.fix, tt.absent)
		})
	}
}

func assertCheck(t *testing.T, checks []doctor.Check, name string, status doctor.Status, detail, fix, absent string) {
	t.Helper()
	c, ok := find(checks, name)
	if !ok {
		t.Fatalf("no check %q in\n%s", name, doctor.Text(checks))
	}
	if c.Status != status {
		t.Errorf("status = %s, want %s: %+v", c.Status, status, c)
	}
	if status == doctor.StatusFail && c.Fix == "" {
		t.Errorf("a failed check has no fix: %+v", c)
	}
	for _, m := range []struct{ field, got, want string }{{"detail", c.Detail, detail}, {"fix", c.Fix, fix}} {
		if m.want != "" && !regexp.MustCompile(m.want).MatchString(m.got) {
			t.Errorf("%s = %q, want match %s", m.field, m.got, m.want)
		}
	}
	if _, has := find(checks, absent); absent != "" && has {
		t.Errorf("check %q should be absent", absent)
	}
}

func find(checks []doctor.Check, name string) (doctor.Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return doctor.Check{}, false
}

func TestFailed(t *testing.T) {
	checks := []doctor.Check{
		{
			Name:   "a",
			Status: doctor.StatusOK,
		},
		{Name: "b", Status: doctor.StatusFail},
		{Name: "c", Status: doctor.StatusWarn},
	}
	got := doctor.Failed(checks)
	if len(got) != 1 || got[0].Name != "b" {
		t.Errorf("Failed = %+v", got)
	}
}

func TestText(t *testing.T) {
	checks := []doctor.Check{
		{Name: "plait", Status: doctor.StatusOK, Detail: "v0.1.0"},
		{Name: agentClaude, Status: doctor.StatusOK, Detail: "2.1.0 (Claude Code) at /usr/bin/claude"},
		{Name: skillsClaude, Status: doctor.StatusFail, Detail: "no plait skills in /home/dev/.claude/skills",
			Fix: "run plait setup --agent claude"},
		{Name: telemetryClaude, Status: doctor.StatusWarn, Detail: "telemetry is on",
			Fix: "turn it off with one of: DISABLE_TELEMETRY=1 in the environment"},
		{Name: "ca", Status: doctor.StatusOK, Detail: "system certificate pool", Fix: "ignored when ok"},
		{Name: "empty", Status: doctor.StatusOK},
	}
	got := []byte(doctor.Text(checks))
	path := filepath.Join("testdata", "text.golden")
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs (rerun with -update):\n%s", path, got)
	}
}

func selfSigned(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
