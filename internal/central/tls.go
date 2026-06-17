package central

import (
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// grpcServerOptions returns the gRPC server options for the given TLS config.
// When TLS is disabled it returns nil (plaintext).
func grpcServerOptions(cfg TLSConfig) ([]grpc.ServerOption, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	creds, err := credentials.NewServerTLSFromFile(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("loading tls credentials: %w", err)
	}
	return []grpc.ServerOption{grpc.Creds(creds)}, nil
}
