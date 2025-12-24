package cli

import (
	"os"
	"testing"
)

func TestIsEditCommand(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected bool
	}{
		{
			name:     "simple edit",
			args:     []string{"edit", "deployment/nginx"},
			expected: true,
		},
		{
			name:     "edit with namespace",
			args:     []string{"edit", "-n", "kube-system", "pod/coredns"},
			expected: true,
		},
		{
			name:     "edit with namespace flag first",
			args:     []string{"-n", "default", "edit", "configmap/my-config"},
			expected: true,
		},
		{
			name:     "get pods",
			args:     []string{"get", "pods"},
			expected: false,
		},
		{
			name:     "apply with file",
			args:     []string{"apply", "-f", "manifest.yaml"},
			expected: false,
		},
		{
			name:     "empty args",
			args:     []string{},
			expected: false,
		},
		{
			name:     "describe with edit in name",
			args:     []string{"describe", "pod/editor"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isEditCommand(tt.args)
			if result != tt.expected {
				t.Errorf("isEditCommand(%v) = %v, want %v", tt.args, result, tt.expected)
			}
		})
	}
}

func TestEditHandler_parseArgs(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantType     string
		wantName     string
		wantNS       string
		wantErr      bool
		errSubstring string
	}{
		{
			name:     "type/name format",
			args:     []string{"edit", "deployment/nginx"},
			wantType: "deployment",
			wantName: "nginx",
			wantNS:   "",
			wantErr:  false,
		},
		{
			name:     "type name format",
			args:     []string{"edit", "pod", "my-pod"},
			wantType: "pod",
			wantName: "my-pod",
			wantNS:   "",
			wantErr:  false,
		},
		{
			name:     "with namespace flag short",
			args:     []string{"edit", "-n", "production", "configmap/app-config"},
			wantType: "configmap",
			wantName: "app-config",
			wantNS:   "production",
			wantErr:  false,
		},
		{
			name:     "with namespace flag long",
			args:     []string{"edit", "--namespace", "staging", "secret/my-secret"},
			wantType: "secret",
			wantName: "my-secret",
			wantNS:   "staging",
			wantErr:  false,
		},
		{
			name:     "namespace with equals",
			args:     []string{"edit", "-n=kube-system", "deployment/coredns"},
			wantType: "deployment",
			wantName: "coredns",
			wantNS:   "kube-system",
			wantErr:  false,
		},
		{
			name:     "long namespace with equals",
			args:     []string{"edit", "--namespace=test", "service/api"},
			wantType: "service",
			wantName: "api",
			wantNS:   "test",
			wantErr:  false,
		},
		{
			name:         "missing resource name",
			args:         []string{"edit", "deployment"},
			wantErr:      true,
			errSubstring: "requires a resource name",
		},
		{
			name:         "empty args after edit",
			args:         []string{"edit"},
			wantErr:      true,
			errSubstring: "requires a resource type",
		},
		{
			name:         "namespace flag without value",
			args:         []string{"edit", "-n"},
			wantErr:      true,
			errSubstring: "requires a value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &EditHandler{}
			err := h.parseArgs(tt.args)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseArgs(%v) expected error, got nil", tt.args)
					return
				}
				if tt.errSubstring != "" && !contains(err.Error(), tt.errSubstring) {
					t.Errorf("parseArgs(%v) error = %v, want error containing %q", tt.args, err, tt.errSubstring)
				}
				return
			}

			if err != nil {
				t.Errorf("parseArgs(%v) unexpected error: %v", tt.args, err)
				return
			}

			if h.resourceType != tt.wantType {
				t.Errorf("parseArgs(%v) resourceType = %q, want %q", tt.args, h.resourceType, tt.wantType)
			}
			if h.resourceName != tt.wantName {
				t.Errorf("parseArgs(%v) resourceName = %q, want %q", tt.args, h.resourceName, tt.wantName)
			}
			if h.namespace != tt.wantNS {
				t.Errorf("parseArgs(%v) namespace = %q, want %q", tt.args, h.namespace, tt.wantNS)
			}
		})
	}
}

func TestGetEditor(t *testing.T) {
	// Save original env vars
	origKubeEditor := os.Getenv("KUBE_EDITOR")
	origEditor := os.Getenv("EDITOR")
	origVisual := os.Getenv("VISUAL")

	// Clean up after test
	defer func() {
		os.Setenv("KUBE_EDITOR", origKubeEditor)
		os.Setenv("EDITOR", origEditor)
		os.Setenv("VISUAL", origVisual)
	}()

	tests := []struct {
		name       string
		kubeEditor string
		editor     string
		visual     string
		want       string
	}{
		{
			name:       "KUBE_EDITOR takes precedence",
			kubeEditor: "nano",
			editor:     "vim",
			visual:     "code",
			want:       "nano",
		},
		{
			name:       "EDITOR when KUBE_EDITOR not set",
			kubeEditor: "",
			editor:     "vim",
			visual:     "code",
			want:       "vim",
		},
		{
			name:       "VISUAL when others not set",
			kubeEditor: "",
			editor:     "",
			visual:     "code",
			want:       "code",
		},
		{
			name:       "default to vi",
			kubeEditor: "",
			editor:     "",
			visual:     "",
			want:       "vi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("KUBE_EDITOR", tt.kubeEditor)
			os.Setenv("EDITOR", tt.editor)
			os.Setenv("VISUAL", tt.visual)

			got := getEditor()
			if got != tt.want {
				t.Errorf("getEditor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEditHandler_createTempFile(t *testing.T) {
	h := &EditHandler{
		resourceType: "deployment",
		resourceName: "nginx",
	}

	content := "apiVersion: apps/v1\nkind: Deployment\n"
	tmpPath, err := h.createTempFile(content)
	if err != nil {
		t.Fatalf("createTempFile() error = %v", err)
	}
	defer os.Remove(tmpPath)

	// Verify file exists and has correct content
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("failed to read temp file: %v", err)
	}

	if string(data) != content {
		t.Errorf("temp file content = %q, want %q", string(data), content)
	}

	// Verify filename contains resource info
	if !contains(tmpPath, "mk8s-edit") {
		t.Errorf("temp file path %q should contain 'mk8s-edit'", tmpPath)
	}
}

func TestEditHandler_resourceIdentifier(t *testing.T) {
	tests := []struct {
		name      string
		handler   *EditHandler
		wantIdent string
	}{
		{
			name: "without namespace",
			handler: &EditHandler{
				resourceType: "deployment",
				resourceName: "nginx",
			},
			wantIdent: "deployment/nginx",
		},
		{
			name: "with namespace",
			handler: &EditHandler{
				resourceType: "configmap",
				resourceName: "app-config",
				namespace:    "production",
			},
			wantIdent: "configmap/app-config (namespace: production)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.handler.resourceIdentifier()
			if got != tt.wantIdent {
				t.Errorf("resourceIdentifier() = %q, want %q", got, tt.wantIdent)
			}
		})
	}
}

// contains checks if substr is in s.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && containsHelper(s, substr)))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
