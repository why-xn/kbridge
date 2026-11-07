package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DefaultPollInterval is how often the agent polls for pending commands.
const DefaultPollInterval = 2 * time.Second

// initialHeartbeatInterval is the wait before the first heartbeat; each
// heartbeat reply then sets the interval. A var so tests need not wait 30s.
var initialHeartbeatInterval = 30 * time.Second

// errRegistrationRejected marks a registration the control plane refused (e.g.
// a revoked token), as opposed to one that never reached it.
var errRegistrationRejected = errors.New("registration rejected")

// errReplaced marks an agent the control plane replaced with a newer
// registration for the same cluster, as the old pod of a rolling update is.
var errReplaced = errors.New("replaced by a newer agent")

// replacedErr returns errReplaced if err is the control plane saying a newer
// agent replaced this one, nil otherwise. The agent then stands by (see
// standBy): re-registering would take the cluster back from the new pod, and
// exiting would too, because during a RollingUpdate the old pod is not yet
// terminating and the kubelet restarts its container, which registers again.
// The control plane answers FailedPrecondition only while the newer agent is
// connected, so a standing-by agent takes the cluster back if that one dies.
func replacedErr(err error) error {
	if status.Code(err) != codes.FailedPrecondition {
		return nil
	}
	return errReplaced
}

// replacedFor is replacedErr for a call made as agentID from a goroutine that
// does not register. A reconnect may have re-registered under a new id since,
// which the control plane counts as replacing the old one; an answer for the
// old id is then stale and says nothing about this agent.
func (a *Agent) replacedFor(err error, agentID string) error {
	if agentID != a.AgentID() {
		return nil
	}
	return replacedErr(err)
}

// standByTouchInterval keeps the health file well inside the chart's
// readiness (60 s) and liveness (180 s) windows while standing by. A var so
// tests need not wait.
var standByTouchInterval = 30 * time.Second

// standByTimeout bounds the stand-by: a rollout stops the old pod within its
// termination grace period, so a replaced agent still running after this is
// a second replica for the same cluster. It exits; Kubernetes restarts it, it
// registers and takes the cluster over, and the other one stands by in turn,
// so two replicas swap the cluster about every 10 minutes (each swap can cut
// a stream) instead of flapping fast. Run one replica (replicaCount: 1). A var
// so tests need not wait.
var standByTimeout = 10 * time.Minute

// errTakeOver tells Run that the agent that replaced this one is gone, so this
// one registers again and serves the cluster.
var errTakeOver = errors.New("the agent that replaced this one is gone")

// standBy keeps a replaced agent alive and live, without serving or
// registering, until it is stopped; it gives up after standByTimeout. On each
// tick it asks the control plane whether the agent that replaced it is still
// connected, and returns errTakeOver when it is not (it died, or the control
// plane restarted), so the cluster is not left without an agent.
func (a *Agent) standBy(ctx context.Context) error {
	log.Printf("%v; standing by until this pod is stopped", errReplaced)
	touch := time.NewTicker(standByTouchInterval)
	defer touch.Stop()
	timeout := time.NewTimer(standByTimeout)
	defer timeout.Stop()
	for {
		select {
		case <-touch.C:
			a.touchHealthFile()
			if a.replacerGone(ctx) {
				log.Printf("%v; registering again", errTakeOver)
				return errTakeOver
			}
		case <-a.stopCh:
			return nil
		case <-ctx.Done():
			return nil
		case <-timeout.C:
			return fmt.Errorf("%w and never stopped: exiting", errReplaced)
		}
	}
}

// replacerGone probes with a heartbeat: the control plane answers a replaced
// id FailedPrecondition while its replacer is connected and NotFound after.
func (a *Agent) replacerGone(ctx context.Context) bool {
	_, err := a.sendHeartbeat(ctx)
	return status.Code(err) == codes.NotFound
}

// replacedC is closed once a newer agent replaced this one.
func (a *Agent) replacedC() <-chan struct{} {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.replaced
}

// markReplaced records that a newer agent replaced this one, which stops the
// heartbeat and poll loops; safe to call more than once.
func (a *Agent) markReplaced() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.isReplaced {
		a.isReplaced = true
		close(a.replaced)
	}
}

// resetReplaced readies a fresh replaced signal before serving again.
func (a *Agent) resetReplaced() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.replaced, a.isReplaced = make(chan struct{}), false
}

// Agent represents the kbridge agent that connects to control plane.
type Agent struct {
	config    *Config
	conn      *grpc.ClientConn
	client    agentpb.AgentServiceClient
	executor  *KubectlExecutor
	window    *Window
	windowDir string // holds the window CA; removed on shutdown
	tempToken string // token file LoadUpstream wrote from a kubeconfig; removed on shutdown
	agentID   string
	sync      *syncer // keeps cluster role bindings in step; set by openWindow
	bindingsV string  // last desired-bindings version a heartbeat reported
	mu        sync.RWMutex
	stopCh    chan struct{}
	stoppedCh chan struct{}
	// replaced is closed once a newer agent replaced this one; guarded by mu
	// because taking the cluster back replaces it (see replacedC).
	replaced   chan struct{}
	isReplaced bool
}

// New creates a new agent with the given configuration.
func New(cfg *Config) *Agent {
	return &Agent{
		config:    cfg,
		executor:  NewKubectlExecutor(nil), // Run opens the window
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
		replaced:  make(chan struct{}),
	}
}

// Run starts the agent, connecting to control plane and maintaining the connection.
func (a *Agent) Run(ctx context.Context) error {
	log.Printf("Agent starting for cluster: %s", a.config.Cluster.Name)
	defer close(a.stoppedCh)

	defer a.closeWindow()
	if err := a.openWindow(); err != nil {
		return fmt.Errorf("starting window: %w", err)
	}

	if err := a.connect(ctx); err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}
	defer a.disconnect()

	if err := a.register(ctx); err != nil {
		return fmt.Errorf("registering with control plane: %w", err)
	}

	// Touch the health file once immediately after successful registration so the
	// liveness initialDelaySeconds window doesn't race a slow first heartbeat.
	a.touchHealthFile()

	for {
		err := a.serve(ctx)
		if !errors.Is(err, errTakeOver) {
			return err
		}
		a.resetReplaced()
		if err := a.reconnect(ctx); err != nil {
			return fmt.Errorf("taking the cluster back: %w", err)
		}
	}
}

// serve runs the heartbeat, poll, stream and sync loops until they stop, then
// stands by if a newer agent replaced this one. It returns errTakeOver when a
// standing-by agent should register again.
func (a *Agent) serve(ctx context.Context) error {
	// A refused re-registration cancels ctx so the loops below stop; being
	// replaced stops them through a.replaced. A running command runs under
	// cmdCtx instead: shutdown and being replaced let it finish (see
	// executeAndSubmit), and only a refused registration (a revoked token)
	// kills it, so a revoked agent exits at once.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmdCtx, kill := context.WithCancel(context.WithoutCancel(ctx))
	defer kill()

	// Start command polling loop in goroutine
	pollDone := make(chan struct{})
	var pollErr error
	go func() {
		defer close(pollDone)
		pollErr = a.loopStopped(a.runCommandPollLoop(ctx, cmdCtx), cancel, kill)
	}()

	// Start persistent stream client. It outlives a shutdown signal: the control
	// plane treats a replaced agent whose stream closed as dead and fails its
	// running commands, so the stream stays open until the in-flight command has
	// reported, and while standing by, so sessions running on it finish.
	streamCtx, stopStream := context.WithCancel(context.WithoutCancel(ctx))
	streamDone := make(chan struct{})
	defer func() { stopStream(); <-streamDone }()
	go func() {
		defer close(streamDone)
		a.runStream(streamCtx)
	}()

	stopSync := a.startSync(ctx)

	// Run heartbeat loop (blocks until stopped)
	hbErr := a.loopStopped(a.runHeartbeatLoop(ctx), cancel, kill)
	stopSync()

	// Let an in-flight command report its result before the connection closes,
	// so its caller gets an answer instead of waiting for the command timeout.
	<-pollDone
	select {
	case <-a.replacedC():
		return a.standBy(ctx)
	default:
	}
	if hbErr != nil {
		return hbErr
	}
	return pollErr
}

// loopStopped handles the error a heartbeat or poll loop stopped with:
// replaced stops the other loop, anything else cancels everything, and a
// refused registration also kills the running command.
func (a *Agent) loopStopped(err error, cancel, kill context.CancelFunc) error {
	if errors.Is(err, errReplaced) {
		a.markReplaced()
		return nil
	}
	if errors.Is(err, errRegistrationRejected) {
		kill()
	}
	if err != nil {
		cancel()
	}
	return err
}

// openWindow builds the window from config and hands it to the executor;
// kubectl never runs with the agent's own credentials.
func (a *Agent) openWindow() error {
	up, err := LoadUpstream(a.config.Kubernetes)
	if err != nil {
		return err
	}
	if a.config.Kubernetes.Kubeconfig != "" && strings.HasPrefix(filepath.Base(up.TokenFile), "kbridge-token-") {
		a.tempToken = up.TokenFile
	}
	if a.windowDir, err = os.MkdirTemp("", "kbridge-window-"); err != nil {
		return fmt.Errorf("creating window dir: %w", err)
	}
	protected := windowProtected(a.config)
	if a.window, err = NewWindow(up, protected, a.windowDir); err != nil {
		return err
	}
	a.executor.window = a.window
	a.sync = newSyncer(newKubeAPI(up), protected, a.config.Sync.Interval, a.currentClient)
	return nil
}

// startSync runs the binding sync loop beside command handling; the returned
// func stops it and waits for it to finish.
func (a *Agent) startSync(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.sync.run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

// currentClient is the control plane client and agent ID the sync loop uses.
func (a *Agent) currentClient() (agentpb.AgentServiceClient, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client, a.agentID
}

// noteBindingsVersion triggers a sync when a heartbeat reports new desired bindings.
func (a *Agent) noteBindingsVersion(v string) {
	if v == "" || v == a.bindingsV || a.sync == nil {
		return
	}
	a.bindingsV = v
	a.sync.trigger()
}

// closeWindow releases whatever openWindow managed to set up.
func (a *Agent) closeWindow() {
	if a.window != nil {
		_ = a.window.Close()
	}
	if a.windowDir != "" {
		_ = os.RemoveAll(a.windowDir)
	}
	if a.tempToken != "" {
		_ = os.Remove(a.tempToken)
	}
}

// windowProtected lists the namespaces the window refuses: the configured ones
// and the agent's own.
func windowProtected(cfg *Config) []string {
	var out []string
	for _, ns := range append(append([]string{}, cfg.ProtectedNamespaces...), ownNamespace(cfg.Kubernetes.TokenFile)) {
		if ns != "" {
			out = append(out, ns)
		}
	}
	return out
}

// Stop signals the agent to stop and waits for it to finish.
func (a *Agent) Stop() {
	close(a.stopCh)
	<-a.stoppedCh
}

// AgentID returns the agent's assigned ID from control plane.
func (a *Agent) AgentID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.agentID
}

func (a *Agent) connect(ctx context.Context) error {
	log.Printf("Connecting to control plane at %s", a.config.ControlPlane.URL)

	// Create connection with timeout
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	creds, err := clientTransportCredentials(a.config.ControlPlane.TLS)
	if err != nil {
		return fmt.Errorf("configuring transport security: %w", err)
	}
	conn, err := grpc.DialContext(dialCtx, a.config.ControlPlane.URL,
		grpc.WithTransportCredentials(creds),
		grpc.WithBlock(),
	)
	if err != nil {
		return fmt.Errorf("dialing control plane: %w", err)
	}

	a.mu.Lock() // the poll and stream goroutines read client beside the id
	a.conn = conn
	a.client = agentpb.NewAgentServiceClient(conn)
	a.mu.Unlock()
	log.Printf("Connected to control plane")
	return nil
}

func (a *Agent) disconnect() {
	if a.conn != nil {
		if err := a.conn.Close(); err != nil {
			log.Printf("Error closing connection: %v", err)
		}
		log.Printf("Disconnected from control plane")
	}
}

func (a *Agent) register(ctx context.Context) error {
	log.Printf("Registering with control plane")

	req := &agentpb.RegisterRequest{
		AgentToken:   a.config.ControlPlane.Token,
		ClusterName:  a.config.Cluster.Name,
		Capabilities: []string{"impersonation", "rbac-sync"},
	}

	// Add timeout for registration
	regCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := a.client.Register(regCtx, req)
	if err != nil {
		return fmt.Errorf("register RPC failed: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("%w: %s", errRegistrationRejected, resp.ErrorMessage)
	}

	a.mu.Lock()
	a.agentID = resp.AgentId
	a.mu.Unlock()

	log.Printf("Registered successfully with agent ID: %s", resp.AgentId)
	// A restarted control plane has no sync status; report without waiting for the timer.
	if a.sync != nil {
		a.sync.trigger()
	}
	return nil
}

// runHeartbeatLoop sends heartbeats until stopped. It returns an error only
// when the control plane refused re-registration or replaced this agent.
func (a *Agent) runHeartbeatLoop(ctx context.Context) error {
	interval := initialHeartbeatInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			nextInterval, err := a.sendHeartbeat(ctx)
			if err != nil {
				if err := a.recoverHeartbeat(ctx, err); err != nil {
					return err
				}
				continue
			}
			a.touchHealthFile()

			// Update ticker interval if server requested different timing
			if nextInterval > 0 && nextInterval != interval {
				interval = nextInterval
				ticker.Reset(interval)
			}

		case <-a.stopCh:
			log.Printf("Heartbeat loop stopping")
			return nil

		case <-a.replacedC():
			return nil

		case <-ctx.Done():
			log.Printf("Context cancelled, stopping heartbeat loop")
			return nil
		}
	}
}

// recoverHeartbeat reconnects after a failed heartbeat. It returns an error
// only when this agent was replaced (it then stands by, see standBy) or its
// registration is refused: every retry would be refused too, so the agent
// exits instead of waiting for the liveness probe to restart it.
func (a *Agent) recoverHeartbeat(ctx context.Context, hbErr error) error {
	log.Printf("Heartbeat failed: %v", hbErr)
	if err := replacedErr(hbErr); err != nil {
		return err
	}
	err := a.reconnect(ctx)
	if errors.Is(err, errRegistrationRejected) {
		log.Printf("%v; exiting", err)
		return fmt.Errorf("registering with control plane: %w", err)
	}
	if err != nil {
		log.Printf("Reconnect failed: %v", err)
	}
	return nil
}

func (a *Agent) sendHeartbeat(ctx context.Context) (time.Duration, error) {
	a.mu.RLock()
	agentID := a.agentID
	a.mu.RUnlock()

	req := &agentpb.HeartbeatRequest{
		AgentId: agentID,
		Status:  agentpb.AgentStatus_AGENT_STATUS_HEALTHY,
	}

	// Add timeout for heartbeat
	hbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := a.client.Heartbeat(hbCtx, req)
	if err != nil {
		return 0, fmt.Errorf("heartbeat RPC failed: %w", err)
	}

	if !resp.Acknowledged {
		return 0, fmt.Errorf("heartbeat not acknowledged")
	}
	a.noteBindingsVersion(resp.GetDesiredBindingsVersion())

	return time.Duration(resp.NextHeartbeatSeconds) * time.Second, nil
}

// touchHealthFile updates the mtime of the health file, creating it if absent.
func (a *Agent) touchHealthFile() {
	if a.config.HealthFile == "" {
		return
	}
	now := time.Now()
	if err := os.Chtimes(a.config.HealthFile, now, now); err != nil {
		// File may not exist yet; create it.
		if f, cerr := os.Create(a.config.HealthFile); cerr == nil {
			f.Close()
		}
	}
}

func (a *Agent) reconnect(ctx context.Context) error {
	log.Printf("Attempting to reconnect to control plane")

	// Close existing connection
	a.disconnect()

	// Retry connection with exponential backoff
	backoff := time.Second
	maxBackoff := 30 * time.Second
	maxRetries := 5

	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-a.stopCh:
			return fmt.Errorf("agent stopped during reconnection")
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := a.connect(ctx); err != nil {
			log.Printf("Reconnect attempt %d failed: %v", attempt, err)
			time.Sleep(backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// Re-register after successful connection
		if err := a.register(ctx); err != nil {
			a.disconnect()
			if errors.Is(err, errRegistrationRejected) {
				return err
			}
			log.Printf("Re-registration failed: %v", err)
			time.Sleep(backoff)
			continue
		}

		log.Printf("Reconnected successfully")
		return nil
	}

	return fmt.Errorf("failed to reconnect after %d attempts", maxRetries)
}

// runCommandPollLoop polls control plane for pending commands until ctx ends
// and executes them under cmdCtx. It returns an error only when this agent
// was replaced.
func (a *Agent) runCommandPollLoop(ctx, cmdCtx context.Context) error {
	ticker := time.NewTicker(DefaultPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := a.pollAndExecuteCommands(ctx, cmdCtx); err != nil {
				return err
			}

		case <-a.stopCh:
			log.Printf("Command poll loop stopping")
			return nil

		case <-a.replacedC():
			return nil

		case <-ctx.Done():
			log.Printf("Context cancelled, stopping command poll loop")
			return nil
		}
	}
}

// pollAndExecuteCommands polls for pending commands and executes them. It
// returns errReplaced when a newer agent replaced this one.
func (a *Agent) pollAndExecuteCommands(ctx, cmdCtx context.Context) error {
	a.mu.RLock()
	agentID := a.agentID
	client := a.client
	a.mu.RUnlock()

	if agentID == "" || client == nil {
		return nil
	}

	// Poll for pending commands
	pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := client.GetPendingCommands(pollCtx, &agentpb.GetPendingCommandsRequest{
		AgentId: agentID,
	})
	if err != nil {
		// Don't log on every poll failure - connection issues are handled by heartbeat
		return a.replacedFor(err, agentID)
	}

	// Execute each command
	for _, cmd := range resp.Commands {
		log.Printf("Executing command %s: %v", cmd.RequestId, cmd.Command)
		a.executeAndSubmit(cmdCtx, cmd)
	}
	return nil
}

// executeAndSubmit executes a command under ctx and submits the result to
// control plane. ctx is the serve loop's cmdCtx, which shutdown never cancels:
// a stopping agent (the old pod of a rollout) lets the command finish and
// reports its real result. The pod's termination grace period bounds that; if
// SIGKILL comes first, the control plane fails the command when the agent's
// stream closes. Only a revoked token cancels ctx and kills the command.
func (a *Agent) executeAndSubmit(ctx context.Context, cmd *agentpb.CommandRequest) {
	// Determine timeout
	timeout := time.Duration(cmd.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	// Execute the kubectl command with optional stdin
	result := a.executor.ExecuteWithStdin(ctx, Identity{User: cmd.GetIdentity().GetUser(), RequestID: cmd.GetRequestId()}, cmd.Command, cmd.Namespace, timeout, cmd.Stdin)

	// Build result request
	submitReq := &agentpb.SubmitCommandResultRequest{
		RequestId: cmd.RequestId,
		Stdout:    result.Stdout,
		Stderr:    result.Stderr,
		ExitCode:  int32(result.ExitCode),
	}
	if result.Error != nil {
		submitReq.ErrorMessage = result.Error.Error()
	}

	// Submit result to control plane
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()

	submitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err := client.SubmitCommandResult(submitCtx, submitReq)
	if err != nil {
		log.Printf("Failed to submit command result for %s: %v", cmd.RequestId, err)
	} else {
		log.Printf("Command %s completed with exit code %d", cmd.RequestId, result.ExitCode)
	}
}
