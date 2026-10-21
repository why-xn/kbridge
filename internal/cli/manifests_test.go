package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestKubectlVerb(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"apply", "-f", "x.yaml"}, "apply"},
		{[]string{"-n", "prod", "logs", "-f", "web"}, "logs"},
		{[]string{"--context=x", "get", "pods"}, "get"},
		{[]string{"--namespace", "prod", "get", "pods"}, "get"},
		{[]string{"-f", "x.yaml"}, "x.yaml"}, // no verb: first positional
		{nil, ""},
	}
	for _, tt := range tests {
		if got := kubectlVerb(tt.args); got != tt.want {
			t.Errorf("kubectlVerb(%v) = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalManifests(t *testing.T) {
	dir := t.TempDir()
	cm := filepath.Join(dir, "cm.yaml")
	writeFile(t, cm, "kind: ConfigMap")
	writeFile(t, filepath.Join(dir, "app", "a.yaml"), "kind: A")
	writeFile(t, filepath.Join(dir, "app", "b.json"), `{"kind":"B"}`)
	writeFile(t, filepath.Join(dir, "app", "notes.txt"), "ignored")
	writeFile(t, filepath.Join(dir, "app", "sub", "c.yml"), "kind: C")
	app := filepath.Join(dir, "app")
	const url = "https://example.com/m.yaml"

	tests := []struct {
		name     string
		args     []string
		stdin    string
		wantArgs []string
		wantBody []string // documents expected in the stdin body, in order
		wantOK   bool
		wantErr  string
	}{
		{name: "no -f is untouched", args: []string{"get", "pods"}, wantArgs: []string{"get", "pods"}},
		{name: "logs -f means follow", args: []string{"logs", "-f", "web"}, wantArgs: []string{"logs", "-f", "web"}},
		{name: "url is fetched by the agent", args: []string{"apply", "-f", url}, wantArgs: []string{"apply", "-f", url}},
		{name: "local file", args: []string{"apply", "-f", cm, "-n", "demo"},
			wantArgs: []string{"apply", "-n", "demo", "-f", "-"}, wantBody: []string{"kind: ConfigMap"}, wantOK: true},
		{name: "--filename= form", args: []string{"delete", "--filename=" + cm},
			wantArgs: []string{"delete", "-f", "-"}, wantBody: []string{"kind: ConfigMap"}, wantOK: true},
		{name: "stdin", args: []string{"apply", "-f", "-"}, stdin: "kind: FromStdin",
			wantArgs: []string{"apply", "-f", "-"}, wantBody: []string{"kind: FromStdin"}, wantOK: true},
		{name: "directory skips subdirs and non-manifests", args: []string{"apply", "-f", app},
			wantArgs: []string{"apply", "-f", "-"}, wantBody: []string{"kind: A", `{"kind":"B"}`}, wantOK: true},
		{name: "directory with -R recurses", args: []string{"apply", "-R", "-f", app},
			wantArgs: []string{"apply", "-R", "-f", "-"}, wantBody: []string{"kind: A", `{"kind":"B"}`, "kind: C"}, wantOK: true},
		{name: "local and url mixed", args: []string{"apply", "-f", cm, "-f", url},
			wantArgs: []string{"apply", "-f", url, "-f", "-"}, wantBody: []string{"kind: ConfigMap"}, wantOK: true},
		{name: "missing file", args: []string{"apply", "-f", filepath.Join(dir, "nope.yaml")}, wantErr: "reading manifest"},
		{name: "local kustomize dir", args: []string{"apply", "-k", app}, wantErr: "kustomize directories are not supported"},
		{name: "remote kustomize is allowed", args: []string{"apply", "-k", url}, wantArgs: []string{"apply", "-k", url}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, body, ok, err := localManifests(tt.args, strings.NewReader(tt.stdin))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ok != tt.wantOK || !slices.Equal(args, tt.wantArgs) {
				t.Fatalf("got (%v, ok=%v), want (%v, ok=%v)", args, ok, tt.wantArgs, tt.wantOK)
			}
			var docs []string
			for _, d := range strings.Split(string(body), "---\n") {
				if d = strings.TrimSpace(d); d != "" {
					docs = append(docs, d)
				}
			}
			if !slices.Equal(docs, tt.wantBody) {
				t.Fatalf("body docs = %q, want %q", docs, tt.wantBody)
			}
		})
	}
}

func TestStreamCommand_ReturnsRemoteExitCode(t *testing.T) {
	tests := []struct {
		name    string
		trailer string
		want    int
	}{
		{"success", "0", 0},
		{"failure", "1", 1},
		{"older server without trailer", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Trailer", exitCodeTrailer)
				_, _ = io.WriteString(w, "output\n")
				if tt.trailer != "" {
					w.Header().Set(exitCodeTrailer, tt.trailer)
				}
			}))
			defer ts.Close()
			var out strings.Builder
			code, err := NewControlPlaneClient(ts.URL).StreamCommand(context.Background(), "prod", []string{"logs", "-f", "x"}, "", &out)
			if err != nil {
				t.Fatal(err)
			}
			if code != tt.want || out.String() != "output\n" {
				t.Fatalf("got code=%d out=%q, want code=%d", code, out.String(), tt.want)
			}
		})
	}
}

func TestErrNoControlPlaneURL_NamesConfigFile(t *testing.T) {
	msg := errNoControlPlaneURL().Error()
	for _, want := range []string{ConfigKeyControlPlaneURL, configFile()} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q", msg, want)
		}
	}
	if strings.Contains(msg, "kb config set") {
		t.Errorf("message %q points to a command that does not exist", msg)
	}
}
