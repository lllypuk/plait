// Package netx builds the one HTTP transport every connection of plait goes through.
package netx

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lllypuk/plait/internal/output"
)

const caFix = "point network.ca_file in .plait/config.yaml or SSL_CERT_FILE to a PEM file " +
	"with the corporate CA certificates"

// Transport proxies by the environment and trusts the system pool plus the certificates of caFile, over TLS 1.2 or later.
func Transport(caFile string) (*http.Transport, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if caFile != "" {
		b, rerr := os.ReadFile(filepath.Clean(caFile))
		if rerr != nil {
			return nil, output.Invalid("ca_unreadable", "CA file: "+rerr.Error(), caFix)
		}
		if !pool.AppendCertsFromPEM(b) {
			return nil, output.Invalid("ca_empty", "CA file "+caFile+" holds no PEM certificate", caFix)
		}
	}
	base, _ := http.DefaultTransport.(*http.Transport) // the standard library declares it so
	t := base.Clone()
	t.Proxy = http.ProxyFromEnvironment
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	return t, nil
}

// CAFile resolves network.ca_file against the repository root, else takes SSL_CERT_FILE; the second result names the source.
func CAFile(configured, root string, environ []string) (string, string) {
	if configured != "" {
		if !filepath.IsAbs(configured) && root != "" {
			configured = filepath.Join(root, configured)
		}
		return configured, "network.ca_file"
	}
	file := ""
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k == "SSL_CERT_FILE" {
			file = v
		}
	}
	if file == "" {
		return "", ""
	}
	return file, "SSL_CERT_FILE"
}
