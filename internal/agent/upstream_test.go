package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCertPEM(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "t"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func writeKubeconfig(t *testing.T, cluster, user string) string {
	t.Helper()
	y := "current-context: c\ncontexts:\n- name: c\n  context: {cluster: k, user: u}\n" +
		"clusters:\n- name: k\n  cluster:\n    server: https://api:6443\n" + cluster +
		"users:\n- name: u\n  user:\n" + user
	p := filepath.Join(t.TempDir(), "kc")
	if err := os.WriteFile(p, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadKubeconfig(t *testing.T) {
	cert, key := testCertPEM(t)
	ca := "    certificate-authority-data: " + b64(cert) + "\n"
	tokFile := filepath.Join(t.TempDir(), "tok")
	_ = os.WriteFile(tokFile, []byte("filetok\n"), 0o600)

	tests := []struct {
		name, cluster, user, wantErr string
		check                        func(t *testing.T, u Upstream)
	}{
		{"client cert", ca, "    client-certificate-data: " + b64(cert) + "\n    client-key-data: " + b64(key) + "\n", "",
			func(t *testing.T, u Upstream) {
				if u.ClientCert == nil || u.TokenFile != "" || u.CA == nil || u.Server != "https://api:6443" {
					t.Fatalf("bad upstream %+v", u)
				}
			}},
		{"inline token", ca, "    token: abc123\n", "",
			func(t *testing.T, u Upstream) {
				got, err := os.ReadFile(u.TokenFile)
				if err != nil || string(got) != "abc123" {
					t.Fatalf("token file %q: %q %v", u.TokenFile, got, err)
				}
				if fi, _ := os.Stat(u.TokenFile); fi.Mode().Perm() != 0o600 {
					t.Fatalf("mode %v", fi.Mode())
				}
			}},
		{"token file", ca, "    tokenFile: " + tokFile + "\n", "",
			func(t *testing.T, u Upstream) {
				if u.TokenFile != tokFile {
					t.Fatalf("TokenFile = %q", u.TokenFile)
				}
			}},
		{"exec", ca, "    exec: {command: x}\n", "kubeconfig user u: exec and auth-provider are not supported", nil},
		{"auth-provider", ca, "    auth-provider: {name: gcp}\n", "kubeconfig user u: exec and auth-provider are not supported", nil},
		{"no credentials", ca, "    username: bob\n", "kubeconfig user u has no token or client certificate", nil},
		{"insecure", "    insecure-skip-tls-verify: true\n", "    token: x\n", "kubeconfig cluster k: insecure-skip-tls-verify is not supported", nil},
		{"no ca", "", "    token: x\n", "kubeconfig cluster k has no usable certificate authority", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := loadKubeconfig(writeKubeconfig(t, tt.cluster, tt.user))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, u)
		})
	}
}

func TestLoadUpstream_InCluster(t *testing.T) {
	dir := t.TempDir()
	cert, _ := testCertPEM(t)
	caFile := filepath.Join(dir, "ca.crt")
	_ = os.WriteFile(caFile, cert, 0o600)
	u, err := LoadUpstream(KubernetesConfig{Server: "https://k:443", CAFile: caFile, TokenFile: "/t"})
	if err != nil || u.CA == nil || u.TokenFile != "/t" || u.Server != "https://k:443" {
		t.Fatalf("got %+v %v", u, err)
	}
	if _, err := LoadUpstream(KubernetesConfig{Server: "https://k", CAFile: filepath.Join(dir, "nope"), TokenFile: "/t"}); err == nil {
		t.Fatal("expected error for missing ca")
	}
}

func TestOwnNamespace(t *testing.T) {
	dir := t.TempDir()
	if got := ownNamespace(filepath.Join(dir, "token")); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	_ = os.WriteFile(filepath.Join(dir, "namespace"), []byte("kbridge\n"), 0o600)
	if got := ownNamespace(filepath.Join(dir, "token")); got != "kbridge" {
		t.Fatalf("got %q", got)
	}
}
