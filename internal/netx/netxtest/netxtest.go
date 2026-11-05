// Package netxtest serves HTTPS under a CA issued per test and proxies to it, without leaving the loopback.
package netxtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// Host is the name the server certificate holds; Proxy tunnels it to the loopback.
const Host = "plait.test"

// Server answers 204 under a certificate that only CA trusts.
type Server struct {
	*httptest.Server

	// CA is the PEM of the issuing certificate.
	CA []byte
}

// NewServer starts a server closed at the end of the test.
func NewServer(t testing.TB) Server {
	t.Helper()
	caKey, caDER := issue(t, nil, nil, &x509.Certificate{
		Subject:               pkix.Name{CommonName: "plait test CA"},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	})
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	key, der := issue(t, ca, caKey, &x509.Certificate{
		Subject:     pkix.Name{CommonName: Host},
		DNSNames:    []string{Host},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return Server{Server: srv, CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})}
}

// HostURL is the server URL under Host, reachable only through Proxy.
func (s Server) HostURL() string {
	_, port, _ := net.SplitHostPort(s.Listener.Addr().String())
	return "https://" + net.JoinHostPort(Host, port)
}

// issue signs tmpl by parent, or by itself when parent is nil.
func issue(
	t testing.TB,
	parent *x509.Certificate,
	parentKey *ecdsa.PrivateKey,
	tmpl *x509.Certificate,
) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl.SerialNumber = big.NewInt(time.Now().UnixNano())
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = time.Now().Add(time.Hour)
	if parent == nil {
		parent, parentKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	return key, der
}

// Proxy tunnels CONNECT to Host:port onto 127.0.0.1:port and counts the tunnels.
type Proxy struct {
	*httptest.Server

	tunnels atomic.Int64
}

// NewProxy starts a proxy; the caller closes it.
func NewProxy() *Proxy {
	p := &Proxy{}
	p.Server = httptest.NewServer(http.HandlerFunc(p.serve))
	return p
}

// Tunnels is the number of CONNECT requests served.
func (p *Proxy) Tunnels() int64 { return p.tunnels.Load() }

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
		return
	}
	p.tunnels.Add(1)
	_, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	up, err := (&net.Dialer{}).DialContext(r.Context(), "tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	conn, buf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		_ = up.Close()
		return
	}
	_, _ = conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	go func() {
		_, _ = io.Copy(up, buf)
		_ = up.Close()
	}()
	_, _ = io.Copy(conn, up)
	_ = conn.Close()
}

// UseProxy points the proxy variables of the process at proxyURL and clears the rest.
// Call it from TestMain before any request: http.ProxyFromEnvironment reads them once per process.
func UseProxy(proxyURL string) {
	for _, k := range []string{"https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		_ = os.Unsetenv(k)
	}
	_ = os.Setenv("HTTPS_PROXY", proxyURL)
}
