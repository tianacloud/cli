package supervisor

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

const (
	HelperContractVersion uint16 = 3
	HelperDrainTimeout           = 5 * time.Second
	HelperStopTimeout            = 2 * time.Second
)

type Supervisor struct {
	Registry       *AdapterRegistry
	Launcher       HelperLauncher
	IO             IO
	DrainTimeout   time.Duration
	StopTimeout    time.Duration
	InvocationIDFn func() (string, error)
}

func NewSupervisor(launcher HelperLauncher) *Supervisor {
	return &Supervisor{
		Registry:     NewAdapterRegistry(),
		Launcher:     launcher,
		IO:           DefaultIO(),
		DrainTimeout: HelperDrainTimeout,
		StopTimeout:  HelperStopTimeout,
	}
}

// Run starts one native child only after the helper has reached READY and the
// caller's security policy accepts the actual BOUND capability. It returns the
// native exit status when a child was successfully started.
func (s *Supervisor) Run(ctx context.Context, options ConnectOptions) (int, error) {
	if s == nil || s.Launcher == nil || len(options.NativeArgv) == 0 {
		return 1, ErrEmptyNativeClient
	}
	if s.Registry == nil {
		s.Registry = NewAdapterRegistry()
	}
	if options.Profile != "" && !validSQLDProfile(options.Profile) {
		return 1, ErrUnsupportedProfile
	}
	adapter, err := s.Registry.Select(options.NativeArgv[0], options.AdapterID)
	if err != nil {
		return 1, err
	}
	endpoint, err := adapter.ResolveEndpoint(options.NativeArgv)
	if err != nil {
		return 1, err
	}
	gatewayOptions, err := resolveGatewayDialTarget(options.Gateway, endpoint)
	if err != nil {
		return 1, err
	}
	gatewayTrust, err := resolveGatewayTrust(gatewayOptions)
	if err != nil {
		return 1, err
	}
	stdin, stderr := s.IO.Stdin, s.IO.Stderr
	if stdin == nil {
		stdin = os.Stdin
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	token, err := readConnectCredential(ctx, options, stdin)
	if err != nil {
		return 1, err
	}
	destroyToken := func() {
		if token != nil {
			token.Destroy()
			token = nil
		}
	}
	defer destroyToken()
	helper, err := s.Launcher.Launch(ctx, options.Credential)
	if err != nil {
		return 1, err
	}
	if helper == nil {
		return 1, ErrHelperProtocol
	}
	defer helper.Close()

	invocationID := ""
	if s.InvocationIDFn != nil {
		invocationID, err = s.InvocationIDFn()
	} else {
		invocationID, err = randomInvocationID()
	}
	if err != nil {
		return 1, fmt.Errorf("%w: invocation identity", ErrHelperProtocol)
	}
	capabilities, err := helper.Handshake(ctx, []uint16{HelperContractVersion}, invocationID)
	if err != nil {
		return 1, fmt.Errorf("helper handshake failed: %w", err)
	}
	if capabilities.SelectedVersion != HelperContractVersion || !capabilities.SQLDAdapter {
		return 1, fmt.Errorf("%w: incompatible helper capabilities (contract=%d, expected=%d)", ErrHelperProtocol, capabilities.SelectedVersion, HelperContractVersion)
	}
	if options.Profile != "" && !capabilities.SQLDExplicitProfile {
		return 1, ErrHelperProtocol
	}
	config := HelperConfig{
		Endpoint:        endpoint.Hostname(),
		AdapterID:       SQLDAdapterID,
		AllowedProfile:  []string{ProfileHranaHTTP, ProfileHranaWebSocket},
		SelectionMode:   SelectionModeBoundedHTTPHeaderClassifier,
		ExplicitProfile: "",
		Deadline:        SQLDClassifierDeadline,
		HeaderLimit:     SQLDHeaderLimit,
		FieldLimit:      SQLDFieldLimit,
		Pre200Limit:     SQLDPre200Limit,
		RequireToken:    false,
		GatewayAddress:  gatewayTrust.Address,
		UseWebPKIRoots:  gatewayTrust.UseWebPKIRoots,
		RootCertDER:     gatewayTrust.RootCertDER,
		InsecureTLS:     gatewayTrust.InsecureTLS,
	}
	if options.Profile != "" {
		config.SelectionMode = SelectionModeExplicitProfile
		config.ExplicitProfile = options.Profile
	}
	if err := helper.Configure(ctx, config); err != nil {
		return 1, err
	}
	credentialErr := helper.DeliverCredential(ctx, token)
	// The helper owns any retained protected copy after this call. Go must not
	// keep the outer token alive through the native child lifetime. This also
	// runs on short-write and helper-rejection paths.
	destroyToken()
	if credentialErr != nil {
		return 1, credentialErr
	}
	bound, err := helper.WaitBound(ctx)
	if err != nil {
		return 1, err
	}
	if _, _, _, endpointErr := localTCPParts(bound.Endpoint, options.Profile); endpointErr != nil {
		return 1, fmt.Errorf("%w: bound locator", ErrHelperProtocol)
	}
	interactive := options.Interactive && isTTYReader(stdin)
	if err := options.Security.Check(bound.Endpoint.SecurityLevel, interactive, stderr); err != nil {
		return 1, err
	}
	ready, err := helper.WaitReady(ctx)
	if err != nil {
		return 1, err
	}
	localEndpoint := bound.Endpoint
	if ready.Endpoint.URL != "" {
		if !sameLocalEndpoint(bound.Endpoint, ready.Endpoint) {
			return 1, ErrHelperProtocol
		}
		localEndpoint = ready.Endpoint
	}
	prepared, err := adapter.Prepare(localEndpoint, options.Profile, options.NativeArgv, defaultChildEnvironment(options.Credential))
	if err != nil {
		return 1, err
	}
	child, restoreTTY, err := spawnNative(prepared, s.IO)
	if err != nil {
		_ = drainHelper(context.Background(), helper, s.DrainTimeout, s.StopTimeout)
		return 1, fmt.Errorf("start native client: %w", err)
	}
	defer restoreTTY()
	identity, err := newChildIdentity(child.Process.Pid)
	if err != nil {
		_ = terminateNative(child)
		_ = drainHelper(context.Background(), helper, s.DrainTimeout, s.StopTimeout)
		return 1, fmt.Errorf("%w: child identity", ErrHelperProtocol)
	}
	if err := helper.ChildStarted(ctx, identity); err != nil {
		_ = terminateNative(child)
		_ = drainHelper(context.Background(), helper, s.DrainTimeout, s.StopTimeout)
		return 1, err
	}

	status, waitErr := waitNative(ctx, child, helper)
	drainErr := drainHelper(context.Background(), helper, s.DrainTimeout, s.StopTimeout)
	if waitErr != nil {
		return status, waitErr
	}
	if drainErr != nil {
		var failure SessionFailureError
		if status == 0 && errors.As(drainErr, &failure) && failure.Phase == SessionFailureAfterConnect && failure.Code == "LOCAL_CLIENT_CLOSED" {
			return 0, nil
		}
		if status == 0 {
			return 1, drainErr
		}
		return status, drainErr
	}
	return status, nil
}

func randomInvocationID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("inv-%x", bytes[:]), nil
}

func spawnNative(prepared PreparedCommand, streams IO) (*exec.Cmd, func(), error) {
	if prepared.Program == "" || len(prepared.Argv) == 0 || prepared.Argv[0] != prepared.Program {
		return nil, func() {}, ErrEmptyNativeClient
	}
	command := exec.Command(prepared.Program, prepared.Argv[1:]...)
	command.Env = append([]string(nil), prepared.Env...)
	if streams.Stdin != nil {
		command.Stdin = streams.Stdin
	}
	if streams.Stdout != nil {
		command.Stdout = streams.Stdout
	}
	if streams.Stderr != nil {
		command.Stderr = streams.Stderr
	}
	configureNativeProcess(command)
	restoreTTY, err := prepareNativeTTY(streams, command)
	if err != nil {
		return nil, func() {}, err
	}
	if err := command.Start(); err != nil {
		restoreTTY()
		return nil, func() {}, err
	}
	return command, restoreTTY, nil
}

func waitNative(ctx context.Context, child *exec.Cmd, helper HelperClient) (int, error) {
	wait := make(chan error, 1)
	go func() { wait <- child.Wait() }()
	var helperExit <-chan error
	if completion, ok := helper.(interface{ Done() <-chan error }); ok {
		helperExit = completion.Done()
	}
	signals, stopSignals := watchSignals()
	defer stopSignals()
	for {
		select {
		case err := <-wait:
			return nativeExitCode(child, err), errForNativeWait(err)
		case helperErr := <-helperExit:
			helperExit = nil
			status, waitErr := terminateNativeAndWait(child, wait)
			if helperErr != nil {
				return status, fmt.Errorf("%w: helper exited", ErrHelperProtocol)
			}
			return status, errForNativeWait(waitErr)
		case signal := <-signals:
			if isResizeSignal(signal) {
				forwardNativeSignal(child, signal)
				continue
			}
			_ = boundedDrainRequest(helper, HelperDrainTimeout)
			status, waitErr := terminateNativeAndWait(child, wait)
			return status, errForNativeWait(waitErr)
		case <-ctx.Done():
			_ = boundedDrainRequest(helper, HelperDrainTimeout)
			status, _ := terminateNativeAndWait(child, wait)
			return status, ctx.Err()
		}
	}
}

func waitStatus(wait <-chan error) <-chan int {
	result := make(chan int, 1)
	go func() {
		err := <-wait
		if err == nil {
			result <- 0
			return
		}
		if exitError, ok := err.(*exec.ExitError); ok {
			result <- exitError.ExitCode()
			return
		}
		result <- 1
	}()
	return result
}

func errForNativeWait(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return nil
	}
	return fmt.Errorf("native client exited: %w", err)
}

func terminateNative(child *exec.Cmd) error {
	if child == nil || child.Process == nil {
		return nil
	}
	wait := make(chan error, 1)
	go func() { wait <- child.Wait() }()
	_, err := terminateNativeAndWait(child, wait)
	return err
}

func terminateNativeAndWait(child *exec.Cmd, wait <-chan error) (int, error) {
	if child == nil || child.Process == nil {
		return 0, nil
	}
	forwardNativeSignal(child, os.Interrupt)
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case err := <-wait:
		return nativeExitCode(child, err), err
	case <-timer.C:
		_ = child.Process.Kill()
		err := <-wait
		return nativeExitCode(child, err), err
	}
}

func drainHelper(ctx context.Context, helper HelperClient, drainTimeout, stopTimeout time.Duration) error {
	if helper == nil {
		return nil
	}
	if drainTimeout <= 0 {
		drainTimeout = HelperDrainTimeout
	}
	if stopTimeout <= 0 {
		stopTimeout = HelperStopTimeout
	}
	ctx = contextOrBackground(ctx)
	drainCtx, cancel := context.WithTimeout(ctx, drainTimeout)
	drainErr := helper.Drain(drainCtx)
	cancel()
	if drainErr != nil {
		return drainErr
	}
	stopCtx, stopCancel := context.WithTimeout(ctx, stopTimeout)
	stopErr := helper.WaitStopped(stopCtx)
	stopCancel()
	return stopErr
}

func boundedDrainRequest(helper HelperClient, timeout time.Duration) error {
	if helper == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = HelperDrainTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return helper.Drain(ctx)
}

func sameLocalEndpoint(left, right LocalEndpoint) bool {
	return left.Network == right.Network && left.Address == right.Address && left.URL == right.URL && left.SecurityLevel == right.SecurityLevel
}

var _ io.Reader
