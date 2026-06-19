package cli

import (
	"reflect"
	"testing"
)

func TestParsePortForwardArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want pfTarget
		ok   bool
	}{
		{"local:remote", []string{"port-forward", "p", "8080:80"},
			pfTarget{pod: "p", mappings: []portMapping{{8080, 80}}}, true},
		{"bare remote means same local", []string{"port-forward", "p", "6379"},
			pfTarget{pod: "p", mappings: []portMapping{{6379, 6379}}}, true},
		{"random local", []string{"port-forward", "p", ":5432"},
			pfTarget{pod: "p", mappings: []portMapping{{0, 5432}}}, true},
		{"multi + namespace", []string{"port-forward", "-n", "prod", "p", "8080:80", "6379:6379"},
			pfTarget{namespace: "prod", pod: "p", mappings: []portMapping{{8080, 80}, {6379, 6379}}}, true},
		{"not port-forward", []string{"get", "pods"}, pfTarget{}, false},
		{"missing pod", []string{"port-forward"}, pfTarget{}, false},
		{"missing ports", []string{"port-forward", "p"}, pfTarget{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parsePortForwardArgs(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v", ok, tc.ok)
			}
			if ok && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}
