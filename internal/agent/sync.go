package agent

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DefaultSyncInterval is how often the agent re-checks bindings with no other trigger.
const DefaultSyncInterval = 60 * time.Second

const (
	syncDebounce     = 2 * time.Second
	watchRetry       = 5 * time.Second
	reconcileTimeout = time.Minute
)

var (
	dnsSubdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
	dnsLabel     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// validNames refuses a name or namespace that is not a DNS-1123 subdomain or
// label; names from the control plane are untrusted and end up in URL paths.
func validNames(b binding) error {
	if len(b.Name) > 253 || !dnsSubdomain.MatchString(b.Name) {
		return fmt.Errorf("binding %q: name is not a valid kubernetes name", b.Name)
	}
	if b.Namespace != "" && (len(b.Namespace) > 63 || !dnsLabel.MatchString(b.Namespace)) {
		return fmt.Errorf("binding %s: namespace %q is not a valid kubernetes namespace", b.Name, b.Namespace)
	}
	return nil
}

// writable is the gate before any write: a valid kbridge- name outside the
// protected namespaces. With the label check it keeps a compromised control
// plane off bindings kbridge does not own.
func writable(b binding, protected []string) error {
	if err := validNames(b); err != nil {
		return err
	}
	if !strings.HasPrefix(b.Name, "kbridge-") {
		return fmt.Errorf("binding %s: name must start with kbridge-", b.Name)
	}
	if b.Namespace != "" && slices.Contains(protected, b.Namespace) {
		return fmt.Errorf("binding %s: namespace %q is reserved for the kbridge agent", b.Name, b.Namespace)
	}
	return nil
}

// checkBinding is writable plus what a desired binding must hold: a role and
// only kbridge users as subjects.
func checkBinding(b binding, protected []string) error {
	if err := writable(b, protected); err != nil {
		return err
	}
	if b.ClusterRole == "" {
		return fmt.Errorf("binding %s: cluster role is empty", b.Name)
	}
	for _, u := range b.Users {
		if !strings.HasPrefix(u, "kbridge:") {
			return fmt.Errorf("binding %s: user %q is not a kbridge user", b.Name, u)
		}
	}
	return nil
}

// reconcile makes the cluster match desired: creates or updates every desired
// binding, deletes managed ones not desired, skips protected namespaces, and
// returns one error string per binding the cluster refused.
func reconcile(ctx context.Context, k *kubeAPI, desired []binding, protected []string) []string {
	var errs []string
	existing, err := k.listManaged(ctx)
	if err != nil {
		errs = append(errs, err.Error())
	}
	for _, b := range desired {
		errs = appendErr(errs, applyOne(ctx, k, b, protected))
	}
	for _, b := range stale(existing, desired) {
		errs = appendErr(errs, pruneOne(ctx, k, b, protected))
	}
	return errs
}

func appendErr(errs []string, err error) []string {
	if err != nil {
		return append(errs, err.Error())
	}
	return errs
}

func applyOne(ctx context.Context, k *kubeAPI, b binding, protected []string) error {
	if err := checkBinding(b, protected); err != nil {
		return err
	}
	if err := checkPodSecurity(ctx, k, b); err != nil {
		return err
	}
	if err := k.apply(ctx, b); err != nil {
		return fmt.Errorf("binding %s: %w", b.Name, err)
	}
	return nil
}

// needsPodSecurity reports whether role can create pods; only view cannot.
func needsPodSecurity(clusterRole string) bool {
	return clusterRole != "view"
}

// checkPodSecurity refuses a namespaced binding that can create pods unless
// the namespace enforces baseline or restricted: a hostPath pod would read
// the agent's token off the node. Cluster-wide bindings are a documented limit.
func checkPodSecurity(ctx context.Context, k *kubeAPI, b binding) error {
	if b.Namespace == "" || !needsPodSecurity(b.ClusterRole) {
		return nil
	}
	level, err := k.namespaceEnforce(ctx, b.Namespace)
	if err != nil {
		return fmt.Errorf("binding %s: reading namespace %q: %w", b.Name, b.Namespace, err)
	}
	if level != "baseline" && level != "restricted" {
		return fmt.Errorf("binding %s: namespace %q must enforce pod-security baseline or restricted before %s can be granted there (kubectl label ns %s pod-security.kubernetes.io/enforce=baseline)",
			b.Name, b.Namespace, b.ClusterRole, b.Namespace)
	}
	return nil
}

func pruneOne(ctx context.Context, k *kubeAPI, b binding, protected []string) error {
	if err := writable(b, protected); err != nil {
		return err
	}
	if !b.Managed {
		return nil
	}
	if err := k.delete(ctx, b); err != nil {
		return fmt.Errorf("binding %s: %w", b.Name, err)
	}
	return nil
}

// stale returns the existing bindings no desired binding names.
func stale(existing, desired []binding) []binding {
	want := map[[2]string]bool{}
	for _, b := range desired {
		want[[2]string{b.Namespace, b.Name}] = true
	}
	var out []binding
	for _, b := range existing {
		if !want[[2]string{b.Namespace, b.Name}] {
			out = append(out, b)
		}
	}
	return out
}

func desiredFrom(pb []*agentpb.DesiredBinding) []binding {
	out := make([]binding, 0, len(pb))
	for _, d := range pb {
		out = append(out, binding{Name: d.GetName(), Namespace: d.GetNamespace(), ClusterRole: d.GetClusterRole(), Users: d.GetUsers()})
	}
	return out
}

// syncer runs reconciles one at a time, on start, on trigger, every interval,
// and shortly after the cluster's managed bindings change.
type syncer struct {
	api       *kubeAPI
	protected []string
	interval  time.Duration
	client    func() (agentpb.AgentServiceClient, string)
	kick      chan struct{}

	mu       sync.Mutex
	debounce *time.Timer
}

func newSyncer(api *kubeAPI, protected []string, interval time.Duration, client func() (agentpb.AgentServiceClient, string)) *syncer {
	if interval <= 0 {
		interval = DefaultSyncInterval
	}
	return &syncer{api: api, protected: protected, interval: interval, client: client, kick: make(chan struct{}, 1)}
}

// trigger asks for a reconcile; triggers while one is pending collapse into it.
func (s *syncer) trigger() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// onWatchEvent triggers a reconcile once events have been quiet for syncDebounce.
func (s *syncer) onWatchEvent() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.debounce == nil {
		s.debounce = time.AfterFunc(syncDebounce, s.trigger)
		return
	}
	s.debounce.Reset(syncDebounce)
}

// run blocks until ctx is done, then waits for the watch to stop.
func (s *syncer) run(ctx context.Context) {
	watchDone := make(chan struct{})
	go func() { defer close(watchDone); s.watchLoop(ctx) }()
	defer func() { <-watchDone }()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	s.trigger()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.once(ctx)
		case <-s.kick:
			s.once(ctx)
		}
	}
}

func (s *syncer) watchLoop(ctx context.Context) {
	for {
		if err := s.api.watchManaged(ctx, s.onWatchEvent); err != nil && ctx.Err() == nil {
			log.Printf("Binding watch failed, retrying in %s: %v", watchRetry, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(watchRetry):
		}
	}
}

// once pulls the desired bindings, reconciles, and reports the outcome.
func (s *syncer) once(ctx context.Context) {
	client, agentID := s.client()
	if client == nil || agentID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()
	resp, err := client.GetDesiredBindings(ctx, &agentpb.GetDesiredBindingsRequest{AgentId: agentID})
	if err != nil {
		if status.Code(err) != codes.Unimplemented { // control plane predates rbac-sync
			log.Printf("Fetching desired bindings failed: %v", err)
		}
		return
	}
	errs := reconcile(ctx, s.api, desiredFrom(resp.GetBindings()), s.protected)
	s.report(ctx, client, agentID, resp.GetVersion(), errs)
}

func (s *syncer) report(ctx context.Context, client agentpb.AgentServiceClient, agentID, version string, errs []string) {
	if len(errs) > 0 {
		log.Printf("Binding sync %s: %s", version, strings.Join(errs, "; "))
	}
	req := &agentpb.ReportBindingSyncRequest{AgentId: agentID, Version: version, InSync: len(errs) == 0, Errors: errs}
	if _, err := client.ReportBindingSync(ctx, req); err != nil {
		log.Printf("Reporting binding sync failed: %v", err)
	}
}
