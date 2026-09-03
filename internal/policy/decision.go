package policy

// Outcome is the result of evaluating a command against a policy.
type Outcome string

const (
	// OutcomeAllowed means the command may run.
	OutcomeAllowed Outcome = "allowed"
	// OutcomeDenied means no role rule grants the command.
	OutcomeDenied Outcome = "denied"
	// OutcomeBlocked means a role rule granted the command but a guardrail
	// rejected it.
	OutcomeBlocked Outcome = "blocked"
	// OutcomeReasonRequired means a guardrail will admit the command once the
	// caller supplies a justification.
	OutcomeReasonRequired Outcome = "reason-required"
	// OutcomeApprovalRequired means a guardrail will admit the command only
	// while an approved grant covers it. Whether one does is dynamic state the
	// control plane holds, not something this package can answer, so a caller
	// that manages grants checks for one before treating this as a refusal.
	OutcomeApprovalRequired Outcome = "approval-required"
)

// Decision is the verdict on a single command, carrying enough detail to
// explain itself to the user and to the audit log.
type Decision struct {
	Outcome   Outcome
	Guardrail string // name of the guardrail that fired; empty for RBAC outcomes
	Message   string // human-readable explanation, safe to return to the caller
	Reason    string // the accepted, normalized justification, when one applied
}

// Allowed reports whether the command may proceed.
func (d Decision) Allowed() bool { return d.Outcome == OutcomeAllowed }

// allowed builds an allow decision carrying any accepted reason.
func allowed(reason string) Decision {
	return Decision{Outcome: OutcomeAllowed, Reason: reason}
}

// Evaluate decides whether subject may run req, given the justification the
// caller supplied (empty when none was). Role rules are checked first; only a
// command they grant is then tested against the guardrails, in file order,
// where the first matching guardrail decides.
func (p *Policy) Evaluate(subject string, req AccessRequest, reason string) Decision {
	if !p.allows(subject, req) {
		return Decision{Outcome: OutcomeDenied, Message: "permission denied"}
	}
	reason = NormalizeReason(reason)
	for _, g := range p.Guardrails {
		if !g.Match.matches(req) || g.exempts(subject) {
			continue
		}
		return g.decide(reason)
	}
	return allowed(reason)
}

// decide applies a matched guardrail's action.
func (g Guardrail) decide(reason string) Decision {
	if g.Action == ActionRequireReason && reasonSatisfied(reason) {
		return allowed(reason)
	}
	return Decision{
		Outcome:   g.Action.outcome(),
		Guardrail: g.Name,
		Message:   g.explain(),
		Reason:    reason,
	}
}

// outcome is the verdict an action produces when it does not admit the command.
func (a Action) outcome() Outcome {
	switch a {
	case ActionRequireReason:
		return OutcomeReasonRequired
	case ActionRequireApproval:
		return OutcomeApprovalRequired
	default:
		return OutcomeBlocked
	}
}

// Evaluate decides whether subject may run req under the current policy.
func (e *Engine) Evaluate(subject string, req AccessRequest, reason string) Decision {
	return e.current.Load().Evaluate(subject, req, reason)
}
