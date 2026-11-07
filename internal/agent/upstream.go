package agent

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadUpstream builds the window's upstream from a kubeconfig when one is set,
// else from the in-cluster server, CA and serviceaccount token file.
func LoadUpstream(cfg KubernetesConfig) (Upstream, error) {
	if cfg.Kubeconfig != "" {
		return loadKubeconfig(cfg.Kubeconfig)
	}
	pool, err := caPool(cfg.CAFile)
	if err != nil {
		return Upstream{}, err
	}
	return Upstream{Server: cfg.Server, CA: pool, TokenFile: cfg.TokenFile}, nil
}

// ownNamespace returns the agent's namespace: the serviceaccount namespace file
// next to TokenFile, else "".
func ownNamespace(tokenFile string) string {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(tokenFile), "namespace"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

type kubeconfigFile struct {
	CurrentContext string `yaml:"current-context"`
	Contexts       []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster string `yaml:"cluster"`
			User    string `yaml:"user"`
		} `yaml:"context"`
	} `yaml:"contexts"`
	Clusters []struct {
		Name    string      `yaml:"name"`
		Cluster kubeCluster `yaml:"cluster"`
	} `yaml:"clusters"`
	Users []struct {
		Name string   `yaml:"name"`
		User kubeUser `yaml:"user"`
	} `yaml:"users"`
}

type kubeCluster struct {
	Server   string `yaml:"server"`
	CAFile   string `yaml:"certificate-authority"`
	CAData   string `yaml:"certificate-authority-data"`
	Insecure bool   `yaml:"insecure-skip-tls-verify"`
}

type kubeUser struct {
	Token        string         `yaml:"token"`
	TokenFile    string         `yaml:"tokenFile"`
	CertFile     string         `yaml:"client-certificate"`
	CertData     string         `yaml:"client-certificate-data"`
	KeyFile      string         `yaml:"client-key"`
	KeyData      string         `yaml:"client-key-data"`
	Exec         map[string]any `yaml:"exec"`
	AuthProvider map[string]any `yaml:"auth-provider"`
}

// current resolves the current context to its cluster and user names.
func (kc *kubeconfigFile) current() (clusterName, userName string, err error) {
	for _, c := range kc.Contexts {
		if c.Name == kc.CurrentContext {
			return c.Context.Cluster, c.Context.User, nil
		}
	}
	return "", "", fmt.Errorf("kubeconfig current context %q not found", kc.CurrentContext)
}

// loadKubeconfig reads only what the window needs from a kubeconfig: current
// context's cluster server and CA (file or -data), and its user's token or client
// cert/key (file or -data). Exec plugins and auth-provider are not supported.
// shortcut: no exec/auth-provider kubeconfig support, add if a cluster needs it
func loadKubeconfig(path string) (Upstream, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Upstream{}, fmt.Errorf("reading kubeconfig: %w", err)
	}
	var kc kubeconfigFile
	if err := yaml.Unmarshal(b, &kc); err != nil {
		return Upstream{}, fmt.Errorf("parsing kubeconfig: %w", err)
	}
	cn, un, err := kc.current()
	if err != nil {
		return Upstream{}, err
	}
	up, err := kc.clusterUpstream(cn, filepath.Dir(path))
	if err != nil {
		return Upstream{}, err
	}
	return up, kc.applyUser(&up, un, filepath.Dir(path))
}

func (kc *kubeconfigFile) clusterUpstream(name, dir string) (Upstream, error) {
	for _, c := range kc.Clusters {
		if c.Name != name {
			continue
		}
		if c.Cluster.Insecure {
			return Upstream{}, fmt.Errorf("kubeconfig cluster %s: insecure-skip-tls-verify is not supported", name)
		}
		data, err := fileOrData(c.Cluster.CAFile, c.Cluster.CAData, dir)
		if err != nil {
			return Upstream{}, fmt.Errorf("kubeconfig cluster %s: %w", name, err)
		}
		if data == nil {
			return Upstream{}, fmt.Errorf("kubeconfig cluster %s has no usable certificate authority", name)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return Upstream{}, fmt.Errorf("kubeconfig cluster %s: no certificates in certificate authority", name)
		}
		return Upstream{Server: c.Cluster.Server, CA: pool}, nil
	}
	return Upstream{}, fmt.Errorf("kubeconfig cluster %q not found", name)
}

func (kc *kubeconfigFile) applyUser(up *Upstream, name, dir string) error {
	for _, u := range kc.Users {
		if u.Name == name {
			return u.User.apply(up, name, dir)
		}
	}
	return fmt.Errorf("kubeconfig user %q not found", name)
}

func (u kubeUser) apply(up *Upstream, name, dir string) (err error) {
	switch {
	case u.Exec != nil || u.AuthProvider != nil:
		return fmt.Errorf("kubeconfig user %s: exec and auth-provider are not supported", name)
	case u.TokenFile != "":
		up.TokenFile = resolvePath(u.TokenFile, dir)
	case u.Token != "":
		up.TokenFile, err = writeTempToken(u.Token)
	case u.CertFile != "" || u.CertData != "":
		up.ClientCert, err = u.clientCert(dir)
	default:
		return fmt.Errorf("kubeconfig user %s has no token or client certificate", name)
	}
	return err
}

func (u kubeUser) clientCert(dir string) (*tls.Certificate, error) {
	cert, err := fileOrData(u.CertFile, u.CertData, dir)
	if err != nil {
		return nil, err
	}
	key, err := fileOrData(u.KeyFile, u.KeyData, dir)
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig client certificate: %w", err)
	}
	return &pair, nil
}

// writeTempToken stores an inline kubeconfig token (mode 0600) because the
// window re-reads its token file per request. The file lives in os.TempDir()
// for the life of the process; the caller owns removing it.
func writeTempToken(token string) (string, error) {
	f, err := os.CreateTemp("", "kbridge-token-*")
	if err != nil {
		return "", fmt.Errorf("creating token file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(token); err != nil {
		return "", fmt.Errorf("writing token file: %w", err)
	}
	return f.Name(), nil
}

func resolvePath(p, dir string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

// fileOrData returns the file's bytes, else the base64 -data bytes, else nil.
func fileOrData(file, data, dir string) ([]byte, error) {
	if file != "" {
		return os.ReadFile(resolvePath(file, dir))
	}
	if data == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(data)
}

func caPool(file string) (*x509.CertPool, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading ca file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b) {
		return nil, errors.New("no certificates in ca file")
	}
	return pool, nil
}
