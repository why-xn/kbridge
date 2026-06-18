package central

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

func TestConfig_ValidateTLS(t *testing.T) {
	tests := []struct {
		name    string
		tls     TLSConfig
		wantErr bool
	}{
		{"disabled", TLSConfig{Enabled: false}, false},
		{"enabled with cert+key", TLSConfig{Enabled: true, CertFile: "c", KeyFile: "k"}, false},
		{"enabled missing key", TLSConfig{Enabled: true, CertFile: "c"}, true},
		{"enabled missing both", TLSConfig{Enabled: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{TLS: tt.tls}
			if err := c.validateTLS(); (err != nil) != tt.wantErr {
				t.Errorf("validateTLS() err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestGRPCServerOptions_MissingFiles(t *testing.T) {
	if _, err := grpcServerOptions(TLSConfig{Enabled: true, CertFile: "/no/cert", KeyFile: "/no/key"}); err == nil {
		t.Fatal("expected error for missing cert/key files")
	}
	opts, err := grpcServerOptions(TLSConfig{Enabled: false})
	if err != nil || opts != nil {
		t.Errorf("disabled TLS should yield nil opts, got opts=%v err=%v", opts, err)
	}
}

// TestGRPCServer_TLSHandshake stands up the gRPC server with TLS and verifies a
// client that trusts the cert can register, while a plaintext client cannot.
func TestGRPCServer_TLSHandshake(t *testing.T) {
	certFile, keyFile := writeSelfSignedCert(t)

	store := newTestStore(t)
	seedClusterToken(t, store, "edge", "tls-token", nil)
	srvImpl := NewGRPCServer(NewAgentStore(), NewCommandQueue(), NewAgentAuthenticator(store, testPepper), NewSessionManager(10))

	opts, err := grpcServerOptions(TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile})
	if err != nil {
		t.Fatalf("server options: %v", err)
	}
	grpcSrv := grpc.NewServer(opts...)
	srvImpl.RegisterWithServer(grpcSrv)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go grpcSrv.Serve(lis)
	defer grpcSrv.Stop()
	addr := lis.Addr().String()

	// Client trusting the cert (used as its own CA) registers successfully.
	clientCreds, err := credentials.NewClientTLSFromFile(certFile, "")
	if err != nil {
		t.Fatalf("client creds: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(clientCreds), grpc.WithBlock())
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer conn.Close()

	resp, err := agentpb.NewAgentServiceClient(conn).Register(ctx, &agentpb.RegisterRequest{
		AgentToken: "tls-token", ClusterName: "edge",
	})
	if err != nil || !resp.Success {
		t.Fatalf("register over TLS failed: err=%v resp=%+v", err, resp)
	}

	// A plaintext client must NOT succeed against the TLS server.
	pctx, pcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pcancel()
	if _, err := grpc.DialContext(pctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock()); err == nil {
		t.Error("plaintext client unexpectedly connected to TLS server")
	}
}

// writeSelfSignedCert generates a self-signed cert/key valid for 127.0.0.1 and
// localhost, writes them to a temp dir, and returns their paths.
func writeSelfSignedCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "kbridge-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}
