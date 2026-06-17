package agent

import (
	"testing"
)

func TestClientTransportCredentials(t *testing.T) {
	t.Run("disabled returns plaintext creds", func(t *testing.T) {
		c, err := clientTransportCredentials(AgentTLSConfig{Enabled: false})
		if err != nil || c == nil {
			t.Fatalf("want creds, got c=%v err=%v", c, err)
		}
		if c.Info().SecurityProtocol != "insecure" {
			t.Errorf("want insecure transport, got %q", c.Info().SecurityProtocol)
		}
	})

	t.Run("enabled+insecure returns tls creds", func(t *testing.T) {
		c, err := clientTransportCredentials(AgentTLSConfig{Enabled: true, Insecure: true})
		if err != nil || c == nil {
			t.Fatalf("want creds, got c=%v err=%v", c, err)
		}
		if c.Info().SecurityProtocol != "tls" {
			t.Errorf("want tls transport, got %q", c.Info().SecurityProtocol)
		}
	})

	t.Run("enabled with missing ca file errors", func(t *testing.T) {
		_, err := clientTransportCredentials(AgentTLSConfig{Enabled: true, CAFile: "/no/such/ca.pem"})
		if err == nil {
			t.Fatal("expected error for missing ca file")
		}
	})

	t.Run("enabled with empty ca uses system roots", func(t *testing.T) {
		c, err := clientTransportCredentials(AgentTLSConfig{Enabled: true})
		if err != nil || c == nil {
			t.Fatalf("want creds, got c=%v err=%v", c, err)
		}
		if c.Info().SecurityProtocol != "tls" {
			t.Errorf("want tls transport, got %q", c.Info().SecurityProtocol)
		}
	})
}
