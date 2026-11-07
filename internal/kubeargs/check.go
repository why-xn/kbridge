// Package kubeargs refuses kubectl arguments the agent must not run.
package kubeargs

import (
	"fmt"
	"slices"
	"strings"
)

var connectionFlags = []string{"-s", "--server", "--token", "--kubeconfig", "--context", "--cluster",
	"--user", "--as", "--as-group", "--as-uid", "--username", "--password",
	"--certificate-authority", "--client-certificate", "--client-key",
	"--insecure-skip-tls-verify", "--tls-server-name", "--cache-dir"}
var fileFlags = []string{"-k", "--kustomize", "--from-file", "--from-env-file", "--cert", "--key",
	"--patch-file", "--template", "--profile", "--profile-output", "--custom"}
var refusedVerbs = map[string]bool{"cp": true, "proxy": true, "plugin": true, "config": true, "kustomize": true}

// containerVerbs pass the arguments after a real "--" to a container command, not to kubectl.
var containerVerbs = map[string]bool{"exec": true, "attach": true, "run": true, "debug": true}

// boolFlags are the boolean flags of each container-command verb at kubectl v1.31.0 (`kubectl <verb> --help`).
// Any other flag written without "=" just before "--" takes "--" as its value.
var boolFlags = map[string][]string{
	"exec":   {"-i", "--stdin", "-t", "--tty", "-q", "--quiet"},
	"attach": {"-i", "--stdin", "-t", "--tty", "-q", "--quiet"},
	"run": {"-i", "--stdin", "-t", "--tty", "-q", "--quiet", "-R", "--recursive", "--allow-missing-template-keys",
		"--attach", "--command", "--expose", "--force", "--leave-stdin-open", "--privileged", "--rm",
		"--save-config", "--show-managed-fields", "--wait"},
	"debug": {"-i", "--stdin", "-t", "--tty", "-q", "--quiet", "--arguments-only", "--attach",
		"--keep-annotations", "--keep-init-containers", "--keep-labels", "--keep-liveness", "--keep-readiness",
		"--keep-startup", "--replace", "--same-node", "--share-processes"},
}

var shortValueFlags = map[byte]bool{'n': true, 'o': true, 'l': true, 'c': true, 'L': true}

// globalValueFlags and globalBoolFlags are kubectl's global options at v1.31.0 (`kubectl options`).
// Before the verb, cobra lets any other --flag written without "=" take the next token, which hides the verb.
var globalValueFlags = []string{"--as", "--as-group", "--as-uid", "--cache-dir", "--certificate-authority",
	"--client-certificate", "--client-key", "--cluster", "--context", "--kubeconfig", "--log-flush-frequency",
	"--namespace", "--password", "--profile", "--profile-output", "--request-timeout", "--server",
	"--tls-server-name", "--token", "--user", "--username", "--v", "--vmodule"}
var globalBoolFlags = []string{"--disable-compression", "--insecure-skip-tls-verify", "--match-server-version",
	"--warnings-as-errors"}

func connErr(flag string) error {
	return fmt.Errorf("flag %s is not allowed: kbridge manages the cluster connection", flag)
}

func fileErr(flag string) error {
	return fmt.Errorf("flag %s is not allowed: the agent does not read local files; build the object locally with 'kubectl create ... --dry-run=client -o yaml' and send it with 'kb apply -f -'", flag)
}

// Check refuses kubectl arguments that change the connection, read local files,
// or run verbs that act on the agent's own environment. It never parses manifests.
// A value flag can swallow "--", so "--" ends the check only for a container-command verb.
func Check(args []string) error {
	verb, err := verbOf(args)
	if err != nil {
		return err
	}
	if refusedVerbs[verb] {
		return fmt.Errorf("kubectl %s is not supported through kbridge", verb)
	}
	for i, tok := range args {
		if tok == "--" && containerVerbs[verb] {
			return checkTerminator(args[:i], verb)
		}
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		if err := checkToken(tok, next, verb); err != nil {
			return err
		}
	}
	return nil
}

// verbOf returns the first positional token, skipping flags and their values the way cobra does,
// and refuses a flag before it that cobra would let swallow the verb.
func verbOf(args []string) (string, error) {
	for i := 0; i < len(args); i++ {
		tok := args[i]
		switch {
		case !strings.HasPrefix(tok, "-"):
			return tok, nil
		case tok == "--" || strings.Contains(tok, "=") || slices.Contains(globalBoolFlags, tok):
		case takesNext(tok):
			i++
		case strings.HasPrefix(tok, "--"):
			return "", fmt.Errorf("flag %s must come after the command or be written as %s=<value>", tok, tok)
		}
	}
	return "", nil
}

// checkTerminator refuses a "--" that the flag just before it would take as its value.
func checkTerminator(before []string, verb string) error {
	if len(before) == 0 {
		return nil
	}
	if flag := swallower(before[len(before)-1], verb); flag != "" {
		return fmt.Errorf("flag %s must be written as %s=<value> before --", flag, flag)
	}
	return nil
}

// swallower returns the flag in tok that would take the next token as its value, or "".
func swallower(tok, verb string) string {
	switch {
	case strings.HasPrefix(tok, "--"):
		if !strings.Contains(tok, "=") && !slices.Contains(boolFlags[verb], tok) {
			return tok
		}
	case len(tok) > 1 && tok[0] == '-':
		return shortSwallower(tok, verb)
	}
	return ""
}

// shortSwallower finds the first non-boolean letter of a cluster; it takes the next token only when it is last.
func shortSwallower(tok, verb string) string {
	for i := 1; i < len(tok); i++ {
		if f := "-" + string(tok[i]); !slices.Contains(boolFlags[verb], f) {
			if i == len(tok)-1 {
				return f
			}
			return ""
		}
	}
	return ""
}

// takesNext reports whether cobra gives tok the next token: any two-letter short flag, or a global value option.
func takesNext(tok string) bool {
	return (len(tok) == 2 && tok[1] != '-') || slices.Contains(globalValueFlags, tok)
}

func checkToken(tok, next, verb string) error {
	switch {
	case strings.HasPrefix(tok, "--"):
		return checkLongFlag(tok, next)
	case len(tok) > 1 && tok[0] == '-':
		return walkShortCluster(tok, next, verb)
	}
	return nil
}

func checkLongFlag(tok, next string) error {
	name, val, hasValue := strings.Cut(tok, "=")
	if !hasValue {
		val = next
	}
	switch {
	case slices.Contains(connectionFlags, name):
		return connErr(name)
	case slices.Contains(fileFlags, name):
		return fileErr(name)
	case name == "--filename":
		return checkFilename(val)
	case name == "--output" && strings.Contains(val, "-file="):
		return fileErr(name)
	}
	return nil
}

func checkFilename(val string) error {
	if val != "-" {
		return fmt.Errorf(`-f only accepts "-" on the agent: the kb CLI uploads local files and URLs`)
	}
	return nil
}

// walkShortCluster walks a single-dash token letter by letter; a value flag ends the walk.
func walkShortCluster(tok, next, verb string) error {
	for i := 1; i < len(tok); i++ {
		c, rest := tok[i], tok[i+1:]
		if rest == "" {
			rest = next
		}
		switch {
		case c == 'f' && verb == "logs":
		case c == 'f':
			return checkFilename(rest)
		case c == 'o' && strings.Contains(rest, "-file="):
			return fileErr("-o")
		case shortValueFlags[c]:
			return nil
		case slices.Contains(connectionFlags, "-"+string(c)):
			return connErr("-" + string(c))
		case slices.Contains(fileFlags, "-"+string(c)):
			return fileErr("-" + string(c))
		}
	}
	return nil
}

// longValueFlags are the long forms of the short value letters.
// The policy treats any other long flag before the resource as a value flag, unless IsBoolFlag knows it.
var longValueFlags = []string{"--output", "--selector", "--container", "--label-columns", "--filename"}

// TakesValue reports whether flag, written without "=", takes the next token as its value.
// A short cluster takes it when its first value letter is its last, as in -itn.
func TakesValue(flag string) bool {
	switch {
	case strings.Contains(flag, "=") || slices.Contains(globalBoolFlags, flag):
		return false
	case strings.HasPrefix(flag, "--"):
		return slices.Contains(globalValueFlags, flag) || slices.Contains(longValueFlags, flag) ||
			slices.Contains(connectionFlags, flag) || slices.Contains(fileFlags, flag)
	case len(flag) > 1 && flag[0] == '-':
		for i := 1; i < len(flag); i++ {
			if valueLetter(flag[i]) {
				return i == len(flag)-1
			}
		}
	}
	return false
}

// IsBoolFlag reports whether flag is one of kubectl's boolean global options.
func IsBoolFlag(flag string) bool {
	return slices.Contains(globalBoolFlags, flag)
}

func valueLetter(c byte) bool {
	f := "-" + string(c)
	return shortValueFlags[c] || c == 'f' || slices.Contains(connectionFlags, f) || slices.Contains(fileFlags, f)
}
