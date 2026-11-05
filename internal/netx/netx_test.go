package netx_test

import (
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/lllypuk/plait/internal/netx"
	"github.com/lllypuk/plait/internal/netx/netxtest"
	"github.com/lllypuk/plait/internal/output"
)

var proxy *netxtest.Proxy //nolint:gochecknoglobals // started once in TestMain

func TestMain(m *testing.M) {
	proxy = netxtest.NewProxy()
	netxtest.UseProxy(proxy.URL)
	code := m.Run()
	proxy.Close()
	os.Exit(code)
}

func get(t *testing.T, caFile, url string) (int, error) {
	t.Helper()
	tr, err := netx.Transport(caFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tr.CloseIdleConnections)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func writeCA(t *testing.T, srv netxtest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, srv.CA, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTransportTrustsCAFile(t *testing.T) {
	srv := netxtest.NewServer(t)
	_, err := get(t, "", srv.URL)
	var verr *tls.CertificateVerificationError
	if !errors.As(err, &verr) {
		t.Fatalf("without the CA file err = %v, want a certificate verification error", err)
	}
	status, err := get(t, writeCA(t, srv), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusNoContent {
		t.Errorf("status = %d", status)
	}
}

func TestTransportUsesProxyFromEnvironment(t *testing.T) {
	srv := netxtest.NewServer(t)
	before := proxy.Tunnels()
	if _, err := get(t, writeCA(t, srv), srv.HostURL()); err != nil {
		t.Fatal(err)
	}
	if got := proxy.Tunnels() - before; got != 1 {
		t.Errorf("proxy tunnels = %d, want 1", got)
	}
}

func TestTransportTLS12(t *testing.T) {
	tr, err := netx.Transport("")
	if err != nil {
		t.Fatal(err)
	}
	if tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %x", tr.TLSClientConfig.MinVersion)
	}
}

func TestTransportBadCAFile(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(empty, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(t.TempDir(), "nope.pem"), empty} {
		_, err := netx.Transport(path)
		e := output.AsError(err)
		if output.ExitCode(err) != 2 || e.Fix == "" {
			t.Errorf("Transport(%s) = %v, want invalid with a fix", path, err)
		}
	}
}

func TestCAFile(t *testing.T) {
	const fromConfig = "network.ca_file"
	env := []string{"SSL_CERT_FILE=/old.pem", "SSL_CERT_FILE=/etc/ca.pem"}
	tests := []struct {
		name, configured, root string
		environ                []string
		file, from             string
	}{
		{name: "none"},
		{name: "environment", environ: env, file: "/etc/ca.pem", from: "SSL_CERT_FILE"},
		{name: "config wins", configured: "/ca.pem", environ: env, file: "/ca.pem", from: fromConfig},
		{
			name:       "relative to root",
			configured: "certs/ca.pem",
			root:       "/repo",
			file:       "/repo/certs/ca.pem",
			from:       fromConfig,
		},
		{name: "relative outside a repository", configured: "ca.pem", file: "ca.pem", from: fromConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, from := netx.CAFile(tt.configured, tt.root, tt.environ)
			if file != tt.file || from != tt.from {
				t.Errorf("CAFile = %q, %q, want %q, %q", file, from, tt.file, tt.from)
			}
		})
	}
}
