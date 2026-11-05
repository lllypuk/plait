package doctor

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/install"
	"github.com/lllypuk/plait/internal/netx"
	"github.com/lllypuk/plait/internal/vcs"
)

const dotPlait = ".plait"

const caFix = "point network.ca_file in .plait/config.yaml or SSL_CERT_FILE to a PEM file " +
	"with the corporate CA certificates, then rerun plait doctor"

func agentChecks(ctx context.Context, env Env, d agent.Descriptor) []Check {
	name := "agent " + d.Name
	det, err := d.Detect(ctx)
	if err != nil {
		return []Check{errCheck(name, err)}
	}
	if !det.Installed {
		return []Check{{
			Name:   name,
			Status: StatusWarn,
			Detail: d.Cmd + " is not in PATH; its checks are skipped",
			Fix: fmt.Sprintf("install the agent, or set agents.%s.cmd in .plait/config.yaml "+
				"if it runs under another command", d.Name),
		}}
	}
	checks := []Check{
		{Name: name, Status: StatusOK, Detail: strings.TrimSpace(det.Version + " at " + det.Path)},
		skillsCheck(env, d),
		denyCheck(env, d),
		telemetryCheck(env, d),
	}
	if d.ModelEndpointEnv != "" {
		checks = append(checks, endpointCheck(env, d))
	}
	return checks
}

func scopes(env Env) []install.Scope {
	if env.Root == "" {
		return []install.Scope{install.ScopeUser}
	}
	return []install.Scope{install.ScopeUser, install.ScopeProject}
}

func installFix(d agent.Descriptor, scope install.Scope) string {
	if scope == install.ScopeProject {
		return "run plait init"
	}
	return "run plait setup --agent " + d.Name
}

// skillsCheck passes when some scope holds every skill stamped with the version of this binary.
func skillsCheck(env Env, d agent.Descriptor) Check {
	name := "skills " + d.Name
	ienv := install.Env{Home: env.Home, Root: env.Root}
	var v verdict
	var dirs []string
	installed := false
	for _, scope := range scopes(env) {
		ops, err := install.SkillPlan(d, scope, ienv, env.Version)
		if err != nil {
			return errCheck(name, err)
		}
		fix := installFix(d, scope)
		var missing, stale, foreign []string
		for _, op := range ops {
			switch op.Action {
			case install.ActionCreate:
				missing = append(missing, op.Path)
			case install.ActionUpdate:
				stale = append(stale, strings.TrimSpace(op.Path+" "+paren(op.Detail)))
			case install.ActionConflict:
				foreign = append(foreign, op.Path)
			case install.ActionSkip, install.ActionRemove, install.ActionWarn:
			}
		}
		if len(ops) > 0 {
			dirs = append(dirs, filepath.Dir(filepath.Dir(ops[0].Path)))
		}
		present := len(ops) - len(missing) - len(foreign)
		installed = installed || present > 0
		switch {
		case len(foreign) > 0:
			v.add(StatusFail, fmt.Sprintf("%s: not written by plait: %s", scope, strings.Join(foreign, ", ")),
				"move these files away, then "+fix)
		case present == 0:
			continue
		case len(stale) > 0:
			v.add(StatusWarn, fmt.Sprintf("%s: differ from plait %s: %s", scope, env.Version,
				strings.Join(stale, ", ")), fix)
		case len(missing) > 0:
			v.add(StatusWarn, fmt.Sprintf("%s: missing %s", scope, strings.Join(missing, ", ")), fix)
		default:
			v.add(StatusOK, fmt.Sprintf("%s: plait %s", scope, env.Version), "")
		}
	}
	if !installed && v.status != StatusFail {
		v.add(StatusFail, "no plait skills in "+strings.Join(dirs, " or "), installFix(d, install.ScopeUser))
	}
	return v.check(name)
}

func paren(s string) string {
	if s == "" {
		return ""
	}
	return "(" + s + ")"
}

// denyCheck passes when some scope denies every command of the descriptor.
func denyCheck(env Env, d agent.Descriptor) Check {
	name := "deny rules " + d.Name
	if d.Permissions.Writer == agent.WriterNone {
		return Check{
			Name:   name,
			Status: StatusWarn,
			Detail: "the agent has no setting to deny commands; plait approve and plait reject still require a terminal",
		}
	}
	ienv := install.Env{Home: env.Home, Root: env.Root}
	var v verdict
	var missing []string
	for _, scope := range scopes(env) {
		if (scope == install.ScopeProject && d.Permissions.Project == "") ||
			(scope == install.ScopeUser && d.Permissions.User == "") {
			continue
		}
		ops, err := install.PermissionPlan(d, scope, ienv)
		if err != nil {
			return errCheck(name, err)
		}
		if len(ops) == 1 && ops[0].Action == install.ActionSkip {
			v.add(StatusOK, fmt.Sprintf("%s: %s", scope, ops[0].Path), "")
			continue
		}
		for _, op := range ops {
			missing = append(missing, op.Path)
		}
	}
	if len(v.details) == 0 {
		v.add(StatusFail, fmt.Sprintf("%s not denied in %s", strings.Join(d.Permissions.Deny, ", "),
			strings.Join(missing, " or ")), installFix(d, install.ScopeUser))
	}
	return v.check(name)
}

// telemetryCheck passes on the first condition of telemetry_off that holds; telemetry left on is a warning.
func telemetryCheck(env Env, d agent.Descriptor) Check {
	name := "telemetry " + d.Name
	if len(d.TelemetryOff) == 0 {
		return Check{Name: name, Status: StatusWarn,
			Detail: "plait does not know how this agent turns telemetry off",
			Fix:    fmt.Sprintf("list the signs in agents.%s.telemetry_off of .plait/config.yaml", d.Name)}
	}
	ways := make([]string, 0, len(d.TelemetryOff))
	for _, c := range d.TelemetryOff {
		if c.Env != "" {
			if val := lookup(env.Sources.Environ, c.Env); truthy(val) {
				return Check{Name: name, Status: StatusOK, Detail: "off: " + c.Env + "=" + val}
			}
			ways = append(ways, c.Env+"=1 in the environment")
			continue
		}
		if c.JSON == nil {
			continue
		}
		path, held := jsonHolds(env, d, *c.JSON)
		if held {
			return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("off: %s in %s", c.JSON.Path, path)}
		}
		want := "true"
		if c.JSON.Equals != nil {
			b, _ := json.Marshal(c.JSON.Equals) // a value decoded from YAML or JSON always marshals
			want = string(b)
		}
		ways = append(ways, fmt.Sprintf("%s: %s in %s", c.JSON.Path, want, c.JSON.File))
	}
	return Check{Name: name, Status: StatusWarn, Detail: "telemetry is on",
		Fix: "turn it off with one of: " + strings.Join(ways, "; ")}
}

// jsonHolds reads the value at the dotted path; an unreadable file or a missing key does not hold.
func jsonHolds(env Env, d agent.Descriptor, c agent.JSONCondition) (string, bool) {
	path, err := install.Env{Home: env.Home, Root: env.Root}.Path(d.Name, install.ScopeUser, c.File)
	if err != nil {
		return c.File, false
	}
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return path, false
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return path, false
	}
	for key := range strings.SplitSeq(c.Path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return path, false
		}
		if v, ok = m[key]; !ok {
			return path, false
		}
	}
	if c.Equals == nil {
		switch t := v.(type) {
		case bool:
			return path, t
		case float64:
			return path, t != 0
		case string:
			return path, truthy(t)
		}
		return path, false
	}
	got, gerr := json.Marshal(v)
	want, werr := json.Marshal(c.Equals)
	return path, gerr == nil && werr == nil && bytes.Equal(got, want)
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

func endpointCheck(env Env, d agent.Descriptor) Check {
	name := "model endpoint " + d.Name
	val := lookup(env.Sources.Environ, d.ModelEndpointEnv)
	if val == "" {
		return Check{Name: name, Status: StatusOK,
			Detail: d.ModelEndpointEnv + " is unset: the agent uses its default endpoint"}
	}
	u, err := url.Parse(val)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return Check{Name: name, Status: StatusFail,
			Detail: d.ModelEndpointEnv + " is not an http(s) URL",
			Fix: fmt.Sprintf("set %s to the base URL of the model endpoint, like https://llm.example.internal/v1",
				d.ModelEndpointEnv)}
	}
	return Check{Name: name, Status: StatusOK,
		Detail: fmt.Sprintf("%s=%s (host %s)", d.ModelEndpointEnv, u.Redacted(), u.Host)}
}

func repoChecks(ctx context.Context, env Env) []Check {
	if env.Root == "" {
		return []Check{{Name: "repository", Status: StatusWarn,
			Detail: "not in a git repository; its checks are skipped",
			Fix:    "run plait doctor from the repository to check it"}}
	}
	dir := filepath.Join(env.Root, dotPlait)
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return []Check{{Name: dotPlait, Status: StatusWarn, Detail: dir + " does not exist",
			Fix: "run plait init to connect the repository"}}
	case err != nil:
		return []Check{{Name: dotPlait, Status: StatusFail, Detail: err.Error(),
			Fix: "make " + dir + " readable, then rerun plait doctor"}}
	case !info.IsDir():
		return []Check{{Name: dotPlait, Status: StatusFail, Detail: dir + " is not a directory",
			Fix: "move " + dir + " away, then run plait init"}}
	}
	return []Check{writableCheck(dir), ignoreCheck(ctx, env.Root)}
}

func writableCheck(dir string) Check {
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return Check{Name: dotPlait, Status: StatusFail, Detail: dir + " is not writable: " + err.Error(),
			Fix: "make " + dir + " writable for this user; inside an agent sandbox allow writes to it, " +
				"then rerun plait doctor"}
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return Check{Name: dotPlait, Status: StatusOK, Detail: dir + " is writable"}
}

func ignoreCheck(ctx context.Context, root string) Check {
	name := ".gitignore"
	ignored, err := vcs.Repo{Root: root}.Ignored(ctx, ".plait/runs")
	switch {
	case err != nil:
		return Check{Name: name, Status: StatusFail, Detail: err.Error(),
			Fix: "check that git works in " + root + ", then rerun plait doctor"}
	case !ignored:
		return Check{Name: name, Status: StatusFail, Detail: ".plait/runs is not ignored: runs would be committed",
			Fix: "run plait init, or add /.plait/* to .gitignore"}
	}
	return Check{Name: name, Status: StatusOK, Detail: ".plait/runs is ignored"}
}

// proxyCheck reads the variables http.ProxyFromEnvironment uses; a proxy without a scheme means http.
func proxyCheck(env Env) Check {
	var v verdict
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		val := lookup(env.Sources.Environ, key)
		if val == "" {
			continue
		}
		if strings.EqualFold(key, "NO_PROXY") {
			v.add(StatusOK, key+"="+val, "")
			continue
		}
		raw := val
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			v.add(StatusFail, key+" is not a proxy URL",
				"set "+key+" to the proxy URL, like http://proxy.example.internal:3128, or unset it")
			continue
		}
		v.add(StatusOK, key+"="+u.Redacted(), "")
	}
	if len(v.details) == 0 {
		v.add(StatusOK, "no proxy in the environment: connections go direct", "")
	}
	return v.check("proxy")
}

// caCheck needs at least one parsable certificate in network.ca_file, else in SSL_CERT_FILE.
func caCheck(env Env, configured string) Check {
	name := "ca"
	file, from := netx.CAFile(configured, env.Root, env.Sources.Environ)
	if file == "" {
		return Check{Name: name, Status: StatusOK, Detail: "system certificate pool"}
	}
	b, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		return Check{Name: name, Status: StatusFail, Detail: from + ": " + err.Error(), Fix: caFix}
	}
	n := 0
	for block, rest := pem.Decode(b); block != nil; block, rest = pem.Decode(rest) {
		if block.Type != "CERTIFICATE" {
			continue
		}
		if _, err = x509.ParseCertificate(block.Bytes); err != nil {
			return Check{Name: name, Status: StatusFail,
				Detail: fmt.Sprintf("%s %s: certificate %d is broken: %v", from, file, n+1, err), Fix: caFix}
		}
		n++
	}
	if n == 0 {
		return Check{Name: name, Status: StatusFail,
			Detail: fmt.Sprintf("%s %s holds no PEM certificate", from, file), Fix: caFix}
	}
	return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("%s %s: %d certificates", from, file, n)}
}
