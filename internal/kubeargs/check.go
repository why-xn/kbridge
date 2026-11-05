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
	"--patch-file", "--template", "--profile", "--profile-output"}
var refusedVerbs = map[string]bool{"cp": true, "proxy": true, "plugin": true, "config": true, "kustomize": true}
var shortValueFlags = map[byte]bool{'n': true, 'o': true, 'l': true, 'c': true, 'L': true}

// valueFlags are global and shared options that take a separate value, which verbOf must skip.
var valueFlags = []string{"-v", "--v", "--vmodule", "--request-timeout", "--log-flush-frequency",
	"--namespace", "--output", "--selector", "--container", "--label-columns"}

func connErr(flag string) error {
	return fmt.Errorf("flag %s is not allowed: kbridge manages the cluster connection", flag)
}

func fileErr(flag string) error {
	return fmt.Errorf("flag %s is not allowed: the agent does not read local files; the kb CLI uploads them", flag)
}

// Check refuses kubectl arguments that change the connection, read local files,
// or run verbs that act on the agent's own environment. It never parses manifests.
func Check(args []string) error {
	verb := verbOf(args)
	if refusedVerbs[verb] {
		return fmt.Errorf("kubectl %s is not supported through kbridge", verb)
	}
	for i, tok := range args {
		if tok == "--" {
			return nil
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

// verbOf returns the first positional token, skipping flags and their values.
func verbOf(args []string) string {
	for i := 0; i < len(args); i++ {
		tok := args[i]
		switch {
		case tok == "--":
			return ""
		case !strings.HasPrefix(tok, "-"):
			return tok
		case takesNext(tok):
			i++
		}
	}
	return ""
}

func takesNext(tok string) bool {
	return (len(tok) == 2 && shortValueFlags[tok[1]]) || tok == "-f" || tok == "--filename" ||
		slices.Contains(connectionFlags, tok) || slices.Contains(fileFlags, tok) ||
		slices.Contains(valueFlags, tok)
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
