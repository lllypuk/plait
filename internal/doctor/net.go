package doctor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/lllypuk/plait/internal/netx"
)

const netTimeout = 5 * time.Second

// target is a URL to reach; from names where it was configured.
type target struct {
	url  string
	from string
}

// netChecks reaches every distinct target at once through netx; any HTTP response means the path works.
func netChecks(ctx context.Context, env Env, caFile string, targets []target) []Check {
	var uniq []target
	for _, t := range targets {
		if !slices.ContainsFunc(uniq, func(u target) bool { return u.url == t.url }) {
			uniq = append(uniq, t)
		}
	}
	if len(uniq) == 0 {
		return []Check{{Name: "net", Status: StatusWarn,
			Detail: "nothing to reach: no model endpoint in the environment and network.endpoints is empty",
			Fix:    "list the forge and model URLs in network.endpoints of .plait/config.yaml"}}
	}
	file, _ := netx.CAFile(caFile, env.Root, env.Sources.Environ)
	tr, err := netx.Transport(file)
	if err != nil {
		return []Check{errCheck("net", err)}
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport:     tr,
		Timeout:       netTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	checks := make([]Check, len(uniq))
	var wg sync.WaitGroup
	for i, t := range uniq {
		wg.Go(func() { checks[i] = reach(ctx, client, tr, t) })
	}
	wg.Wait()
	return checks
}

func reach(ctx context.Context, client *http.Client, tr *http.Transport, t target) Check {
	u, err := url.Parse(t.url)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return Check{
			Name:   "net " + t.url,
			Status: StatusFail,
			Detail: t.from + " holds a value that is not an http(s) URL",
			Fix:    "set " + t.from + " to a URL like https://host.example.internal",
		}
	}
	name := "net " + u.Redacted()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	if err != nil {
		return Check{Name: name, Status: StatusFail, Detail: err.Error(), Fix: "fix the URL in " + t.from}
	}
	via := ""
	if p, perr := tr.Proxy(req); perr == nil && p != nil {
		via = " via proxy " + p.Redacted()
	}
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("HTTP %d%s", resp.StatusCode, via)}
	}
	if ue := (*url.Error)(nil); errors.As(err, &ue) {
		err = ue.Err // the URL is already in the name, without credentials
	}
	var host x509.HostnameError
	var verify *tls.CertificateVerificationError
	switch {
	case errors.As(err, &host):
		return Check{Name: name, Status: StatusFail, Detail: "certificate is for another host: " + err.Error(),
			Fix: "check the host in " + t.from + " against the certificate of the server"}
	case errors.As(err, &verify):
		return Check{Name: name, Status: StatusFail, Detail: "certificate is not trusted: " + err.Error(),
			Fix: caFix}
	case via != "":
		return Check{Name: name, Status: StatusFail, Detail: "unreachable" + via + ": " + err.Error(),
			Fix: fmt.Sprintf("check that the proxy lets %s through, or add it to NO_PROXY if it is inside the network",
				u.Hostname())}
	}
	return Check{Name: name, Status: StatusFail, Detail: "unreachable: " + err.Error(),
		Fix: fmt.Sprintf("check the URL in %s and that %s resolves and listens; behind a proxy set HTTPS_PROXY",
			t.from, u.Hostname())}
}
