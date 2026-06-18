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
