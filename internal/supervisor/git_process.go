package supervisor

import (
	"context"
	"os/exec"
	"time"
)

// RunGitProcess runs one native Git invocation with its helper descendants.
// It shares terminal handling with native clients but owns a separate process tree.
func RunGitProcess(ctx context.Context, command *exec.Cmd, streams IO) (int, error) {
	if ctx.Err() != nil {
		return 130, nil
	}
	command.Stdin, command.Stdout, command.Stderr = streams.Stdin, streams.Stdout, streams.Stderr
	configureNativeProcess(command)
	restore, err := prepareNativeTTY(streams, command)
	if err != nil {
		return 1, err
	}
	defer restore()
	tree, err := startGitTree(command)
	if err != nil {
		return 1, err
	}
	defer tree.close()
	signals, stop := watchSignals()
	defer stop()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	for {
		select {
		case err := <-done:
			return nativeExitCode(command, err), nil
		case value := <-signals:
			forwardNativeSignal(command, value)
			if isResizeSignal(value) {
				continue
			}
		case <-ctx.Done():
			tree.interrupt()
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			tree.close()
			<-done
		}
		return 130, nil
	}
}
