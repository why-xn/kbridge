package cli

import "strings"

// managementCommands are the first-argument words that select a kbridge
// management subcommand rather than a remote kubectl invocation. Everything not
// in this set is treated as a kubectl command (kubectl-by-default), so
// `kb get pods` runs kubectl while `kb admin users list` runs the admin command.
var managementCommands = map[string]bool{
	"admin":      true,
	"clusters":   true,
	"cluster":    true, // alias for "clusters"
	"login":      true,
	"logout":     true,
	"status":     true,
	"kubectl":    true, // explicit escape hatch
	"k":          true, // explicit escape hatch
	"help":       true, // cobra built-in
	"completion": true, // cobra built-in
}

// rewriteArgs implements kubectl-by-default dispatch. Given the raw CLI
// arguments (os.Args[1:]), it returns the arguments cobra should run: if the
// first argument is a management subcommand, a top-level CLI flag, or a cobra
// internal directive, it is left untouched; otherwise "kubectl" is prepended so
// the whole line is executed as a remote kubectl command.
func rewriteArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}

	first := args[0]

	// Cobra shell-completion machinery (e.g. __complete) must pass through.
	if strings.HasPrefix(first, "__") {
		return args
	}

	if strings.HasPrefix(first, "-") {
		switch first {
		case "-h", "--help", "-v", "--version":
			return args
		default:
			// A leading flag that isn't ours (e.g. `kb -n ns get pods`) belongs
			// to kubectl.
			return prependKubectl(args)
		}
	}

	if managementCommands[first] {
		return args
	}
	return prependKubectl(args)
}

func prependKubectl(args []string) []string {
	return append([]string{"kubectl"}, args...)
}
