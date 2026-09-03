package cli

import "strings"

// reasonFlag is the kbridge-specific flag that carries a justification for a
// command. Guardrails configured with the require-reason action demand it, and
// the control plane records it in the audit log.
const reasonFlag = "--reason"

// extractReason pulls --reason out of a kubectl argument list and returns the
// justification alongside the remaining arguments. Both "--reason why" and
// "--reason=why" are accepted. kubectl has no --reason flag of its own, so
// removing it here keeps the remote invocation valid. The last occurrence wins.
func extractReason(args []string) (string, []string) {
	reason := ""
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		value, consumed, ok := reasonAt(args, i)
		if !ok {
			rest = append(rest, args[i])
			continue
		}
		reason = value
		i += consumed
	}
	return strings.TrimSpace(reason), rest
}

// reasonAt reports whether args[i] starts a --reason flag, returning its value
// and how many extra arguments it consumed.
func reasonAt(args []string, i int) (value string, consumed int, ok bool) {
	arg := args[i]
	if v, found := strings.CutPrefix(arg, reasonFlag+"="); found {
		return v, 0, true
	}
	if arg != reasonFlag {
		return "", 0, false
	}
	if i+1 < len(args) {
		return args[i+1], 1, true
	}
	return "", 0, true
}
