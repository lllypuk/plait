// Package doctor checks the agents, the repository and the closed network plait works in; only Env.Net reaches the network.
package doctor

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/output"
)

// Status is the outcome of a check.
type Status string

// Check statuses; only fail makes doctor exit with 4.
const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Check is one finding; Fix says what to change when Status is not ok.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// Env is what the checks read; agents are still looked up in the PATH of the process.
type Env struct {
	Version string
	Home    string
	// Root is the repository root; empty outside a repository.
	Root    string
	Sources config.Sources
	// Agents limits the agent checks to these names; empty means every known agent.
	Agents []string
	// Net also reaches the model endpoints and network.endpoints.
	Net bool
}

// Run performs every check in a stable order.
func Run(ctx context.Context, env Env) []Check {
	checks := []Check{{Name: "plait", Status: StatusOK, Detail: env.Version}}
	cfg, err := config.Load(env.Sources)
	if err != nil {
		checks = append(checks, errCheck("config", err))
	} else {
		checks = append(checks, configCheck(env.Sources))
	}
	reg, err := agent.Load(cfg.Agents)
	if err != nil {
		checks = append(checks, errCheck("agents", err))
		if reg, err = agent.Load(nil); err != nil {
			return append(checks, errCheck("agents", err))
		}
	}
	var targets []target
	for _, d := range reg.All() {
		if len(env.Agents) == 0 || slices.Contains(env.Agents, d.Name) {
			checks = append(checks, agentChecks(ctx, env, d)...)
			if u := lookup(env.Sources.Environ, d.ModelEndpointEnv); d.ModelEndpointEnv != "" && u != "" {
				targets = append(targets, target{url: u, from: d.ModelEndpointEnv})
			}
		}
	}
	checks = append(checks, repoChecks(ctx, env)...)
	checks = append(checks, proxyCheck(env), caCheck(env, cfg.Network.CAFile))
	if env.Net {
		for _, u := range cfg.Network.Endpoints {
			targets = append(targets, target{url: u, from: "network.endpoints"})
		}
		checks = append(checks, netChecks(ctx, env, cfg.Network.CAFile, targets)...)
	}
	return checks
}

// Failed returns the checks with status fail.
func Failed(checks []Check) []Check {
	var out []Check
	for _, c := range checks {
		if c.Status == StatusFail {
			out = append(out, c)
		}
	}
	return out
}

// Text renders the checks for a human: one line each, the fix under a check that is not ok, then the counts.
func Text(checks []Check) string {
	const gap = "  "
	width := 0
	for _, c := range checks {
		width = max(width, len(c.Name))
	}
	indent := strings.Repeat(" ", len(StatusFail)+len(gap)+width+len(gap))
	var b strings.Builder
	var nOK, nWarn, nFail int
	for _, c := range checks {
		switch c.Status {
		case StatusOK:
			nOK++
		case StatusWarn:
			nWarn++
		case StatusFail:
			nFail++
		}
		line := fmt.Sprintf("%-4s%s%-*s%s%s", c.Status, gap, width, c.Name, gap, c.Detail)
		b.WriteString(strings.TrimRight(line, " ") + "\n")
		if c.Fix != "" && c.Status != StatusOK {
			b.WriteString(indent + "fix: " + c.Fix + "\n")
		}
	}
	fmt.Fprintf(&b, "%d ok, %d warn, %d fail", nOK, nWarn, nFail)
	return b.String()
}

func errCheck(name string, err error) Check {
	e := output.AsError(err)
	return Check{Name: name, Status: StatusFail, Detail: e.Message, Fix: e.Fix}
}

func configCheck(s config.Sources) Check {
	var files []string
	for _, p := range []string{s.System, s.User, s.Repo} {
		if _, err := os.Stat(p); p != "" && err == nil {
			files = append(files, p)
		}
	}
	detail := "built-in defaults only"
	if len(files) > 0 {
		detail = "read " + strings.Join(files, ", ")
	}
	return Check{Name: "config", Status: StatusOK, Detail: detail}
}

// verdict folds several findings into one check: the worst status wins, details and fixes are joined.
type verdict struct {
	status  Status
	details []string
	fixes   []string
}

func (v *verdict) add(s Status, detail, fix string) {
	if rank(s) > rank(v.status) {
		v.status = s
	}
	v.details = append(v.details, detail)
	if fix != "" && !slices.Contains(v.fixes, fix) {
		v.fixes = append(v.fixes, fix)
	}
}

func (v *verdict) check(name string) Check {
	s := v.status
	if s == "" {
		s = StatusOK
	}
	return Check{Name: name, Status: s, Detail: strings.Join(v.details, "; "), Fix: strings.Join(v.fixes, "; ")}
}

// rank orders statuses from best to worst; an unset status ranks below ok.
func rank(s Status) int { return slices.Index([]Status{StatusOK, StatusWarn, StatusFail}, s) }

// lookup returns the last value of key in environ, as the process environment does.
func lookup(environ []string, key string) string {
	v := ""
	for _, kv := range environ {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v = val
		}
	}
	return v
}
