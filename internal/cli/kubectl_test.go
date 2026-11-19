package cli

import "testing"

func TestIsStreamingCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"logs -f", []string{"logs", "-f", "pod"}, true},
		{"logs --follow", []string{"logs", "--follow", "pod"}, true},
		{"plain logs", []string{"logs", "pod"}, false},
		{"get -w", []string{"get", "pods", "-w"}, true},
		{"get --watch", []string{"get", "pods", "--watch"}, true},
		{"plain get", []string{"get", "pods"}, false},
		{"logs -f after -n", []string{"-n", "prod", "logs", "-f", "pod"}, true},
		{"apply -f is a filename", []string{"apply", "-f", "app.yaml"}, false},
		{"delete -f is a filename", []string{"delete", "-f", "app.yaml"}, false},
		{"create -f stdin", []string{"create", "-f", "-"}, false},
		{"get -f with -w watches", []string{"get", "-f", "app.yaml", "-w"}, true},
		{"--watch-only", []string{"get", "pods", "--watch-only"}, true},
		{"empty", []string{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isStreamingCommand(tt.args); got != tt.want {
				t.Errorf("isStreamingCommand(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestExitStatus(t *testing.T) {
	for _, tt := range []struct{ in, want int }{{-1, 1}, {1, 1}, {2, 2}, {137, 137}} {
		if got := exitStatus(tt.in); got != tt.want {
			t.Errorf("exitStatus(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestFlagNamespace(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"container command -n", []string{"exec", "web", "--", "grep", "-n", "web", "f"}, ""},
		{"-n after --", []string{"get", "pods", "--", "-n", "x"}, ""},
		{"-n before and after --", []string{"get", "pods", "-n", "team-a", "--", "-n", "x"}, "team-a"},
		{"--namespace=", []string{"get", "pods", "--namespace=team-a"}, "team-a"},
		{"--namespace", []string{"get", "pods", "--namespace", "team-a"}, "team-a"},
		{"-n joined", []string{"get", "pods", "-nteam-a"}, "team-a"},
		{"-n=", []string{"get", "pods", "-n=team-a"}, "team-a"},
		{"-n before the verb", []string{"-n", "prod", "logs", "-f", "pod"}, "prod"},
		{"last -n wins", []string{"get", "pods", "-n", "a", "-n", "b"}, "b"},
		{"no flag", []string{"get", "pods"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := flagNamespace(tt.args); got != tt.want {
				t.Errorf("flagNamespace(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
