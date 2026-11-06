package controlplane

import (
	"reflect"
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
		{"-A wins over a later -n", []string{"get", "secrets", "-A", "-n", "team-a"}, "", "get", "secrets", "*"},
		{"-n value is not the resource", []string{"get", "-n", "team-a", "secrets"}, "", "get", "secrets", "team-a"},
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

func TestDenialMessage(t *testing.T) {
	const tail = ` on cluster "prod" is not allowed for dev@x.com; ask a kbridge admin to extend your role`
	tests := []struct {
		name    string
		command []string
		want    string
	}{
		{"namespaced", []string{"delete", "pods", "-n", "team-b"}, `permission denied: delete pods in namespace "team-b"` + tail},
		{"all namespaces", []string{"get", "pods", "-A"}, `permission denied: get pods in all namespaces` + tail},
		{"no resource", []string{"apply", "-f", "-"}, `permission denied: apply in namespace "default"` + tail},
		{"flag hid the resource", []string{"get", "--no-headers", "pods"},
			`permission denied: get in namespace "default"` + tail + " (write flags as --flag=value or put them after the resource)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAccessRequest("prod", tt.command, "").DenialMessage("dev@x.com")
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestParseAccessRequest_AttachedNamespace(t *testing.T) {
	tests := []struct {
		name    string
		command []string
		want    string
	}{
		{"attached", []string{"get", "pods", "-nteam-b"}, "team-b"},
		{"last wins", []string{"get", "pods", "-n", "team-a", "-nteam-b"}, "team-b"},
		{"separate after attached", []string{"get", "pods", "-nteam-b", "-n", "team-a"}, "team-a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseAccessRequest("c1", tt.command, "").Namespace; got != tt.want {
				t.Errorf("namespace = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManifestNamespaces(t *testing.T) {
	tests := []struct {
		name, stdin string
		want        []string
		wantUnnamed bool
	}{
		{"one namespaced, one not", "metadata:\n  namespace: team-a\n---\nmetadata:\n  name: x\n", []string{"team-a"}, true},
		{"duplicates collapse", "metadata: {namespace: a}\n---\nmetadata: {namespace: b}\n---\nmetadata: {namespace: a}\n", []string{"a", "b"}, false},
		{"invalid yaml", "metadata: [\n", nil, true},
		{"empty", "", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, unnamed := manifestNamespaces([]byte(tt.stdin))
			if !reflect.DeepEqual(got, tt.want) || unnamed != tt.wantUnnamed {
				t.Errorf("got %q unnamed=%v, want %q unnamed=%v", got, unnamed, tt.want, tt.wantUnnamed)
			}
		})
	}
}

func TestParseAccessRequest_SkipsFlagValues(t *testing.T) {
	tests := []struct {
		name    string
		command []string
		wantRes string
		wantNS  string
	}{
		{"-l value", []string{"get", "-l", "pods", "secrets"}, "secrets", "default"},
		{"-o value", []string{"get", "-o", "json", "secrets"}, "secrets", "default"},
		{"--selector value", []string{"get", "--selector", "app=x", "pods"}, "pods", "default"},
		{"-o= form", []string{"get", "-o=json", "secrets"}, "secrets", "default"},
		{"global value flag", []string{"get", "--request-timeout", "pods", "secrets"}, "secrets", "default"},
		{"pod-scoped verb", []string{"logs", "-c", "app", "web"}, "pods", "default"},
		{"no aliasing", []string{"get", "--output", "wide", "-n", "dev", "deploy"}, "deploy", "dev"},
		{"short cluster ending in n", []string{"exec", "-itn", "dev", "web", "--", "sh"}, "pods", "dev"},
		{"short cluster with attached n", []string{"get", "-wndev", "secrets"}, "secrets", "dev"},
		{"logs -f is follow", []string{"logs", "-f", "-n", "dev", "web"}, "pods", "dev"},
		{"-l value that looks like -n", []string{"get", "-ln", "secrets"}, "secrets", "default"},
		{"unknown long flag takes a value", []string{"get", "--sort-by", "pods", "secrets"}, "secrets", "default"},
		{"unknown long flag hides the resource", []string{"get", "--no-headers", "pods"}, "", "default"},
		{"unknown long flag with =", []string{"get", "--no-headers=true", "pods"}, "pods", "default"},
		{"unknown long flag after resource", []string{"get", "pods", "--no-headers"}, "pods", "default"},
		{"unknown long flag never swallows a flag", []string{"get", "--no-headers", "-n", "dev", "pods"}, "pods", "dev"},
		{"known global bool", []string{"get", "--warnings-as-errors", "pods"}, "pods", "default"},
		{"-A inside a short cluster", []string{"get", "-An", "dev", "secrets"}, "secrets", "*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAccessRequest("c1", tt.command, "")
			if got.Resource != tt.wantRes || got.Namespace != tt.wantNS {
				t.Errorf("got res=%q ns=%q; want res=%q ns=%q", got.Resource, got.Namespace, tt.wantRes, tt.wantNS)
			}
		})
	}
}
