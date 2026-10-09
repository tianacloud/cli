package main

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/localstate"
	"github.com/urfave/cli/v3"
)

type managementJSONKey struct{}

func managementJSON(ctx context.Context) bool {
	enabled, _ := ctx.Value(managementJSONKey{}).(bool)
	return enabled
}

type countedJSONWriter struct {
	io.Writer
	written int
}

func (w *countedJSONWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.written += n
	return n, err
}

// Detect requested management output even when parsing fails before --json.
// Skip option values and -- operands; native streams and SQL keep their contracts.
func requestedManagementJSON(root *cli.Command, args []string) bool {
	current := root
	requested := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if current.SkipFlagParsing {
			return false
		}
		if strings.HasPrefix(arg, "-") {
			name, value, assigned := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			var found cli.Flag
			for _, scope := range []*cli.Command{current, root} {
				for _, flag := range scope.Flags {
					for _, alias := range flag.Names() {
						if alias == name {
							found = flag
						}
					}
				}
			}
			if found == nil {
				continue
			}
			if name == "json" {
				requested = true
				if assigned {
					if enabled, err := strconv.ParseBool(value); err == nil {
						requested = enabled
					}
				}
				continue
			}
			if doc, ok := found.(cli.DocGenerationFlag); ok && doc.TakesValue() && !assigned {
				i++
			}
			continue
		}
		for _, child := range current.Commands {
			if child.Name == arg {
				current = child
				break
			}
		}
	}
	return requested && !current.SkipFlagParsing && current.Name != "shell"
}

func managementFailure(err error, output, diagnostics io.Writer, data any) int {
	failure := &apppublish.Error{Code: "MANAGEMENT_FAILED", Message: "Management command did not complete", NextAction: "Inspect command status and diagnostics before retrying", ExitCode: 1}
	var local *localstate.Error
	switch {
	case errors.As(err, &local):
		failure.Code = local.Code
		failure.Message = safeDisplay(local.Error())
		failure.NextAction = "Set XDG_CONFIG_HOME to a writable directory or repair local state permissions"
		if local.Code == "LOCAL_STATE_BUSY" {
			failure.NextAction = "Wait for the other CLI command to finish, then retry"
		}
		if local.Code == "LEGACY_PENDING_COMMAND" {
			failure.NextAction = "Set TIANA_PENDING_COMMAND_FILE to the previous path and recover the original command"
		}
	case errors.Is(err, authclient.ErrAuthenticationRequired), errors.Is(err, authclient.ErrCredentialNotFound):
		failure.Code = "AUTH_REQUIRED"
		failure.Message = "Account authentication is required"
		failure.NextAction = "Run tiana login --start --no-open --json"
	case errors.Is(err, authclient.ErrInstanceNotFound):
		failure.Code = "INSTANCE_NOT_FOUND"
		failure.Message = "Instance not found"
		failure.NextAction = "Check the selected environment and instance ID"
	case errors.Is(err, context.Canceled):
		failure.Code = "CANCELLED"
		failure.Message = "Command cancelled"
		failure.NextAction = ""
		failure.ExitCode = 130
	}
	writeCommandError(diagnostics, err)
	result := apppublish.Failure(failure)
	result.Data = data
	return writeAppResult(result, true, output, diagnostics)
}

type instanceOutput struct {
	authclient.Instance
	CurrentJobID string             `json:"current_job_id,omitempty"`
	Branch       *authclient.Branch `json:"branch,omitempty"`
}

func instanceJSON(instance authclient.Instance) instanceOutput {
	job := ""
	if instance.CurrentJobID != 0 {
		job = strconv.FormatUint(instance.CurrentJobID, 10)
	}
	return instanceOutput{Instance: instance, CurrentJobID: job}
}
func instanceListJSON(instances []authclient.Instance) any {
	items := make([]any, 0, len(instances))
	for _, instance := range instances {
		items = append(items, instanceJSON(instance))
	}
	return map[string]any{"items": items}
}

func mutationJSONFailure(ctx context.Context, err error, data any, output, diagnostics io.Writer) int {
	var api *authclient.APIError
	if errors.As(err, &api) && api.Status < 500 && api.Code != "COMMIT_STATUS_UNKNOWN" {
		return managementFailure(err, output, diagnostics, data)
	}
	writeCommandError(diagnostics, err)
	code := 4
	if ctx.Err() != nil {
		code = 130
	}
	return writeAppResult(apppublish.Result{Status: "unknown", Data: data, Error: &apppublish.Error{Code: "MUTATION_OUTCOME_UNKNOWN", Message: "Mutation outcome is not confirmed", NextAction: "Inspect the original immutable target and operation; do not blindly repeat creation", ExitCode: code}}, true, output, diagnostics)
}

func observedMutationJSON(ctx context.Context, err error, data any, output, diagnostics io.Writer) int {
	if err == nil {
		return writeAppResult(apppublish.Success(data), true, output, diagnostics)
	}
	writeCommandError(diagnostics, err)
	code := 4
	if ctx.Err() != nil {
		code = 130
	}
	status := "unknown"
	errorCode := "OBSERVATION_STOPPED"
	if errors.Is(err, errOperationFailed) {
		status = "failed"
		errorCode = "OPERATION_FAILED"
		code = 1
	}
	return writeAppResult(apppublish.Result{Status: status, Data: data, Error: &apppublish.Error{Code: errorCode, Message: "Accepted operation completion was not confirmed", NextAction: "Query the saved operation and immutable target before another write", ExitCode: code}}, true, output, diagnostics)
}
