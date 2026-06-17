package central

import (
	"testing"
)

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern, value string
		want           bool
	}{
		{"*", "anything", true},
		{"*", "", true},
		{"dev-cluster", "dev-cluster", true},
		{"dev-cluster", "dev-cluster-2", false},
		{"dev-*", "dev-cluster", true},
		{"dev-*", "dev-", true},
		{"dev-*", "prod-cluster", false},
		{"*-prod", "us-prod", true},
		{"*-prod", "us-prod-1", false},
		{"app-*-svc", "app-web-svc", true},
		{"app-*-svc", "app--svc", true},  // '*' matches empty
		{"app-*-svc", "app-svc", false},  // missing the second '-'
		{"app-*-svc", "app-web-api", false},
		{"", "", true},
		{"", "x", false},
	}
	for _, tt := range tests {
		if got := matchPattern(tt.pattern, tt.value); got != tt.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.value, got, tt.want)
		}
	}
}

func TestVerbAllowed(t *testing.T) {
	tests := []struct {
		verbs, verb string
		want        bool
	}{
		{"*", "get", true},
		{"get,list,logs", "list", true},
		{"get,list,logs", "delete", false},
		{"get, list , logs", "list", true}, // tolerate spaces
		{"GET,LIST", "get", true},           // case-insensitive
		{"get", "GET", true},
		{"", "get", false},
	}
	for _, tt := range tests {
		if got := verbAllowed(tt.verbs, tt.verb); got != tt.want {
			t.Errorf("verbAllowed(%q, %q) = %v, want %v", tt.verbs, tt.verb, got, tt.want)
		}
	}
}

func TestParseAccessRequest(t *testing.T) {
	tests := []struct {
		name      string
		command   []string
		fallback  string
		wantVerb  string
		wantRes   string
		wantNS    string
	}{
		{"simple get", []string{"get", "pods"}, "", "get", "pods", "default"},
		{"fallback namespace", []string{"get", "pods"}, "app", "get", "pods", "app"},
		{"-n flag overrides fallback", []string{"get", "pods", "-n", "kube-system"}, "app", "get", "pods", "kube-system"},
		{"--namespace= form", []string{"get", "svc", "--namespace=infra"}, "", "get", "svc", "infra"},
		{"all namespaces -A", []string{"get", "pods", "-A"}, "", "get", "pods", "*"},
		{"all namespaces long", []string{"get", "pods", "--all-namespaces"}, "", "get", "pods", "*"},
		{"resource/name strips name", []string{"delete", "pods/web-1"}, "", "delete", "pods", "default"},
		{"logs is pod-scoped", []string{"logs", "web-1"}, "", "logs", "pods", "default"},
		{"exec is pod-scoped", []string{"exec", "web-1", "--", "sh"}, "", "exec", "pods", "default"},
		{"empty command", []string{}, "", "", "", "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAccessRequest("c1", tt.command, tt.fallback)
			if got.Cluster != "c1" {
				t.Errorf("cluster = %q, want c1", got.Cluster)
			}
			if got.Verb != tt.wantVerb || got.Resource != tt.wantRes || got.Namespace != tt.wantNS {
				t.Errorf("got verb=%q res=%q ns=%q; want verb=%q res=%q ns=%q",
					got.Verb, got.Resource, got.Namespace, tt.wantVerb, tt.wantRes, tt.wantNS)
			}
		})
	}
}
