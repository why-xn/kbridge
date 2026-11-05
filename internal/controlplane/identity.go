package controlplane

import (
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/why-xn/kbridge/api/proto/agentpb"
)

// capImpersonation is advertised by agents that run each command as the requesting user.
const capImpersonation = "impersonation"

// impersonatedUser is the Kubernetes user the agent runs a command as. An
// unknown caller maps to "" so the agent refuses rather than impersonating "kbridge:".
func impersonatedUser(email string) string {
	if email == "" {
		return ""
	}
	return "kbridge:" + email
}

// identityFor builds the identity sent with every command, stream and port-forward.
func identityFor(email string) *agentpb.Identity {
	return &agentpb.Identity{User: impersonatedUser(email)}
}

// agentSupports reports whether the agent advertised the capability at registration.
func agentSupports(a *AgentInfo, cap string) bool {
	return slices.Contains(a.Capabilities, cap)
}

// requireImpersonation writes a 503 and returns false when the cluster's agent
// would run the command as itself rather than as the requesting user.
func requireImpersonation(c *gin.Context, a *AgentInfo) bool {
	if agentSupports(a, capImpersonation) {
		return true
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error": "agent does not support impersonation: upgrade the agent for cluster " + a.ClusterName,
	})
	return false
}
