package central

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// PolicyRule grants a set of verbs on resources within clusters/namespaces.
// Each field is a list of glob patterns ('*' wildcard); a request must match at
// least one entry in every field.
type PolicyRule struct {
	Clusters   []string `yaml:"clusters"`
	Namespaces []string `yaml:"namespaces"`
	Resources  []string `yaml:"resources"`
	Verbs      []string `yaml:"verbs"`
}

// PolicyRole is a named collection of rules.
type PolicyRole struct {
	Name  string       `yaml:"name"`
	Rules []PolicyRule `yaml:"rules"`
}

// PolicyBinding maps a subject (matched against the JWT email, may contain
// wildcards) to one or more role names.
type PolicyBinding struct {
	Subject string   `yaml:"subject"`
	Roles   []string `yaml:"roles"`
}

// Policy is a parsed RBAC policy document.
type Policy struct {
	Default  string          `yaml:"default"`
	Roles    []PolicyRole    `yaml:"roles"`
	Bindings []PolicyBinding `yaml:"bindings"`
}

// allows reports whether the rule grants the requested access.
func (r PolicyRule) allows(req AccessRequest) bool {
	return anyMatch(r.Clusters, req.Cluster) &&
		anyMatch(r.Namespaces, req.Namespace) &&
		anyMatch(r.Resources, req.Resource) &&
		anyVerb(r.Verbs, req.Verb)
}

// rolesFor returns the role names that apply to subject: every binding whose
// subject pattern matches, plus the default role when set.
func (p *Policy) rolesFor(subject string) []string {
	var roles []string
	for _, b := range p.Bindings {
		if matchPattern(b.Subject, subject) {
			roles = append(roles, b.Roles...)
		}
	}
	if p.Default != "" {
		roles = append(roles, p.Default)
	}
	return roles
}

// allows reports whether subject may perform req under this policy.
func (p *Policy) allows(subject string, req AccessRequest) bool {
	active := make(map[string]bool)
	for _, name := range p.rolesFor(subject) {
		active[name] = true
	}
	for _, role := range p.Roles {
		if !active[role.Name] {
			continue
		}
		for _, rule := range role.Rules {
			if rule.allows(req) {
				return true
			}
		}
	}
	return false
}

// ParsePolicy parses and validates a YAML policy document.
func ParsePolicy(data []byte) (*Policy, error) {
	var p Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing policy: %w", err)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// validate checks that role names are unique and that all referenced roles
// (bindings and default) are defined.
func (p *Policy) validate() error {
	defined := make(map[string]bool, len(p.Roles))
	for _, r := range p.Roles {
		if r.Name == "" {
			return fmt.Errorf("policy role missing name")
		}
		if defined[r.Name] {
			return fmt.Errorf("duplicate policy role %q", r.Name)
		}
		defined[r.Name] = true
	}
	for _, b := range p.Bindings {
		for _, name := range b.Roles {
			if !defined[name] {
				return fmt.Errorf("binding for %q references unknown role %q", b.Subject, name)
			}
		}
	}
	if p.Default != "" && !defined[p.Default] {
		return fmt.Errorf("default references unknown role %q", p.Default)
	}
	return nil
}

func anyMatch(patterns []string, value string) bool {
	for _, p := range patterns {
		if matchPattern(p, value) {
			return true
		}
	}
	return false
}

func anyVerb(verbs []string, verb string) bool {
	for _, v := range verbs {
		if v == "*" || strings.EqualFold(strings.TrimSpace(v), verb) {
			return true
		}
	}
	return false
}

// PolicyEngine holds the active policy and supports lock-free hot-swapping.
type PolicyEngine struct {
	current atomic.Pointer[Policy]
	path    string
}

// NewPolicyEngineFromFile loads a policy from path into a new engine.
func NewPolicyEngineFromFile(path string) (*PolicyEngine, error) {
	policy, err := loadPolicyFile(path)
	if err != nil {
		return nil, err
	}
	e := &PolicyEngine{path: path}
	e.current.Store(policy)
	return e, nil
}

func loadPolicyFile(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading policy file: %w", err)
	}
	return ParsePolicy(data)
}

// Allows reports whether subject may perform req under the current policy.
func (e *PolicyEngine) Allows(subject string, req AccessRequest) bool {
	return e.current.Load().allows(subject, req)
}

// Reload re-reads the policy file and atomically swaps it in. On error the
// current policy is left unchanged.
func (e *PolicyEngine) Reload() error {
	policy, err := loadPolicyFile(e.path)
	if err != nil {
		return err
	}
	e.current.Store(policy)
	return nil
}

// Watch reloads the policy whenever its file changes, until stop is closed.
// It watches the containing directory so atomic editor rename/replace writes
// are still observed. Reload failures are logged and the previous policy is
// kept.
func (e *PolicyEngine) Watch(stop <-chan struct{}) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("rbac watch disabled: %v", err)
		return
	}
	dir := filepath.Dir(e.path)
	if err := watcher.Add(dir); err != nil {
		log.Printf("rbac watch disabled: %v", err)
		watcher.Close()
		return
	}

	go func() {
		defer watcher.Close()
		target := filepath.Clean(e.path)
		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Clean(ev.Name) != target {
					continue
				}
				if err := e.Reload(); err != nil {
					log.Printf("rbac reload failed, keeping previous policy: %v", err)
				} else {
					log.Printf("rbac policy reloaded from %s", e.path)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("rbac watch error: %v", err)
			case <-stop:
				return
			}
		}
	}()
}
