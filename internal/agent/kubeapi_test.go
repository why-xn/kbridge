package agent

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeKube is an in-memory RBAC API server. It refuses (403) any roleRef
// outside [view, edit], standing in for the agent's escalation ceiling.
type fakeKube struct {
	mu      sync.Mutex
	objs    map[string]rbObject // key: "<ns>/<name>", ns empty for cluster bindings
	rv      int
	writes  []string // "METHOD <ns>/<name>"
	tokens  []string
	forbidC bool                         // 403 on listing clusterrolebindings
	ns      map[string]map[string]string // namespace name -> labels; absent = 404
	nsFail  int                          // when set, namespace GETs answer this status
}

func newFakeKube(t *testing.T) (*fakeKube, *kubeAPI) {
	t.Helper()
	f := &fakeKube{objs: map[string]rbObject{}, ns: map[string]map[string]string{}}
	srv := httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tokenFile := filepath.Join(t.TempDir(), "token")
	writeToken(t, tokenFile, "agent-sa-token")
	return f, newKubeAPI(Upstream{Server: srv.URL, CA: pool, TokenFile: tokenFile})
}

// put seeds an object directly, bypassing the API.
func (f *fakeKube) put(b binding, managed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := toObject(b)
	if !managed {
		o.Metadata.Labels = nil
	}
	f.rv++
	o.Metadata.ResourceVersion = strconv.Itoa(f.rv)
	f.objs[b.Namespace+"/"+b.Name] = o
}

// putNamespace seeds a namespace with labels.
func (f *fakeKube) putNamespace(name string, labels map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ns[name] = labels
}

func (f *fakeKube) readNamespace(w http.ResponseWriter, name string) {
	f.mu.Lock()
	labels, ok := f.ns[name]
	fail := f.nsFail
	f.mu.Unlock()
	if fail != 0 {
		kubeStatus(w, fail, http.StatusText(fail))
		return
	}
	if !ok {
		kubeStatus(w, http.StatusNotFound, "namespaces \""+name+"\" not found")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"kind": "Namespace", "metadata": map[string]any{"name": name, "labels": labels}})
}

func (f *fakeKube) get(ns, name string) (rbObject, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objs[ns+"/"+name]
	return o, ok
}

func (f *fakeKube) writeLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writes...)
}

func (f *fakeKube) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.tokens = append(f.tokens, r.Header.Get("Authorization"))
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer agent-sa-token" {
		kubeStatus(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if name, ok := strings.CutPrefix(r.URL.Path, "/api/v1/namespaces/"); ok && r.Method == http.MethodGet {
		f.readNamespace(w, name)
		return
	}
	ns, name, collection := parseRBACPath(r.URL.Path)
	switch {
	case collection && r.Method == http.MethodGet && r.URL.Query().Get("watch") == "true":
		f.watch(w, r)
	case collection && r.Method == http.MethodGet:
		f.list(w, r)
	case collection && r.Method == http.MethodPost:
		f.create(w, r, ns)
	case r.Method == http.MethodGet:
		f.read(w, ns, name)
	case r.Method == http.MethodPut:
		f.update(w, r, ns, name)
	case r.Method == http.MethodDelete:
		f.remove(w, ns, name)
	default:
		kubeStatus(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// parseRBACPath splits an RBAC path into namespace, name and whether it is a collection.
func parseRBACPath(p string) (ns, name string, collection bool) {
	rest := strings.TrimPrefix(p, rbacAPI+"/")
	segs := strings.Split(rest, "/")
	if segs[0] == "namespaces" && len(segs) >= 3 {
		ns, segs = segs[1], segs[2:]
	}
	if len(segs) == 2 {
		return ns, segs[1], false
	}
	return ns, "", true
}

func kubeStatus(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "code": code, "message": msg})
}

func (f *fakeKube) list(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("labelSelector") != managedByLabel+"=kbridge" {
		kubeStatus(w, http.StatusBadRequest, "test expects the managed-by selector")
		return
	}
	if f.forbidC && strings.HasSuffix(r.URL.Path, "/clusterrolebindings") {
		kubeStatus(w, http.StatusForbidden, "cannot list clusterrolebindings")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []rbObject{}
	cluster := strings.HasSuffix(r.URL.Path, "/clusterrolebindings")
	for _, o := range f.objs {
		if o.Metadata.Labels[managedByLabel] == "kbridge" && (o.Metadata.Namespace == "") == cluster {
			items = append(items, o)
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

func (f *fakeKube) read(w http.ResponseWriter, ns, name string) {
	o, ok := f.get(ns, name)
	if !ok {
		kubeStatus(w, http.StatusNotFound, "not found")
		return
	}
	_ = json.NewEncoder(w).Encode(o)
}

// decodeAllowed decodes a body and enforces the fake ceiling.
func decodeAllowed(w http.ResponseWriter, r *http.Request) (rbObject, bool) {
	var o rbObject
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		kubeStatus(w, http.StatusBadRequest, err.Error())
		return o, false
	}
	if o.RoleRef.Name != "view" && o.RoleRef.Name != "edit" {
		kubeStatus(w, http.StatusForbidden, "attempting to grant RBAC permissions not currently held")
		return o, false
	}
	return o, true
}

func (f *fakeKube) create(w http.ResponseWriter, r *http.Request, ns string) {
	o, ok := decodeAllowed(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := ns + "/" + o.Metadata.Name
	if _, exists := f.objs[key]; exists {
		kubeStatus(w, http.StatusConflict, "already exists")
		return
	}
	f.store(w, "POST", key, o)
}

func (f *fakeKube) update(w http.ResponseWriter, r *http.Request, ns, name string) {
	o, ok := decodeAllowed(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, exists := f.objs[ns+"/"+name]
	switch {
	case !exists:
		kubeStatus(w, http.StatusNotFound, "not found")
	case cur.Metadata.ResourceVersion != o.Metadata.ResourceVersion:
		kubeStatus(w, http.StatusConflict, "resourceVersion mismatch")
	case cur.RoleRef != o.RoleRef:
		kubeStatus(w, http.StatusUnprocessableEntity, "cannot change roleRef")
	default:
		f.store(w, "PUT", ns+"/"+name, o)
	}
}

// store saves o under key with a fresh resourceVersion; f.mu must be held.
func (f *fakeKube) store(w http.ResponseWriter, method, key string, o rbObject) {
	f.rv++
	o.Metadata.ResourceVersion = strconv.Itoa(f.rv)
	f.objs[key] = o
	f.writes = append(f.writes, method+" "+key)
	_ = json.NewEncoder(w).Encode(o)
}

func (f *fakeKube) remove(w http.ResponseWriter, ns, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.objs[ns+"/"+name]; !ok {
		kubeStatus(w, http.StatusNotFound, "not found")
		return
	}
	delete(f.objs, ns+"/"+name)
	f.writes = append(f.writes, "DELETE "+ns+"/"+name)
	kubeStatus(w, http.StatusOK, "deleted")
}

// watch streams an ADDED event per managed object, then holds the stream open.
func (f *fakeKube) watch(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	var events []map[string]any
	for _, o := range f.objs {
		if o.Metadata.Labels[managedByLabel] == "kbridge" {
			events = append(events, map[string]any{"type": "ADDED", "object": o})
		}
	}
	f.mu.Unlock()
	for _, e := range events {
		_ = json.NewEncoder(w).Encode(e)
	}
	w.(http.Flusher).Flush()
	<-r.Context().Done()
}

func TestWatchManaged_TriggersOnEvent(t *testing.T) {
	f, k := newFakeKube(t)
	f.put(binding{Name: "kbridge-dev-view", Namespace: "team-a", ClusterRole: "view", Users: []string{"kbridge:a@x"}}, true)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan struct{}, 8)
	done := make(chan error, 1)
	go func() { done <- k.watchManaged(ctx, func() { got <- struct{}{} }) }()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no event from watch")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watchManaged did not return after cancel")
	}
}

func TestKubeAPI_SendsAgentToken(t *testing.T) {
	f, k := newFakeKube(t)
	if _, err := k.listManaged(context.Background()); err != nil {
		t.Fatalf("listManaged: %v", err)
	}
	for _, tok := range f.tokens {
		if tok != "Bearer agent-sa-token" {
			t.Errorf("Authorization = %q", tok)
		}
	}
}

func TestKubeAPI_NamespaceEnforce(t *testing.T) {
	f, k := newFakeKube(t)
	f.putNamespace("enforced", map[string]string{podSecurityEnforce: "baseline", "pod-security.kubernetes.io/warn": "restricted"})
	f.putNamespace("warn-only", map[string]string{"pod-security.kubernetes.io/warn": "baseline"})
	f.putNamespace("bare", nil)
	tests := []struct {
		ns, want string
		wantErr  bool
	}{
		{"enforced", "baseline", false},
		{"warn-only", "", false},
		{"bare", "", false},
		{"missing", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.ns, func(t *testing.T) {
			got, err := k.namespaceEnforce(context.Background(), tt.ns)
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Errorf("namespaceEnforce = %q, %v; want %q, err %v", got, err, tt.want, tt.wantErr)
			}
			if tt.wantErr && !isNotFound(err) {
				t.Errorf("err = %v, want a 404", err)
			}
		})
	}
}
