package cli

import (
	"reflect"
	"testing"
)

func TestParseExecArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want execTarget
		ok   bool
	}{
		{"interactive shell", []string{"exec", "-it", "mypod", "--", "sh"},
			execTarget{pod: "mypod", command: []string{"sh"}, tty: true, stdin: true}, true},
		{"with container and namespace", []string{"exec", "-i", "-t", "-n", "prod", "p", "-c", "app", "--", "bash", "-l"},
			execTarget{namespace: "prod", pod: "p", container: "app", command: []string{"bash", "-l"}, tty: true, stdin: true}, true},
		{"stdin only", []string{"exec", "-i", "p", "--", "sh"},
			execTarget{pod: "p", command: []string{"sh"}, stdin: true}, true},
		{"non-interactive exec is not handled here", []string{"exec", "p", "--", "ls"},
			execTarget{}, false},
		{"not an exec command", []string{"get", "pods"}, execTarget{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseExecArgs(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v", ok, tc.ok)
			}
			if ok && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}
