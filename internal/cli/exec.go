package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/viper"
	"github.com/why-xn/kbridge/internal/execframe"
	"golang.org/x/net/http2"
	"golang.org/x/term"
)

type execTarget struct {
	namespace string
	pod       string
	container string
	command   []string
	tty       bool
	stdin     bool
}

// parseExecArgs recognizes an interactive `exec` (has -i and/or -t). It returns
// ok=false for non-interactive exec (handled by the one-shot path) and non-exec.
func parseExecArgs(args []string) (execTarget, bool) {
	if len(args) == 0 || args[0] != "exec" {
		return execTarget{}, false
	}
	var tgt execTarget
	rest := args[1:]
	var positional []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--":
			tgt.command = rest[i+1:]
			i = len(rest)
		case a == "-i" || a == "--stdin":
			tgt.stdin = true
		case a == "-t" || a == "--tty":
			tgt.tty = true
		case a == "-it" || a == "-ti":
			tgt.stdin, tgt.tty = true, true
		case a == "-n" || a == "--namespace":
			if i+1 < len(rest) {
				tgt.namespace = rest[i+1]
				i++
			}
		case strings.HasPrefix(a, "--namespace="):
			tgt.namespace = strings.TrimPrefix(a, "--namespace=")
		case a == "-c" || a == "--container":
			if i+1 < len(rest) {
				tgt.container = rest[i+1]
				i++
			}
		case strings.HasPrefix(a, "-c="):
			tgt.container = strings.TrimPrefix(a, "-c=")
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) > 0 {
		tgt.pod = positional[0]
	}
	if !tgt.stdin && !tgt.tty {
		return execTarget{}, false // non-interactive: let the one-shot path handle it
	}
	return tgt, true
}

func httpStatusError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("authentication required, run kb login")
	case http.StatusForbidden:
		return fmt.Errorf("permission denied")
	case http.StatusNotFound:
		return fmt.Errorf("cluster not found")
	case http.StatusServiceUnavailable:
		return fmt.Errorf("cluster agent is disconnected")
	case http.StatusTooManyRequests:
		return fmt.Errorf("too many concurrent sessions")
	default:
		return fmt.Errorf("exec failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
}

// refreshTokenViaHTTP exchanges a refresh token for a new access/refresh pair.
// It is used by the streaming connect paths that bypass CentralClient.doRequest.
func refreshTokenViaHTTP(centralURL, refreshToken string, insecure bool) (newAccess, newRefresh string, err error) {
	body, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	req, err := http.NewRequest(http.MethodPost, centralURL+"/auth/refresh", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("creating refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec
	}
	c := &http.Client{Transport: tr}
	resp, err := c.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("refresh failed with status %d", resp.StatusCode)
	}
	var lr LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return "", "", fmt.Errorf("decoding refresh response: %w", err)
	}
	return lr.AccessToken, lr.RefreshToken, nil
}

// runExecInteractive opens the bidi exec stream and bridges the local terminal.
func runExecInteractive(centralURL, cluster, token string, tgt execTarget, insecure bool) error {
	if tgt.pod == "" {
		return fmt.Errorf("exec requires a pod name")
	}

	rows, cols := uint16(24), uint16(80)
	stdinFd := int(os.Stdin.Fd())
	isTTY := tgt.tty && term.IsTerminal(stdinFd)
	if isTTY {
		if w, h, err := term.GetSize(stdinFd); err == nil {
			cols, rows = uint16(w), uint16(h)
		}
	}

	reqURL := execURL(centralURL, cluster, tgt, rows, cols)
	client, err := http2Client(centralURL, insecure)
	if err != nil {
		return err
	}

	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, reqURL, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connecting: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		refreshToken := viper.GetString(ConfigKeyRefreshToken)
		if refreshToken != "" {
			newAccess, newRefresh, rerr := refreshTokenViaHTTP(centralURL, refreshToken, insecure)
			if rerr == nil {
				viper.Set(ConfigKeyToken, newAccess)
				viper.Set(ConfigKeyRefreshToken, newRefresh)
				_ = saveConfig()
				token = newAccess

				// Rebuild the pipe and request for the retry.
				pr, pw = io.Pipe()
				req2, err2 := http.NewRequest(http.MethodPost, reqURL, pr)
				if err2 != nil {
					return err2
				}
				req2.Header.Set("Authorization", "Bearer "+token)
				resp, err = client.Do(req2)
				if err != nil {
					return fmt.Errorf("connecting: %w", err)
				}
			}
		}
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpStatusError(resp)
	}

	// Raw mode only after a successful 200, restored on every exit path.
	var restore func()
	if isTTY {
		old, err := term.MakeRaw(stdinFd)
		if err == nil {
			restore = func() { _ = term.Restore(stdinFd, old) }
			defer restore()
		}
	}
	stopSig := make(chan os.Signal, 1)
	notifyStopSignals(stopSig)
	go func() {
		<-stopSig
		if restore != nil {
			restore()
		}
		os.Exit(1)
	}()

	// stdin -> STDIN frames
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := os.Stdin.Read(buf)
			if n > 0 {
				if werr := execframe.Encode(pw, execframe.Stdin, buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				_ = pw.Close() // half-close request: send EOF to remote stdin
				return
			}
		}
	}()

	// SIGWINCH -> RESIZE frames
	if isTTY {
		winch := make(chan os.Signal, 1)
		notifyWinchSignals(winch)
		go func() {
			for range winch {
				if w, h, err := term.GetSize(stdinFd); err == nil {
					_ = execframe.Encode(pw, execframe.Resize, execframe.EncodeResize(uint16(h), uint16(w)))
				}
			}
		}()
	}

	// response frames -> stdout/stderr; EXIT ends the session
	for {
		t, payload, derr := execframe.Decode(resp.Body)
		if derr != nil {
			return nil // stream closed
		}
		switch t {
		case execframe.Stdout:
			os.Stdout.Write(payload) //nolint:errcheck
		case execframe.Stderr:
			os.Stderr.Write(payload) //nolint:errcheck
		case execframe.Exit:
			code, msg, _ := execframe.DecodeExit(payload)
			if restore != nil {
				restore()
			}
			if msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
			if code != 0 {
				os.Exit(int(code))
			}
			return nil
		}
	}
}

func execURL(centralURL, cluster string, tgt execTarget, rows, cols uint16) string {
	q := url.Values{}
	q.Set("pod", tgt.pod)
	if tgt.container != "" {
		q.Set("container", tgt.container)
	}
	if tgt.namespace != "" {
		q.Set("namespace", tgt.namespace)
	}
	for _, a := range tgt.command {
		q.Add("command", a)
	}
	if tgt.tty {
		q.Set("tty", "true")
	}
	q.Set("rows", strconv.Itoa(int(rows)))
	q.Set("cols", strconv.Itoa(int(cols)))
	return fmt.Sprintf("%s/api/v1/clusters/%s/exec/attach?%s", centralURL, url.PathEscape(cluster), q.Encode())
}

// http2Client returns a client that forces HTTP/2 over the central URL scheme.
func http2Client(centralURL string, insecure bool) (*http.Client, error) {
	u, err := url.Parse(centralURL)
	if err != nil {
		return nil, fmt.Errorf("parsing central url: %w", err)
	}
	if u.Scheme == "https" {
		return &http.Client{Transport: &http2.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec
		}}, nil
	}
	// cleartext h2c
	return &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}, nil
}

// execInteractiveFromConfig reads viper config and delegates to runExecInteractive.
func execInteractiveFromConfig(tgt execTarget) error {
	centralURL := viper.GetString(ConfigKeyCentralURL)
	if centralURL == "" {
		return fmt.Errorf("central URL not configured. Run 'kb login' first")
	}
	currentCluster := viper.GetString(ConfigKeyCurrentCluster)
	if currentCluster == "" {
		return fmt.Errorf("no cluster selected. Run 'kb clusters use <name>' first")
	}
	token := viper.GetString(ConfigKeyToken)
	insecure := viper.GetBool(ConfigKeyInsecure)
	return runExecInteractive(centralURL, currentCluster, token, tgt, insecure)
}
