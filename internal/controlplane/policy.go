package controlplane

import "github.com/why-xn/kbridge/internal/policy"

// The authorization domain lives in internal/policy so the CLI can evaluate a
// policy file offline without linking the control plane's HTTP, gRPC, and
// database dependencies. These aliases keep the control plane's own call sites
// and tests reading naturally.
type (
	// PolicyEngine holds the active policy and hot-reloads it on change.
	PolicyEngine = policy.Engine
	// AccessRequest describes a single kubectl action a user wants to perform.
	AccessRequest = policy.AccessRequest
	// PolicyDecision is the verdict on a single command.
	PolicyDecision = policy.Decision
)

// NewPolicyEngineFromBytes parses a policy document into a new engine that has
// no file backing it.
func NewPolicyEngineFromBytes(data []byte) (*PolicyEngine, error) {
	return policy.NewEngineFromBytes(data)
}

// NewPolicyEngineFromFile loads a policy from path into a new engine.
func NewPolicyEngineFromFile(path string) (*PolicyEngine, error) {
	return policy.NewEngineFromFile(path)
}

// parseAccessRequest derives an AccessRequest from a kubectl command.
func parseAccessRequest(cluster string, command []string, fallbackNamespace string) AccessRequest {
	return policy.ParseAccessRequest(cluster, command, fallbackNamespace)
}
