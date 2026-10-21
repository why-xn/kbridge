package cli

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// valueFlags are kubectl global flags that may precede the subcommand and take
// a separate value, so kubectlVerb must skip that value too.
var valueFlags = map[string]bool{
	"-n": true, "--namespace": true, "--context": true, "--cluster": true,
	"--user": true, "-s": true, "--server": true, "--kubeconfig": true, "--as": true,
}

// kubectlVerb returns the kubectl subcommand: the first positional argument.
func kubectlVerb(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			return a
		}
		if valueFlags[a] {
			i++
		}
	}
	return ""
}

// manifestSource is one -f value: a local path, "-" for stdin, or a URL.
type manifestSource struct {
	value string
}

func (m manifestSource) isURL() bool {
	return strings.HasPrefix(m.value, "http://") || strings.HasPrefix(m.value, "https://")
}

// splitFilenameFlags separates -f/--filename values from the other arguments.
// "logs -f" means follow, not a filename, so logs is left untouched.
func splitFilenameFlags(args []string) (rest []string, sources []manifestSource) {
	if kubectlVerb(args) == "logs" {
		return args, nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case (a == "-f" || a == "--filename") && i+1 < len(args):
			sources = append(sources, manifestSource{args[i+1]})
			i++
		case strings.HasPrefix(a, "--filename="):
			sources = append(sources, manifestSource{strings.TrimPrefix(a, "--filename=")})
		case strings.HasPrefix(a, "-f="):
			sources = append(sources, manifestSource{strings.TrimPrefix(a, "-f=")})
		default:
			rest = append(rest, a)
		}
	}
	return rest, sources
}

// localManifests makes local -f inputs work against a remote agent: kubectl
// runs on the agent, which cannot see the user's files or terminal. Every local
// file, directory and "-" is read here and sent as stdin behind a single
// "-f -"; URLs stay as flags for the agent to fetch. It returns ok=false when
// there is nothing local to send.
func localManifests(args []string, stdin io.Reader) (newArgs []string, body []byte, ok bool, err error) {
	if err := rejectLocalKustomize(args); err != nil {
		return nil, nil, false, err
	}
	rest, sources := splitFilenameFlags(args)
	recursive := hasFlag(args, "-R", "--recursive")
	var buf bytes.Buffer
	for _, src := range sources {
		if src.isURL() {
			rest = append(rest, "-f", src.value)
			continue
		}
		if err := appendManifest(&buf, src.value, recursive, stdin); err != nil {
			return nil, nil, false, err
		}
		ok = true
	}
	if !ok {
		return args, nil, false, nil
	}
	return append(rest, "-f", "-"), buf.Bytes(), true, nil
}

// appendManifest adds one source's documents to buf, each behind a "---"
// separator so multiple files (YAML or JSON) form one valid YAML stream.
func appendManifest(buf *bytes.Buffer, value string, recursive bool, stdin io.Reader) error {
	if value == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("reading manifests from stdin: %w", err)
		}
		writeDoc(buf, data)
		return nil
	}
	files, err := manifestFiles(value, recursive)
	if err != nil {
		return err
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("reading manifest %s: %w", f, err)
		}
		writeDoc(buf, data)
	}
	return nil
}

func writeDoc(buf *bytes.Buffer, data []byte) {
	buf.WriteString("---\n")
	buf.Write(data)
	buf.WriteString("\n")
}

// manifestFiles expands a path the way kubectl does: a file is itself; a
// directory yields its .yaml/.yml/.json files (subdirectories only with -R).
func manifestFiles(path string, recursive bool) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	var files []string
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != path && !recursive {
				return fs.SkipDir
			}
			return nil
		}
		if isManifestFile(p) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading manifest directory %s: %w", path, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .yaml, .yml or .json files in %s", path)
	}
	sort.Strings(files)
	return files, nil
}

func isManifestFile(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".yaml", ".yml", ".json":
		return true
	}
	return false
}

// rejectLocalKustomize fails fast for -k with a local directory: it would be
// resolved on the agent, where the directory does not exist.
func rejectLocalKustomize(args []string) error {
	for i, a := range args {
		dir := ""
		switch {
		case (a == "-k" || a == "--kustomize") && i+1 < len(args):
			dir = args[i+1]
		case strings.HasPrefix(a, "--kustomize="):
			dir = strings.TrimPrefix(a, "--kustomize=")
		}
		if dir != "" && !(manifestSource{dir}).isURL() {
			return fmt.Errorf("local kustomize directories are not supported: run 'kubectl kustomize %s | kb %s -f -'", dir, kubectlVerb(args))
		}
	}
	return nil
}

func hasFlag(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == n || a == n+"=true" {
				return true
			}
		}
	}
	return false
}
