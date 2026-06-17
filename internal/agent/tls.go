package agent

import (
	"crypto/tls"
	"fmt"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// clientTransportCredentials builds the gRPC transport credentials for the
// agent's connection to central, based on the TLS config.
func clientTransportCredentials(cfg AgentTLSConfig) (credentials.TransportCredentials, error) {
	if !cfg.Enabled {
		return insecure.NewCredentials(), nil
	}
	if cfg.Insecure {
		return credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}), nil
	}
	if cfg.CAFile != "" {
		creds, err := credentials.NewClientTLSFromFile(cfg.CAFile, "")
		if err != nil {
			return nil, fmt.Errorf("loading ca cert: %w", err)
		}
		return creds, nil
	}
	// Verify against the system root CA pool.
	return credentials.NewTLS(&tls.Config{}), nil
}
