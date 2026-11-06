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
	fileMsg = "flag %s is not allowed: the agent does not read local files; build the object locally with 'kubectl create ... --dry-run=client -o yaml' and send it with 'kb apply -f -'"
	fMsg    = `-f only accepts "-" on the agent: the kb CLI uploads local files and URLs`
	verbMsg = "kubectl %s is not supported through kbridge"
	termMsg = "flag %s must be written as %s=<value> before --"
	leadMsg = "flag %s must come after the command or be written as %s=<value>"
)

func sprintf(f, a string) string { return strings.Replace(f, "%s", a, 1) }

func term(flag string) string { return strings.ReplaceAll(termMsg, "%s", flag) }

func lead(flag string) string { return strings.ReplaceAll(leadMsg, "%s", flag) }

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
		{"-v 4 cp /var/run/kbridge/sa/token default/x:/tmp/t", sprintf(verbMsg, "cp")},
		{"--request-timeout 5s config view", sprintf(verbMsg, "config")},
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
		// a value flag swallows "--", so the flags after it are live to kubectl
		{"create configmap x --dry-run=client -o yaml --field-manager -- --from-file=/var/run/kbridge/sa/token",
			sprintf(fileMsg, "--from-file")},
		{"apply --field-manager -- -f /var/run/kbridge/sa/token", fMsg},
		{"patch cm x --subresource -- --patch-file=/var/run/kbridge/sa/token", sprintf(fileMsg, "--patch-file")},
		{"debug node/x --profile -- --custom=/var/run/kbridge/sa/token", sprintf(fileMsg, "--profile")},
		{"debug node/x --image -- --custom=/var/run/kbridge/sa/token", term("--image")},
		{"run x --image=busybox --overrides -- sh", term("--overrides")},
		{"exec web -c -- --kubeconfig=/k sh", term("-c")},
		{"exec web -itn -- --server=x sh", term("-n")},
		{"exec web -v -- --token=x sh", term("-v")},
		{"-- cp /var/run/kbridge/sa/token default/x:/tmp/t", sprintf(verbMsg, "cp")},
		// cobra lets an unknown flag before the verb take the next token, so the verb would hide behind it
		{"--retries 5 cp /var/run/kbridge/sa/token default/x:/tmp/t", lead("--retries")},
		{"--port 8001 proxy", lead("--port")},
		{"--retries=5 cp a b", sprintf(verbMsg, "cp")},
		{"--unknown-flag get pods", lead("--unknown-flag")},
		{"-p 8001 proxy", sprintf(verbMsg, "proxy")},
		{"-c 5 cp /var/run/kbridge/sa/token default/x:/tmp/t", sprintf(verbMsg, "cp")},
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
		"-v 4 get pods", "--request-timeout=5s get pods",
		"exec web -c app -- sh", "exec --stdin --tty web -- sh", "exec web --stdin -- sh",
		"run x --image=busybox --rm -it -- sh", "run x --image busybox -- sh",
		"debug node/x --image=busybox -it -- sh",
		"get pods -- name",
		"--namespace dev get pods", "--warnings-as-errors get pods", "get pods --retries 5",
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
	assertBoolFlagsBoolean(t, bin)
	for _, sub := range [][]string{{"options"}, {"apply", "--help"}} {
		out, err := exec.Command(bin, sub...).CombinedOutput()
		if err != nil {
			t.Fatalf("kubectl %v: %v", sub, err)
		}
		if sub[0] == "options" {
			assertValueOptionsSkipped(t, string(out))
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

var optionLine = regexp.MustCompile(`(?m)^\s+(?:(-\w), )?(--[\w-]+)=(.*?):`)

// assertValueOptionsSkipped fails when a global option is not classified as kubectl classifies it,
// or when verbOf does not skip a value-taking option's value.
func assertValueOptionsSkipped(t *testing.T, options string) {
	t.Helper()
	for _, m := range optionLine.FindAllStringSubmatch(options, -1) {
		if m[3] == "true" || m[3] == "false" {
			if !slices.Contains(globalBoolFlags, m[2]) {
				t.Errorf("global boolean option %s is not in globalBoolFlags", m[2])
			}
			continue
		}
		if !slices.Contains(globalValueFlags, m[2]) {
			t.Errorf("global value option %s is not in globalValueFlags", m[2])
		}
		for _, name := range []string{m[1], m[2]} {
			if verb, _ := verbOf([]string{name, "VALUE", "get"}); name != "" && verb != "get" {
				t.Errorf("verbOf does not skip the value of %s", name)
			}
		}
	}
}

// assertBoolFlagsBoolean fails when a flag the check treats as boolean takes a value at the pinned kubectl.
func assertBoolFlagsBoolean(t *testing.T, bin string) {
	t.Helper()
	for verb, flags := range boolFlags {
		out, err := exec.Command(bin, verb, "--help").CombinedOutput()
		if err != nil {
			t.Fatalf("kubectl %s --help: %v", verb, err)
		}
		var bools []string
		for _, m := range optionLine.FindAllStringSubmatch(string(out), -1) {
			if m[3] == "true" || m[3] == "false" {
				bools = append(bools, m[1], m[2])
			}
		}
		for _, f := range flags {
			if !slices.Contains(bools, f) {
				t.Errorf("kubectl %s: flag %s is not boolean", verb, f)
			}
		}
	}
}
