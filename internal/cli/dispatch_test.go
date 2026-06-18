package cli

import (
	"reflect"
	"testing"
)

func TestRewriteArgs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"no args", []string{}, []string{}},
		{"kubectl verb prepends kubectl", []string{"get", "pods"}, []string{"kubectl", "get", "pods"}},
		{"kubectl verb with flags", []string{"get", "pods", "-A"}, []string{"kubectl", "get", "pods", "-A"}},
		{"management admin untouched", []string{"admin", "users", "list"}, []string{"admin", "users", "list"}},
		{"management clusters untouched", []string{"clusters", "use", "prod"}, []string{"clusters", "use", "prod"}},
		{"cluster alias untouched", []string{"cluster", "use", "prod"}, []string{"cluster", "use", "prod"}},
		{"login untouched", []string{"login"}, []string{"login"}},
		{"logout untouched", []string{"logout"}, []string{"logout"}},
		{"status untouched", []string{"status"}, []string{"status"}},
		{"explicit kubectl untouched", []string{"kubectl", "get", "pods"}, []string{"kubectl", "get", "pods"}},
		{"explicit k untouched", []string{"k", "get", "pods"}, []string{"k", "get", "pods"}},
		{"help untouched", []string{"help"}, []string{"help"}},
		{"completion untouched", []string{"completion", "bash"}, []string{"completion", "bash"}},
		{"--help untouched", []string{"--help"}, []string{"--help"}},
		{"-h untouched", []string{"-h"}, []string{"-h"}},
		{"--version untouched", []string{"--version"}, []string{"--version"}},
		{"version word goes to kubectl", []string{"version"}, []string{"kubectl", "version"}},
		{"leading kubectl flag goes to kubectl", []string{"-n", "kube-system", "get", "pods"}, []string{"kubectl", "-n", "kube-system", "get", "pods"}},
		{"completion directive passes through", []string{"__complete", "get", ""}, []string{"__complete", "get", ""}},
		{"unknown verb (typo) goes to kubectl", []string{"gte", "pods"}, []string{"kubectl", "gte", "pods"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriteArgs(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("rewriteArgs(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
