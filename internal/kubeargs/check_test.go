package kubeargs

import (
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	connMsg = "flag %s is not allowed: kbridge manages the cluster connection"
	fileMsg = "flag %s is not allowed: the agent does not read local files; the kb CLI uploads them"
	fMsg    = `-f only accepts "-" on the agent: the kb CLI uploads local files and URLs`
	verbMsg = "kubectl %s is not supported through kbridge"
)

func sprintf(f, a string) string { return strings.Replace(f, "%s", a, 1) }

func TestCheck(t *testing.T) {
	refused := []struct {
		args string
		want string
	}{
		{"get pods --server=https://evil", sprintf(connMsg, "--server")},
		{"get pods -shttps://evil", sprintf(connMsg, "-s")},
		{"get pods --token x", sprintf(connMsg, "--token")},
		{"get pods --kubeconfig /k", sprintf(connMsg, "--kubeconfig")},
		{"get pods --as=admin", sprintf(connMsg, "--as")},
		{"--namespace x cp /a b:/c", sprintf(verbMsg, "cp")},
		{"apply -f /etc/passwd", fMsg},
		{"apply -f https://x/y.yaml", fMsg},
		{"apply -Rf /dir", fMsg},
		{"apply --filename=/dir", fMsg},
		{"apply -k ./d", sprintf(fileMsg, "-k")},
		{"create configmap x --from-file=/var/run/kbridge/sa/token", sprintf(fileMsg, "--from-file")},
		{"create secret tls x --cert=/a --key=/b", sprintf(fileMsg, "--cert")},
		{"get pods -o go-template-file=/t", sprintf(fileMsg, "-o")},
		{"get pods --output=custom-columns-file=/t", sprintf(fileMsg, "--output")},
		{"patch deploy x --patch-file /p", sprintf(fileMsg, "--patch-file")},
		{"cp /etc/hosts default/x:/tmp/h", sprintf(verbMsg, "cp")},
		{"proxy", sprintf(verbMsg, "proxy")},
		{"config view", sprintf(verbMsg, "config")},
		{"kustomize ./d", sprintf(verbMsg, "kustomize")},
	}
	for _, tc := range refused {
		err := Check(strings.Fields(tc.args))
		if err == nil || err.Error() != tc.want {
			t.Errorf("Check(%q) = %v, want %q", tc.args, err, tc.want)
		}
	}
	allowed := []string{
		"get pods -n dev", "apply -f -", "logs -f web -n dev", "logs -fp web",
		"exec -itn dev web -- cat /etc/passwd",
		"exec -n dev web -- curl --server x -f /x",
		"get pods -o wide", "get pods -o jsonpath={.items[0]}",
		"get pods -l app=x -nfoo", "-n dev get pods",
	}
	for _, a := range allowed {
		if err := Check(strings.Fields(a)); err != nil {
			t.Errorf("Check(%q) = %v, want nil", a, err)
		}
	}
}

func TestCheck_KnowsPinnedKubectlFlags(t *testing.T) {
	bin := os.Getenv("KBRIDGE_KUBECTL")
	if bin == "" {
		t.Skip("KBRIDGE_KUBECTL not set")
	}
	known := slices.Concat(connectionFlags, fileFlags, []string{"-f", "--filename"}, benignFlags)
	re := regexp.MustCompile(`--[a-z0-9-]+`)
	for _, sub := range [][]string{{"options"}, {"apply", "--help"}} {
		out, err := exec.Command(bin, sub...).CombinedOutput()
		if err != nil {
			t.Fatalf("kubectl %v: %v", sub, err)
		}
		for _, f := range re.FindAllString(string(out), -1) {
			if mentionsRisk(f) && !slices.Contains(known, f) {
				t.Errorf("kubectl %v: flag %s is not in the check tables", sub, f)
			}
		}
	}
}

// benignFlags match the risk words but neither read files nor change the connection.
var benignFlags = []string{"--server-side", "--match-server-version", "--allow-missing-template-keys"}

func mentionsRisk(flag string) bool {
	for _, w := range []string{"file", "kustomize", "cert", "key", "server", "token", "kubeconfig"} {
		if strings.Contains(flag, w) {
			return true
		}
	}
	return false
}
