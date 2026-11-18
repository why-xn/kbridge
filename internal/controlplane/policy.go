package controlplane

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

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
	Name          string          `yaml:"name"`
	Rules         []PolicyRule    `yaml:"rules"`
	ClusterAccess []ClusterAccess `yaml:"clusterAccess"`
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
// subject pattern matches, or the default role when no binding matches.
func (p *Policy) rolesFor(subject string) []string {
	var roles []string
	matched := false
	for _, b := range p.Bindings {
		if matchPattern(b.Subject, subject) {
			roles = append(roles, b.Roles...)
			matched = true
		}
	}
	if !matched && p.Default != "" {
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
		if err := validateClusterAccess(r); err != nil {
			return err
		}
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

// policyPollInterval is how often Watch re-reads the policy file. Kubelet
// updates a ConfigMap mount by swapping a "..data" symlink, which fsnotify on
// the directory reports under that name rather than the policy file's.
var policyPollInterval = 10 * time.Second

// Watch reloads the policy whenever its file changes, until stop is closed:
// on a filesystem event for the file, and when a poll finds new content.
// Reload failures are logged and the previous policy is kept.
func (e *PolicyEngine) Watch(stop <-chan struct{}) {
	watcher := e.watchDir()
	go func() {
		var events <-chan fsnotify.Event
		var errs <-chan error
		if watcher != nil {
			defer watcher.Close()
			events, errs = watcher.Events, watcher.Errors
		}
		e.watchLoop(events, errs, stop)
	}()
}

// watchDir watches the file's directory so editor rename/replace writes are
// seen. It returns nil when events are unavailable; polling still runs.
func (e *PolicyEngine) watchDir() *fsnotify.Watcher {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("rbac file events disabled, polling only: %v", err)
		return nil
	}
	if err := watcher.Add(filepath.Dir(e.path)); err != nil {
		log.Printf("rbac file events disabled, polling only: %v", err)
		watcher.Close()
		return nil
	}
	return watcher
}

func (e *PolicyEngine) watchLoop(events <-chan fsnotify.Event, errs <-chan error, stop <-chan struct{}) {
	ticker := time.NewTicker(policyPollInterval)
	defer ticker.Stop()
	target := filepath.Clean(e.path)
	last := fileHash(e.path)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			if filepath.Clean(ev.Name) == target {
				last = e.reloadAndLog()
			}
		case <-ticker.C:
			if fileHash(e.path) != last {
				last = e.reloadAndLog()
			}
		case err, ok := <-errs:
			if !ok {
				return
			}
			log.Printf("rbac watch error: %v", err)
		case <-stop:
			return
		}
	}
}

// reloadAndLog reloads the policy and returns the hash of the content it read.
func (e *PolicyEngine) reloadAndLog() [sha256.Size]byte {
	h := fileHash(e.path)
	if err := e.Reload(); err != nil {
		log.Printf("rbac reload failed, keeping previous policy: %v", err)
	} else {
		log.Printf("rbac policy reloaded from %s", e.path)
	}
	return h
}

// fileHash returns the SHA-256 of the file, or the zero hash if unreadable.
func fileHash(path string) [sha256.Size]byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return [sha256.Size]byte{}
	}
	return sha256.Sum256(data)
}

// RolesFor returns the roles that apply to subject and whether they come from
// the default role. A nil engine (RBAC off) has none.
func (e *PolicyEngine) RolesFor(subject string) (roles []string, isDefault bool) {
	if e == nil {
		return nil, false
	}
	p := e.current.Load()
	roles = p.rolesFor(subject)
	if len(roles) == 1 && p.Default == roles[0] {
		isDefault = true
		for _, b := range p.Bindings {
			if matchPattern(b.Subject, subject) {
				isDefault = false
			}
		}
	}
	return roles, isDefault
}

// AccessSummary is one cluster-level line of what a subject may do.
type AccessSummary struct {
	Cluster    string   `json:"cluster"`    // known cluster name, or "*" for every cluster
	Namespaces []string `json:"namespaces"` // ["*"] means all namespaces
	Level      string   `json:"level"`      // "read-only" | "read-write"
}

var readOnlyVerbs = map[string]bool{"get": true, "list": true, "watch": true, "describe": true,
	"logs": true, "top": true, "explain": true, "version": true, "api-resources": true}

func ruleLevel(r PolicyRule) string {
	for _, v := range r.Verbs {
		if !readOnlyVerbs[strings.ToLower(strings.TrimSpace(v))] {
			return "read-write"
		}
	}
	return "read-only"
}

// AccessFor summarises what subject's roles allow, resolved against the known
// cluster names. A nil engine returns nil; no matching role returns empty.
func (e *PolicyEngine) AccessFor(subject string, clusters []string) []AccessSummary {
	if e == nil {
		return nil
	}
	p := e.current.Load()
	ns := accessSet{}
	for _, name := range p.rolesFor(subject) {
		for _, role := range p.Roles {
			if role.Name != name {
				continue
			}
			for _, r := range role.Rules {
				for _, c := range ruleClusters(r, clusters) {
					ns.add(c, r)
				}
			}
		}
	}
	return sortedAccess(ns)
}

// ruleClusters returns "*" for a rule on every cluster, else the known
// clusters it matches. A rule that can allow nothing yields none.
func ruleClusters(r PolicyRule, clusters []string) []string {
	if len(r.Namespaces) == 0 || len(r.Verbs) == 0 || len(r.Resources) == 0 {
		return nil
	}
	if anyEqual(r.Clusters, "*") {
		return []string{"*"}
	}
	var out []string
	for _, c := range clusters {
		if anyMatch(r.Clusters, c) {
			out = append(out, c)
		}
	}
	return out
}

type accessSet map[accessKey]map[string]bool

func (s accessSet) add(cluster string, r PolicyRule) {
	k := accessKey{cluster, ruleLevel(r)}
	if s[k] == nil {
		s[k] = map[string]bool{}
	}
	for _, n := range r.Namespaces {
		s[k][n] = true
	}
}

type accessKey struct{ cluster, level string }

func anyEqual(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// sortedAccess merges namespaces per (cluster, level) and orders the result:
// read-write first, then "*" before names, then by name.
func sortedAccess(ns accessSet) []AccessSummary {
	out := []AccessSummary{}
	for k, set := range ns {
		names := []string{}
		for n := range set {
			names = append(names, n)
		}
		sort.Strings(names)
		if set["*"] {
			names = []string{"*"}
		}
		out = append(out, AccessSummary{k.cluster, names, k.level})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Level != b.Level {
			return a.Level == "read-write"
		}
		if (a.Cluster == "*") != (b.Cluster == "*") {
			return a.Cluster == "*"
		}
		return a.Cluster < b.Cluster
	})
	return out
}
