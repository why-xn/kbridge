package cli

import (
	"encoding/json"
	"errors"
	"fmt"
)

// policyRejection is the control plane's 403 body for a command the policy
// refused. The extra fields are present only when a guardrail was responsible.
type policyRejection struct {
	Error            string `json:"error"`
	Guardrail        string `json:"guardrail,omitempty"`
	ReasonRequired   bool   `json:"reason_required,omitempty"`
	ApprovalRequired bool   `json:"approval_required,omitempty"`
}

// reasonHint tells the user how to satisfy a guardrail that wants a reason.
const reasonHint = `retry with --reason "<why>", for example: --reason "INC-4521 rolling back bad deploy"`

// approvalHint tells the user how to satisfy a guardrail that wants an approved
// grant. Unlike a reason, they cannot satisfy this one alone.
const approvalHint = `request access with: kb request <cluster> --duration 2h --reason "<why>"
someone else must approve it; track it with 'kb grants'`

// policyRejectionError turns a 403 body into an error the user can act on. A
// body that is missing or not a policy rejection degrades to "permission
// denied", which is what the control plane sends for a plain RBAC denial.
func policyRejectionError(body []byte) error {
	var r policyRejection
	if err := json.Unmarshal(body, &r); err != nil || r.Error == "" {
		return errors.New("permission denied")
	}
	msg := r.Error
	if r.Guardrail != "" {
		msg = fmt.Sprintf("%s (guardrail %q)", msg, r.Guardrail)
	}
	if r.ReasonRequired {
		msg += "\n" + reasonHint
	}
	if r.ApprovalRequired {
		msg += "\n" + approvalHint
	}
	return errors.New(msg)
}
