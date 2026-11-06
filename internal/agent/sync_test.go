package agent

import (
	"context"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func rb(ns, name, role string, users ...string) binding {
	return binding{Name: name, Namespace: ns, ClusterRole: role, Users: users}
}

func usersOf(o rbObject) []string {
	var out []string
	for _, s := range o.Subjects {
		out = append(out, s.Name)
	}
	return out
}

func TestReconcile_CreatesUpdatesDeletes(t *testing.T) {
	f, k := newFakeKube(t)
	f.put(rb("team-a", "kbridge-dev-view", "view", "kbridge:old@x"), true) // B, stale users
	f.put(rb("team-a", "kbridge-gone-view", "view", "kbridge:c@x"), true)  // C, no longer desired
	f.put(rb("team-a", "kbridge-handmade", "view", "someone"), false)      // unlabelled
	f.put(rb("team-a", "ops-binding", "edit", "ops"), false)               // unlabelled, unprefixed
	desired := []binding{
		rb("", "kbridge-admins-view", "view", "kbridge:a@x"),                   // A, cluster-wide
		rb("team-a", "kbridge-dev-view", "view", "kbridge:a@x", "kbridge:b@x"), // B
	}
	if errs := reconcile(context.Background(), k, desired, []string{"kbridge"}); len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	if _, ok := f.get("", "kbridge-admins-view"); !ok {
		t.Error("A not created")
	}
	if b, _ := f.get("team-a", "kbridge-dev-view"); !slices.Equal(usersOf(b), []string{"kbridge:a@x", "kbridge:b@x"}) {
		t.Errorf("B users = %v", usersOf(b))
	}
	if _, ok := f.get("team-a", "kbridge-gone-view"); ok {
		t.Error("C not deleted")
	}
	for _, n := range []string{"kbridge-handmade", "ops-binding"} {
		if _, ok := f.get("team-a", n); !ok {
			t.Errorf("unlabelled %s was deleted", n)
		}
	}
	want := []string{"POST /kbridge-admins-view", "PUT team-a/kbridge-dev-view", "DELETE team-a/kbridge-gone-view"}
	if got := f.writeLog(); !slices.Equal(got, want) {
		t.Errorf("writes = %v, want %v", got, want)
	}
}

func TestReconcile_NoWritesWhenInSync(t *testing.T) {
	f, k := newFakeKube(t)
	desired := []binding{rb("team-a", "kbridge-dev-view", "view", "kbridge:a@x")}
	reconcile(context.Background(), k, desired, nil)
	before := len(f.writeLog())
	if errs := reconcile(context.Background(), k, desired, nil); len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	if got := f.writeLog()[before:]; len(got) != 0 {
		t.Errorf("second reconcile wrote %v", got)
	}
}

func TestReconcile_SkipsProtectedNamespace(t *testing.T) {
	f, k := newFakeKube(t)
	f.put(rb("kbridge", "kbridge-stale-view", "view", "kbridge:a@x"), true)
	desired := []binding{rb("kbridge", "kbridge-dev-view", "view", "kbridge:a@x")}
	errs := reconcile(context.Background(), k, desired, []string{"kbridge"})
	want := `binding kbridge-dev-view: namespace "kbridge" is reserved for the kbridge agent`
	if !slices.Contains(errs, want) {
		t.Errorf("errors = %v, want %q", errs, want)
	}
	if got := f.writeLog(); len(got) != 0 {
		t.Errorf("writes in protected namespace: %v", got)
	}
}

func TestReconcile_ReportsCeiling(t *testing.T) {
	f, k := newFakeKube(t)
	desired := []binding{
		rb("", "kbridge-root-cluster-admin", "cluster-admin", "kbridge:a@x"),
		rb("team-a", "kbridge-dev-view", "view", "kbridge:a@x"),
	}
	errs := reconcile(context.Background(), k, desired, nil)
	if len(errs) != 1 || !strings.Contains(errs[0], "403") || !strings.HasPrefix(errs[0], "binding kbridge-root-cluster-admin: ") {
		t.Errorf("errors = %v, want one 403 for kbridge-root-cluster-admin", errs)
	}
	if _, ok := f.get("team-a", "kbridge-dev-view"); !ok {
		t.Error("allowed binding not applied")
	}
}

func TestReconcile_ListForbiddenIsOneError(t *testing.T) {
	f, k := newFakeKube(t)
	f.forbidC = true
	desired := []binding{rb("team-a", "kbridge-dev-view", "view", "kbridge:a@x")}
	errs := reconcile(context.Background(), k, desired, nil)
	if len(errs) != 1 || !strings.Contains(errs[0], "403") {
		t.Errorf("errors = %v, want one 403", errs)
	}
	if _, ok := f.get("team-a", "kbridge-dev-view"); !ok {
		t.Error("binding not applied after list failure")
	}
}

func TestReconcile_RevertsDrift(t *testing.T) {
	f, k := newFakeKube(t)
	desired := []binding{rb("team-a", "kbridge-dev-view", "view", "kbridge:a@x")}
	reconcile(context.Background(), k, desired, nil)
	f.put(rb("team-a", "kbridge-dev-view", "view", "kbridge:a@x", "mallory"), true)
	if errs := reconcile(context.Background(), k, desired, nil); len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	if b, _ := f.get("team-a", "kbridge-dev-view"); !slices.Equal(usersOf(b), []string{"kbridge:a@x"}) {
		t.Errorf("users after drift = %v", usersOf(b))
	}
}

func TestReconcile_RoleChangeRecreates(t *testing.T) {
	f, k := newFakeKube(t)
	f.putNamespace("team-a", map[string]string{podSecurityEnforce: "baseline"})
	f.put(rb("team-a", "kbridge-dev", "view", "kbridge:a@x"), true)
	desired := []binding{rb("team-a", "kbridge-dev", "edit", "kbridge:a@x")}
	if errs := reconcile(context.Background(), k, desired, nil); len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	if b, _ := f.get("team-a", "kbridge-dev"); b.RoleRef.Name != "edit" {
		t.Errorf("roleRef = %q, want edit", b.RoleRef.Name)
	}
	want := []string{"DELETE team-a/kbridge-dev", "POST team-a/kbridge-dev"}
	if got := f.writeLog(); !slices.Equal(got, want) {
		t.Errorf("writes = %v, want %v", got, want)
	}
}

func TestReconcile_LeavesUnlabelledSameName(t *testing.T) {
	f, k := newFakeKube(t)
	f.put(rb("team-a", "kbridge-dev-view", "view", "someone"), false)
	desired := []binding{rb("team-a", "kbridge-dev-view", "view", "kbridge:a@x")}
	errs := reconcile(context.Background(), k, desired, nil)
	if len(errs) != 1 || !strings.Contains(errs[0], "not managed by kbridge") {
		t.Errorf("errors = %v", errs)
	}
	if got := f.writeLog(); len(got) != 0 {
		t.Errorf("writes = %v", got)
	}
}

func TestReconcile_RefusesUnprefixedManaged(t *testing.T) {
	f, k := newFakeKube(t)
	f.put(rb("team-a", "ops-binding", "view", "ops"), true) // labelled by someone else
	errs := reconcile(context.Background(), k, nil, nil)
	want := "binding ops-binding: name must start with kbridge-"
	if !slices.Equal(errs, []string{want}) {
		t.Errorf("errors = %v, want %q", errs, want)
	}
	if _, ok := f.get("team-a", "ops-binding"); !ok {
		t.Error("unprefixed binding deleted")
	}
}

func TestReconcile_RequiresPodSecurity(t *testing.T) {
	refusal := `binding kbridge-dev-edit: namespace "team-a" must enforce pod-security baseline or restricted before edit can be granted there (kubectl label ns team-a pod-security.kubernetes.io/enforce=baseline)`
	tests := []struct {
		name    string
		labels  map[string]string // nil = namespace does not exist
		b       binding
		created bool
		wantErr string
	}{
		{"baseline", map[string]string{podSecurityEnforce: "baseline"}, rb("team-a", "kbridge-dev-edit", "edit", "kbridge:a@x"), true, ""},
		{"restricted", map[string]string{podSecurityEnforce: "restricted"}, rb("team-a", "kbridge-dev-edit", "edit", "kbridge:a@x"), true, ""},
		{"unlabelled", map[string]string{}, rb("team-a", "kbridge-dev-edit", "edit", "kbridge:a@x"), false, refusal},
		{"privileged", map[string]string{podSecurityEnforce: "privileged"}, rb("team-a", "kbridge-dev-edit", "edit", "kbridge:a@x"), false, refusal},
		{"warn only", map[string]string{"pod-security.kubernetes.io/warn": "baseline"}, rb("team-a", "kbridge-dev-edit", "edit", "kbridge:a@x"), false, refusal},
		{"missing namespace", nil, rb("team-a", "kbridge-dev-edit", "edit", "kbridge:a@x"), false, `binding kbridge-dev-edit: reading namespace "team-a": GET /api/v1/namespaces/team-a: 404 namespaces "team-a" not found`},
		{"view unlabelled", map[string]string{}, rb("team-a", "kbridge-dev-view", "view", "kbridge:a@x"), true, ""},
		{"cluster-wide", nil, rb("", "kbridge-dev-edit", "edit", "kbridge:a@x"), true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, k := newFakeKube(t)
			if tt.labels != nil {
				f.putNamespace("team-a", tt.labels)
			}
			errs := reconcile(context.Background(), k, []binding{tt.b}, nil)
			if got := strings.Join(errs, "; "); got != tt.wantErr {
				t.Errorf("errors = %q, want %q", got, tt.wantErr)
			}
			if _, ok := f.get(tt.b.Namespace, tt.b.Name); ok != tt.created {
				t.Errorf("created = %v, want %v", ok, tt.created)
			}
		})
	}
	t.Run("label removed revokes", func(t *testing.T) {
		f, k := newFakeKube(t)
		f.putNamespace("team-a", map[string]string{podSecurityEnforce: "baseline"})
		desired := []binding{rb("team-a", "kbridge-dev-edit", "edit", "kbridge:a@x"), rb("team-a", "kbridge-dev-view", "view", "kbridge:a@x")}
		if errs := reconcile(context.Background(), k, desired, nil); len(errs) != 0 {
			t.Fatalf("errors = %v", errs)
		}
		f.putNamespace("team-a", map[string]string{})
		if errs := reconcile(context.Background(), k, desired, nil); !slices.Equal(errs, []string{refusal}) {
			t.Errorf("errors = %v, want %q", errs, refusal)
		}
		if _, ok := f.get("team-a", "kbridge-dev-edit"); ok {
			t.Error("edit binding kept after the namespace stopped enforcing")
		}
		if _, ok := f.get("team-a", "kbridge-dev-view"); !ok {
			t.Error("view binding deleted")
		}
	})
}

func TestNeedsPodSecurity(t *testing.T) {
	for role, want := range map[string]bool{"view": false, "edit": true, "admin": true, "cluster-admin": true, "custom": true} {
		if got := needsPodSecurity(role); got != want {
			t.Errorf("needsPodSecurity(%q) = %v, want %v", role, got, want)
		}
	}
}

func TestCheckBinding(t *testing.T) {
	tests := []struct {
		name string
		b    binding
		want string
	}{
		{"ok namespaced", rb("team-a", "kbridge-dev-view", "view", "kbridge:a@x"), ""},
		{"ok cluster", rb("", "kbridge-dev-view", "view", "kbridge:a@x"), ""},
		{"no prefix", rb("team-a", "dev-view", "view", "kbridge:a@x"), "binding dev-view: name must start with kbridge-"},
		{"bad name", rb("team-a", "kbridge-../../x", "view", "kbridge:a@x"), `binding "kbridge-../../x": name is not a valid kubernetes name`},
		{"colon name", rb("team-a", "kbridge-dev-system:view", "view", "kbridge:a@x"), `binding "kbridge-dev-system:view": name is not a valid kubernetes name`},
		{"bad namespace", rb("Team/A", "kbridge-dev-view", "view", "kbridge:a@x"), `binding kbridge-dev-view: namespace "Team/A" is not a valid kubernetes namespace`},
		{"protected", rb("kbridge", "kbridge-dev-view", "view", "kbridge:a@x"), `binding kbridge-dev-view: namespace "kbridge" is reserved for the kbridge agent`},
		{"foreign user", rb("team-a", "kbridge-dev-view", "view", "admin"), `binding kbridge-dev-view: user "admin" is not a kbridge user`},
		{"no role", rb("team-a", "kbridge-dev-view", "", "kbridge:a@x"), "binding kbridge-dev-view: cluster role is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ""
			if err := checkBinding(tt.b, []string{"kbridge"}); err != nil {
				got = err.Error()
			}
			if got != tt.want {
				t.Errorf("checkBinding = %q, want %q", got, tt.want)
			}
		})
	}
}

// bindingsCP serves desired bindings and records sync reports.
type bindingsCP struct {
	agentpb.UnimplementedAgentServiceServer
	desired []*agentpb.DesiredBinding
	reports chan *agentpb.ReportBindingSyncRequest
}

func (c *bindingsCP) GetDesiredBindings(context.Context, *agentpb.GetDesiredBindingsRequest) (*agentpb.GetDesiredBindingsResponse, error) {
	return &agentpb.GetDesiredBindingsResponse{Version: "v1", Bindings: c.desired}, nil
}

func (c *bindingsCP) ReportBindingSync(_ context.Context, r *agentpb.ReportBindingSyncRequest) (*agentpb.ReportBindingSyncResponse, error) {
	c.reports <- r
	return &agentpb.ReportBindingSyncResponse{}, nil
}

func startBindingsCP(t *testing.T, cp *bindingsCP) agentpb.AgentServiceClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	agentpb.RegisterAgentServiceServer(srv, cp)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return agentpb.NewAgentServiceClient(conn)
}

func TestSyncer_RunReconcilesAndReports(t *testing.T) {
	f, k := newFakeKube(t)
	cp := &bindingsCP{reports: make(chan *agentpb.ReportBindingSyncRequest, 4), desired: []*agentpb.DesiredBinding{
		{Name: "kbridge-dev-view", Namespace: "team-a", ClusterRole: "view", Users: []string{"kbridge:a@x"}},
		{Name: "kbridge-dev-view", Namespace: "kbridge", ClusterRole: "view", Users: []string{"kbridge:a@x"}},
	}}
	client := startBindingsCP(t, cp)
	s := newSyncer(k, []string{"kbridge"}, time.Hour, func() (agentpb.AgentServiceClient, string) { return client, "agent-1" })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.run(ctx) }()
	var r *agentpb.ReportBindingSyncRequest
	select {
	case r = <-cp.reports:
	case <-time.After(5 * time.Second):
		t.Fatal("no sync report")
	}
	cancel()
	<-done
	if r.GetAgentId() != "agent-1" || r.GetVersion() != "v1" || r.GetInSync() || len(r.GetErrors()) != 1 {
		t.Errorf("report = %+v", r)
	}
	if _, ok := f.get("team-a", "kbridge-dev-view"); !ok {
		t.Error("desired binding not applied")
	}
}

func TestAgent_NoteBindingsVersionTriggers(t *testing.T) {
	a := New(DefaultConfig())
	a.noteBindingsVersion("v1") // no syncer yet: must not panic
	a.sync = newSyncer(nil, nil, 0, nil)
	tests := []struct {
		version string
		want    bool
	}{{"", false}, {"v1", true}, {"v1", false}, {"v2", true}}
	for _, tt := range tests {
		a.noteBindingsVersion(tt.version)
		select {
		case <-a.sync.kick:
			if !tt.want {
				t.Errorf("version %q triggered a sync", tt.version)
			}
		default:
			if tt.want {
				t.Errorf("version %q did not trigger a sync", tt.version)
			}
		}
	}
}
