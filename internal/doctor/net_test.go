package doctor_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/lllypuk/plait/internal/doctor"
	"github.com/lllypuk/plait/internal/netx/netxtest"
)

var proxy *netxtest.Proxy //nolint:gochecknoglobals // started once in TestMain

func TestMain(m *testing.M) {
	proxy = netxtest.NewProxy()
	netxtest.UseProxy(proxy.URL)
	code := m.Run()
	proxy.Close()
	os.Exit(code)
}

// closedPort returns a loopback address nothing listens on.
func closedPort(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestNet(t *testing.T) { //nolint:funlen // one table for every outcome
	const caFix = `network\.ca_file`
	tests := []struct {
		name   string
		prep   func(f *fixture, srv netxtest.Server) (check string)
		status doctor.Status
		detail string
		fix    string
	}{
		{
			name:   "nothing to reach",
			prep:   func(*fixture, netxtest.Server) string { return "net" },
			status: doctor.StatusWarn, detail: "nothing to reach", fix: `network\.endpoints`,
		},
		{
			name: "untrusted certificate",
			prep: func(f *fixture, srv netxtest.Server) string {
				f.write(filepath.Join(f.plaitDir(), "config.yaml"), "network:\n  endpoints: ["+srv.URL+"]\n")
				return "net " + srv.URL
			},
			status: doctor.StatusFail, detail: "^certificate is not trusted: .*unknown authority", fix: caFix,
		},
		{
			name: "CA from the config",
			prep: func(f *fixture, srv netxtest.Server) string {
				f.write(filepath.Join(f.root, "ca.pem"), string(srv.CA))
				f.write(filepath.Join(f.plaitDir(), "config.yaml"),
					"network:\n  ca_file: ca.pem\n  endpoints: ["+srv.URL+"]\n")
				return "net " + srv.URL
			},
			status: doctor.StatusOK, detail: `^HTTP 204$`,
		},
		{
			name: "model endpoint with CA from the environment",
			prep: func(f *fixture, srv netxtest.Server) string {
				f.write(filepath.Join(f.home, "ca.pem"), string(srv.CA))
				f.environ = []string{
					"SSL_CERT_FILE=" + filepath.Join(f.home, "ca.pem"),
					"ANTHROPIC_BASE_URL=" + srv.URL + "/v1",
				}
				return "net " + srv.URL + "/v1"
			},
			status: doctor.StatusOK, detail: `^HTTP 204$`,
		},
		{
			name: "through the proxy",
			prep: func(f *fixture, srv netxtest.Server) string {
				f.write(filepath.Join(f.home, "ca.pem"), string(srv.CA))
				f.environ = []string{
					"SSL_CERT_FILE=" + filepath.Join(f.home, "ca.pem"),
					"OPENAI_BASE_URL=" + srv.HostURL(),
				}
				return "net " + srv.HostURL()
			},
			status: doctor.StatusOK, detail: `^HTTP 204 via proxy http://127\.0\.0\.1:\d+$`,
		},
		{
			name: "certificate for another host",
			prep: func(f *fixture, srv netxtest.Server) string {
				f.write(filepath.Join(f.home, "ca.pem"), string(srv.CA))
				u := "https://localhost:" + srv.URL[len("https://127.0.0.1:"):]
				f.environ = []string{"SSL_CERT_FILE=" + filepath.Join(f.home, "ca.pem"), "ANTHROPIC_BASE_URL=" + u}
				return "net " + u
			},
			status: doctor.StatusFail, detail: "^certificate is for another host", fix: "ANTHROPIC_BASE_URL",
		},
		{
			name: "unreachable",
			prep: func(f *fixture, _ netxtest.Server) string {
				u := "https://" + closedPort(f.t)
				f.write(filepath.Join(f.plaitDir(), "config.yaml"), "network:\n  endpoints: ["+u+"]\n")
				return "net " + u
			},
			status: doctor.StatusFail, detail: "^unreachable: .*refused", fix: `network\.endpoints.*HTTPS_PROXY`,
		},
		{
			name: "unreachable through the proxy",
			prep: func(f *fixture, _ netxtest.Server) string {
				_, port, _ := net.SplitHostPort(closedPort(f.t))
				u := "https://" + net.JoinHostPort(netxtest.Host, port)
				f.write(filepath.Join(f.plaitDir(), "config.yaml"), "network:\n  endpoints: ["+u+"]\n")
				return "net " + u
			},
			status: doctor.StatusFail, detail: `^unreachable via proxy http://127\.0\.0\.1:\d+: `, fix: "NO_PROXY",
		},
		{
			name: "not a URL",
			prep: func(f *fixture, _ netxtest.Server) string {
				f.write(filepath.Join(f.plaitDir(), "config.yaml"), "network:\n  endpoints: [gitlab]\n")
				return "net gitlab"
			},
			status: doctor.StatusFail, detail: "network.endpoints holds", fix: `^set network\.endpoints`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.net = true
			check := tt.prep(f, netxtest.NewServer(t))
			assertCheck(t, f.run(), check, tt.status, tt.detail, tt.fix, "")
		})
	}
}

func TestNetOnlyOnRequest(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.plaitDir(), "config.yaml"), "network:\n  endpoints: [https://"+closedPort(t)+"]\n")
	for _, c := range f.run() {
		if c.Name == "net" || len(c.Name) > 4 && c.Name[:4] == "net " {
			t.Errorf("check %q ran without Net", c.Name)
		}
	}
}

func TestNetDeduplicates(t *testing.T) {
	f := newFixture(t)
	f.net = true
	srv := netxtest.NewServer(t)
	f.environ = []string{"ANTHROPIC_BASE_URL=" + srv.URL, "OPENAI_BASE_URL=" + srv.URL}
	f.write(filepath.Join(f.plaitDir(), "config.yaml"), "network:\n  endpoints: ["+srv.URL+"]\n")
	n := 0
	for _, c := range f.run() {
		if c.Name == "net "+srv.URL {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d checks for one URL, want 1", n)
	}
}
