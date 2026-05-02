// Package installer brings up a fresh selfcloud node on first boot:
// generating a self-signed TLS pair, minting a one-time bootstrap token,
// and rendering a systemd unit so the node restarts on reboot.
package installer

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// EnsureTLS creates self-signed cert+key under dir/tls if missing.
func EnsureTLS(dir, host string) (certFile, keyFile string, err error) {
	certFile = filepath.Join(dir, "tls", "cert.pem")
	keyFile = filepath.Join(dir, "tls", "key.pem")
	if _, err := os.Stat(certFile); err == nil {
		if _, err := os.Stat(keyFile); err == nil {
			return certFile, keyFile, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(certFile), 0o750); err != nil {
		return "", "", err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "selfcloud", Organization: []string{"selfcloud"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:              []string{"localhost", "selfcloud", host},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return "", "", err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		return "", "", err
	}
	return certFile, keyFile, nil
}

// SystemdUnit renders the selfcloud systemd service file content.
type UnitParams struct {
	BinaryPath string
	DataDir    string
	APIAddr    string
	User       string
	Group      string
}

const systemdTemplate = `[Unit]
Description=selfCloud node
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
ExecStart={{.BinaryPath}} server --data-dir {{.DataDir}} --api-addr {{.APIAddr}}
Restart=on-failure
RestartSec=2s
User={{.User}}
Group={{.Group}}
LimitNOFILE=65536
NoNewPrivileges=yes
AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_ADMIN
CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_ADMIN

[Install]
WantedBy=multi-user.target
`

// RenderSystemd returns the unit contents.
func RenderSystemd(p UnitParams) string {
	if p.BinaryPath == "" {
		p.BinaryPath = "/usr/local/bin/selfcloud"
	}
	if p.DataDir == "" {
		p.DataDir = "/var/lib/selfcloud"
	}
	if p.APIAddr == "" {
		p.APIAddr = "0.0.0.0:8443"
	}
	if p.User == "" {
		p.User = "root"
	}
	if p.Group == "" {
		p.Group = p.User
	}
	out := systemdTemplate
	out = replace(out, "{{.BinaryPath}}", p.BinaryPath)
	out = replace(out, "{{.DataDir}}", p.DataDir)
	out = replace(out, "{{.APIAddr}}", p.APIAddr)
	out = replace(out, "{{.User}}", p.User)
	out = replace(out, "{{.Group}}", p.Group)
	return out
}

func replace(s, old, new string) string {
	for i := 0; ; i++ {
		idx := indexOf(s, old)
		if idx < 0 {
			return s
		}
		s = s[:idx] + new + s[idx+len(old):]
		if i > 100 {
			return s // safety
		}
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// WriteBootstrapTokenFile dumps the plaintext bootstrap token to
// dir/bootstrap-token (mode 0600). The dashboard's first-run wizard reads
// it and rotates it.
func WriteBootstrapTokenFile(dir, token string) error {
	path := filepath.Join(dir, "bootstrap-token")
	return os.WriteFile(path, []byte(fmt.Sprintf("%s\n", token)), 0o600)
}
