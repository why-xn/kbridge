package controlplane

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newSyncServer registers one agent per cluster and seeds users alice, bob
// and the inactive carol. It returns the server, the store and the agent IDs.
func newSyncServer(t *testing.T, eng *PolicyEngine, clusters ...string) (*GRPCServer, *SQLiteStore, map[string]string) {
	t.Helper()
	db := newTestStore(t)
	for _, u := range []User{{Email: "alice", IsActive: true}, {Email: "bob", IsActive: true}, {Email: "carol"}} {
		if err := db.CreateUser(context.Background(), &u); err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	srv := NewGRPCServer(NewAgentStore(), NewCommandQueue(), NewAgentAuthenticator(db, testPepper), NewSessionManager(10))
	srv.policy = eng
	ids := map[string]string{}
	for _, c := range clusters {
		seedClusterToken(t, db, c, "token-"+c, nil)
		resp, err := srv.Register(context.Background(), &agentpb.RegisterRequest{AgentToken: "token-" + c, ClusterName: c})
		if err != nil || !resp.Success {
			t.Fatalf("register %s: %v %s", c, err, resp.GetErrorMessage())
		}
		ids[c] = resp.AgentId
	}
	return srv, db, ids
}

func TestHeartbeat_CarriesBindingsVersion(t *testing.T) {
	eng := engineFor(t, accessPolicy)
	tests := []struct {
		name string
		eng  *PolicyEngine
		want string
	}{
		{"no policy", nil, "none"},
		{"policy", eng, BindingsVersion(eng.DesiredBindings("dev-1", []string{"alice", "bob"}, nil))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _, ids := newSyncServer(t, tt.eng, "dev-1")
			resp, err := srv.Heartbeat(context.Background(), &agentpb.HeartbeatRequest{AgentId: ids["dev-1"]})
			if err != nil {
				t.Fatalf("heartbeat: %v", err)
			}
			if resp.DesiredBindingsVersion != tt.want {
				t.Errorf("version = %q, want %q", resp.DesiredBindingsVersion, tt.want)
			}
		})
	}
}

func TestGetDesiredBindings(t *testing.T) {
	alice := []string{"kbridge:alice"}
	want := []*agentpb.DesiredBinding{
		{Name: "kbridge-viewer-view", ClusterRole: "view", Users: []string{"kbridge:alice", "kbridge:bob"}},
		{Name: "kbridge-developer-edit", Namespace: "dev", ClusterRole: "edit", Users: alice},
		{Name: "kbridge-developer-view", Namespace: "prod", ClusterRole: "view", Users: alice},
		{Name: "kbridge-developer-edit", Namespace: "staging", ClusterRole: "edit", Users: alice},
	}
	eng := engineFor(t, accessPolicy)
	srv, _, ids := newSyncServer(t, eng, "prod-1")
	resp, err := srv.GetDesiredBindings(context.Background(), &agentpb.GetDesiredBindingsRequest{AgentId: ids["prod-1"]})
	if err != nil {
		t.Fatalf("get desired bindings: %v", err)
	}
	if resp.Version != BindingsVersion(eng.DesiredBindings("prod-1", []string{"alice", "bob"}, nil)) {
		t.Errorf("unexpected version %q", resp.Version)
	}
	if len(resp.Bindings) != len(want) {
		t.Fatalf("got %d bindings, want %d: %v", len(resp.Bindings), len(want), resp.Bindings)
	}
	for i := range want {
		got := resp.Bindings[i]
		if got.Name != want[i].Name || got.Namespace != want[i].Namespace ||
			got.ClusterRole != want[i].ClusterRole || !reflect.DeepEqual(got.Users, want[i].Users) {
			t.Errorf("binding %d = %v, want %v", i, got, want[i])
		}
	}
}

func TestGetDesiredBindings_OnlyOwnCluster(t *testing.T) {
	srv, _, ids := newSyncServer(t, engineFor(t, accessPolicy), "dev-1", "prod-1")
	tests := []struct {
		name     string
		agentID  string
		wantCode codes.Code
		wantProd bool
	}{
		{"dev agent sees no prod binding", ids["dev-1"], codes.OK, false},
		{"prod agent sees prod binding", ids["prod-1"], codes.OK, true},
		{"unknown agent refused", "agent-forged", codes.PermissionDenied, false},
		{"missing agent id", "", codes.InvalidArgument, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := srv.GetDesiredBindings(context.Background(), &agentpb.GetDesiredBindingsRequest{AgentId: tt.agentID})
			if status.Code(err) != tt.wantCode {
				t.Fatalf("code = %v, want %v (%v)", status.Code(err), tt.wantCode, err)
			}
			gotProd := false
			for _, b := range resp.GetBindings() {
				gotProd = gotProd || b.Namespace == "prod"
			}
			if gotProd != tt.wantProd {
				t.Errorf("prod binding present = %v, want %v", gotProd, tt.wantProd)
			}
		})
	}
}

func TestReportBindingSync_Refusals(t *testing.T) {
	srv, _, _ := newSyncServer(t, nil, "dev-1")
	tests := []struct {
		name, agentID string
		want          codes.Code
	}{
		{"unknown agent", "agent-forged", codes.PermissionDenied},
		{"missing agent id", "", codes.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := srv.ReportBindingSync(context.Background(), &agentpb.ReportBindingSyncRequest{AgentId: tt.agentID})
			if status.Code(err) != tt.want {
				t.Errorf("code = %v, want %v", status.Code(err), tt.want)
			}
		})
	}
}

func TestCapSyncErrors(t *testing.T) {
	many := make([]string, 60)
	for i := range many {
		many[i] = "e"
	}
	tests := []struct {
		name    string
		in      []string
		wantN   int
		wantMax int
	}{
		{"nil", nil, 0, 0},
		{"too many", many, maxSyncErrors, 1},
		{"too long", []string{strings.Repeat("x", 600)}, 1, maxSyncErrorBytes},
		{"control chars stripped", []string{"a\x1b[31mb\nc"}, 1, len("a[31mbc")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := capSyncErrors(tt.in)
			if len(got) != tt.wantN {
				t.Fatalf("len = %d, want %d", len(got), tt.wantN)
			}
			for _, e := range got {
				if len(e) != tt.wantMax {
					t.Errorf("entry len = %d, want %d (%q)", len(e), tt.wantMax, e)
				}
			}
		})
	}
}

func TestSyncState(t *testing.T) {
	tests := []struct {
		name string
		st   *SyncStatus
		want string
	}{
		{"never reported", nil, SyncStateUnreported},
		{"matching and in sync", &SyncStatus{Version: "v1", InSync: true}, SyncStateInSync},
		{"stale version", &SyncStatus{Version: "v0", InSync: true}, SyncStateDrift},
		{"apply failed", &SyncStatus{Version: "v1", InSync: false}, SyncStateDrift},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := syncState(tt.st, "v1"); got != tt.want {
				t.Errorf("syncState = %q, want %q", got, tt.want)
			}
		})
	}
}

const accessPolicy = `
default: viewer
roles:
  - name: developer
    clusterAccess:
      - clusters: ["*"]
        clusterRole: edit
        namespaces: [dev, staging]
      - clusters: ["prod-*"]
        clusterRole: view
        namespaces: [prod]
  - name: viewer
    clusterAccess:
      - clusters: ["*"]
        clusterRole: view
        clusterWide: true
bindings:
  - subject: alice
    roles: [developer]
`

// engineFor parses doc into an engine with no file behind it.
func engineFor(t *testing.T, doc string) *PolicyEngine {
	t.Helper()
	eng, err := NewPolicyEngineFromBytes([]byte(doc))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	return eng
}

const gatedPolicy = `
roles:
  - name: oncall
    clusterAccess:
      - clusters: ["prod-*"]
        clusterRole: edit
        namespaces: [payments]
        requiresGrant: true
      - clusters: ["prod-*"]
        clusterRole: view
        clusterWide: true
        requiresGrant: true
bindings:
  - subject: alice
    roles: [oncall]
  - subject: carol
    roles: [oncall]
`

// TestGetDesiredBindings_GrantGated: only a role member with an approved,
// unexpired grant covering the binding's cluster and namespace is bound.
func TestGetDesiredBindings_GrantGated(t *testing.T) {
	future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Minute)
	tests := []struct {
		name                        string
		subject, cluster, namespace string
		status                      GrantStatus
		expires                     time.Time
		wantNamespaced, wantWide    []string
	}{
		{"live grant on the namespace", "alice", "prod-*", "payments", GrantStatusApproved, future, []string{"kbridge:alice"}, nil},
		{"live grant on the whole cluster", "alice", "prod-1", "", GrantStatusApproved, future, []string{"kbridge:alice"}, []string{"kbridge:alice"}},
		{"expired", "alice", "prod-*", "payments", GrantStatusApproved, past, nil, nil},
		{"revoked", "alice", "prod-*", "payments", GrantStatusRevoked, future, nil, nil},
		{"pending", "alice", "prod-*", "payments", GrantStatusPending, future, nil, nil},
		{"another cluster", "alice", "staging", "payments", GrantStatusApproved, future, nil, nil},
		{"another namespace", "alice", "prod-*", "billing", GrantStatusApproved, future, nil, nil},
		{"holder outside the role", "bob", "prod-*", "payments", GrantStatusApproved, future, nil, nil},
		{"inactive holder in the role", "carol", "prod-*", "payments", GrantStatusApproved, future, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, db, ids := newSyncServer(t, engineFor(t, gatedPolicy), "prod-1")
			now := time.Now()
			g := &Grant{ID: "g1", Subject: tt.subject, ClusterName: tt.cluster, Namespace: tt.namespace, Status: tt.status,
				Reason: "incident 42", Duration: time.Hour, RequestedAt: now, DecidedAt: &now, ExpiresAt: &tt.expires}
			if err := db.CreateGrant(context.Background(), g); err != nil {
				t.Fatalf("create grant: %v", err)
			}
			resp, err := srv.GetDesiredBindings(context.Background(), &agentpb.GetDesiredBindingsRequest{AgentId: ids["prod-1"]})
			if err != nil {
				t.Fatal(err)
			}
			got := map[string][]string{}
			for _, b := range resp.Bindings {
				got[b.Name] = b.Users
			}
			if len(got) != 2 {
				t.Fatalf("bindings = %v, want the two gated ones", got)
			}
			if u := got["kbridge-oncall-edit-grant"]; !sameUsers(u, tt.wantNamespaced) {
				t.Errorf("namespaced users = %v, want %v", u, tt.wantNamespaced)
			}
			if u := got["kbridge-oncall-view-grant"]; !sameUsers(u, tt.wantWide) {
				t.Errorf("cluster-wide users = %v, want %v", u, tt.wantWide)
			}
		})
	}
}

func sameUsers(got, want []string) bool {
	return len(got) == len(want) && (len(got) == 0 || reflect.DeepEqual(got, want))
}
