package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
)

const (
	rbacAPI        = "/apis/rbac.authorization.k8s.io/v1"
	rbacGroup      = "rbac.authorization.k8s.io"
	managedByLabel = "app.kubernetes.io/managed-by"
	managedByValue = "kbridge"
	managedQuery   = "labelSelector=app.kubernetes.io%2Fmanaged-by%3Dkbridge"

	podSecurityEnforce = "pod-security.kubernetes.io/enforce"
)

// managedCollections are the cluster-wide lists the agent reads and watches.
var managedCollections = []string{rbacAPI + "/rolebindings", rbacAPI + "/clusterrolebindings"}

// binding is a RoleBinding, or a ClusterRoleBinding when Namespace is empty,
// of ClusterRole to kbridge users.
type binding struct {
	Name, Namespace, ClusterRole string
	Users                        []string
	ResourceVersion              string
	Managed                      bool // carries the managed-by=kbridge label
}

// rbObject is the wire form of a RoleBinding or ClusterRoleBinding.
type rbObject struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   rbMeta      `json:"metadata"`
	RoleRef    rbRoleRef   `json:"roleRef"`
	Subjects   []rbSubject `json:"subjects"`
}

type rbMeta struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
}

type rbRoleRef struct {
	APIGroup string `json:"apiGroup"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}

type rbSubject struct {
	Kind      string `json:"kind"`
	APIGroup  string `json:"apiGroup,omitempty"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

func toObject(b binding) rbObject {
	kind := "RoleBinding"
	if b.Namespace == "" {
		kind = "ClusterRoleBinding"
	}
	subjects := make([]rbSubject, 0, len(b.Users))
	for _, u := range b.Users {
		subjects = append(subjects, rbSubject{Kind: "User", APIGroup: rbacGroup, Name: u})
	}
	return rbObject{
		APIVersion: rbacGroup + "/v1",
		Kind:       kind,
		Metadata: rbMeta{Name: b.Name, Namespace: b.Namespace, ResourceVersion: b.ResourceVersion,
			Labels: map[string]string{managedByLabel: managedByValue}},
		RoleRef:  rbRoleRef{APIGroup: rbacGroup, Kind: "ClusterRole", Name: b.ClusterRole},
		Subjects: subjects,
	}
}

func fromObject(o rbObject) binding {
	b := binding{Name: o.Metadata.Name, Namespace: o.Metadata.Namespace, ClusterRole: o.RoleRef.Name,
		ResourceVersion: o.Metadata.ResourceVersion, Managed: o.Metadata.Labels[managedByLabel] == managedByValue}
	for _, s := range o.Subjects {
		b.Users = append(b.Users, s.Name)
	}
	return b
}

// kubeAPI talks to the API server as the agent's own ServiceAccount: no
// window, no impersonation.
type kubeAPI struct {
	up     Upstream
	client *http.Client
}

func newKubeAPI(up Upstream) *kubeAPI {
	return &kubeAPI{up: up, client: &http.Client{Transport: up.transport()}}
}

// apiError is a non-2xx answer from the API server.
type apiError struct {
	code int
	msg  string
}

func (e *apiError) Error() string { return fmt.Sprintf("%d %s", e.code, e.msg) }

func readAPIError(resp *http.Response) error {
	var st struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&st)
	if st.Message == "" {
		st.Message = http.StatusText(resp.StatusCode)
	}
	return &apiError{code: resp.StatusCode, msg: st.Message}
}

func isNotFound(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.code == http.StatusNotFound
}

func isForbidden(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.code == http.StatusForbidden
}

// send makes one request and returns the response only when it is 2xx.
func (k *kubeAPI) send(ctx context.Context, method, path string, body any) (*http.Response, error) {
	req, err := k.newRequest(ctx, method, path, body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return nil, fmt.Errorf("%s %s: %w", method, path, readAPIError(resp))
	}
	return resp, nil
}

func (k *kubeAPI) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	r, err := jsonBody(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, k.up.Server+path, r)
	if err != nil {
		return nil, err
	}
	token, err := k.up.token()
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func jsonBody(body any) (io.Reader, error) {
	if body == nil {
		return http.NoBody, nil
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// do sends a request and decodes a 2xx body into out when out is not nil.
func (k *kubeAPI) do(ctx context.Context, method, path string, body, out any) error {
	resp, err := k.send(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
	}
	return nil
}

// collectionPath is the binding list for ns; ns must already be validated.
func collectionPath(ns string) string {
	if ns == "" {
		return rbacAPI + "/clusterrolebindings"
	}
	return rbacAPI + "/namespaces/" + ns + "/rolebindings"
}

// itemPath validates b's name and namespace before they reach a URL path.
func itemPath(b binding) (string, error) {
	if err := validNames(b); err != nil {
		return "", err
	}
	return collectionPath(b.Namespace) + "/" + b.Name, nil
}

// listManaged lists bindings of both kinds carrying the managed-by label. A
// failed list (a 403 under the ceiling) is returned as an error alongside
// whatever the other list found.
func (k *kubeAPI) listManaged(ctx context.Context) ([]binding, error) {
	var out []binding
	var errs []error
	for _, p := range managedCollections {
		var list struct {
			Items []rbObject `json:"items"`
		}
		if err := k.do(ctx, http.MethodGet, p+"?"+managedQuery, nil, &list); err != nil {
			errs = append(errs, fmt.Errorf("listing managed bindings: %w", err))
			continue
		}
		for _, o := range list.Items {
			out = append(out, fromObject(o))
		}
	}
	return out, errors.Join(errs...)
}

// apply creates b, or brings an existing managed binding in line with it.
func (k *kubeAPI) apply(ctx context.Context, b binding) error {
	p, err := itemPath(b)
	if err != nil {
		return err
	}
	var cur rbObject
	err = k.do(ctx, http.MethodGet, p, nil, &cur)
	if isNotFound(err) {
		return k.do(ctx, http.MethodPost, collectionPath(b.Namespace), toObject(b), nil)
	}
	if err != nil {
		return err
	}
	return k.update(ctx, p, cur, toObject(b))
}

// update rewrites cur as want, refusing a binding without the managed-by label.
func (k *kubeAPI) update(ctx context.Context, p string, cur, want rbObject) error {
	switch {
	case cur.Metadata.Labels[managedByLabel] != managedByValue:
		return errors.New("exists and is not managed by kbridge")
	case cur.RoleRef != want.RoleRef:
		return k.recreate(ctx, fromObject(cur), want)
	case slices.Equal(cur.Subjects, want.Subjects):
		return nil
	}
	want.Metadata.ResourceVersion = cur.Metadata.ResourceVersion
	return k.do(ctx, http.MethodPut, p, want, nil)
}

// recreate replaces cur with want; Kubernetes forbids changing a roleRef in place.
func (k *kubeAPI) recreate(ctx context.Context, cur binding, want rbObject) error {
	if err := k.delete(ctx, cur); err != nil {
		return err
	}
	return k.do(ctx, http.MethodPost, collectionPath(cur.Namespace), want, nil)
}

// delete removes b, only if it is still the version that was read.
func (k *kubeAPI) delete(ctx context.Context, b binding) error {
	p, err := itemPath(b)
	if err != nil {
		return err
	}
	opts := map[string]any{"apiVersion": "v1", "kind": "DeleteOptions"}
	if b.ResourceVersion != "" {
		opts["preconditions"] = map[string]string{"resourceVersion": b.ResourceVersion}
	}
	return k.do(ctx, http.MethodDelete, p, opts, nil)
}

// namespaceEnforce returns ns's pod-security enforce level, "" when unset.
func (k *kubeAPI) namespaceEnforce(ctx context.Context, ns string) (string, error) {
	if !dnsLabel.MatchString(ns) {
		return "", fmt.Errorf("namespace %q is not a valid kubernetes namespace", ns)
	}
	var obj struct {
		Metadata rbMeta `json:"metadata"`
	}
	if err := k.do(ctx, http.MethodGet, "/api/v1/namespaces/"+ns, nil, &obj); err != nil {
		return "", err
	}
	return obj.Metadata.Labels[podSecurityEnforce], nil
}

// watchManaged calls onEvent (possibly concurrently) for each change to a
// managed binding of either kind. It returns when ctx is done or either
// stream ends.
func (k *kubeAPI) watchManaged(ctx context.Context, onEvent func()) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, len(managedCollections))
	for _, p := range managedCollections {
		go func() { errc <- k.watch(ctx, p, onEvent) }()
	}
	err := <-errc
	cancel()
	for range len(managedCollections) - 1 {
		<-errc
	}
	return err
}

func (k *kubeAPI) watch(ctx context.Context, collection string, onEvent func()) error {
	resp, err := k.send(ctx, http.MethodGet, collection+"?watch=true&"+managedQuery, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var ev struct {
			Type   string          `json:"type"`
			Object json.RawMessage `json:"object"`
		}
		if err := dec.Decode(&ev); err != nil {
			return watchEnd(ctx, collection, err)
		}
		if err := handleEvent(ev.Type, ev.Object, onEvent); err != nil {
			return fmt.Errorf("watching %s: %w", collection, err)
		}
	}
}

func handleEvent(typ string, obj json.RawMessage, onEvent func()) error {
	switch typ {
	case "ADDED", "MODIFIED", "DELETED":
		onEvent()
	case "ERROR":
		var st struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(obj, &st)
		return fmt.Errorf("watch error: %s", st.Message)
	}
	return nil
}

// watchEnd turns a stream's end into nil when it was cancelled or closed cleanly.
func watchEnd(ctx context.Context, collection string, err error) error {
	if ctx.Err() != nil || errors.Is(err, io.EOF) {
		return nil
	}
	return fmt.Errorf("watching %s: %w", collection, err)
}
