package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/supervisor"
)

// errCommandReported marks a failure the operation already wrote to stderr;
// the dispatcher returns the recorded exit code without printing again.
var errCommandReported = errors.New("command failure already reported")

func isHelp(args []string) bool {
	return len(args) > 0 && (args[0] == "--help" || args[0] == "-h")
}

// reportUnfinishedOperation explains how to finish or abandon a locally
// recorded operation that a new invocation cannot replace without risking a
// duplicate resource.
func reportUnfinishedOperation(errorOutput io.Writer, path string, pending authclient.PendingCommand) {
	fmt.Fprintln(errorOutput, "tiana: an unfinished database operation must be completed first")
	switch pending.Command {
	case "git.create":
		fmt.Fprintf(errorOutput, "Finish the Git operation with:\n  tiana git %s\n", safeDisplay(quoteCommandArgs(pending.Args)))
	case "db.create":
		fmt.Fprintf(errorOutput, "Finish the SQLite operation with:\n  tiana sqlite %s\n", safeDisplay(quoteCommandArgs(pending.Args)))
	default:
		fmt.Fprintln(errorOutput, "The pending operation is not supported by this CLI.")
	}
	if path != "" {
		fmt.Fprintf(errorOutput, "Pending record: %s\nDo not delete it while the result is unknown.\n", safeDisplay(path))
	}
}

// instanceConnectionURL uses MGR's deployed connection metadata.
func instanceConnectionURL(instance authclient.Instance) (string, bool) {
	if instance.Connection != nil {
		if endpoint, err := supervisor.ParseEndpointURL(instance.Connection.URL); err == nil {
			if instance.Engine == "git" {
				if instance.EndpointID != endpoint.ID() || instance.Connection.Hostname != endpoint.Hostname() {
					return "", false
				}
				return "tiana://" + strings.TrimPrefix(endpoint.URL(), "https://") + "/repo.git", true
			}
			return endpoint.URL(), true
		}
	}
	return "", false
}

func connectionURLColumn(instance authclient.Instance) string {
	if url, ok := instanceConnectionURL(instance); ok {
		return url
	}
	return "-"
}

// instanceDisplayState projects deletion progress without changing MGR's
// product state. Stale runtime observations cannot override durable MGR facts.
func instanceDisplayState(instance authclient.Instance) string {
	lifecycle := instance.LifecycleState
	if instance.RuntimeStatusStale {
		lifecycle = ""
	}
	if instance.ProductState == "DELETED" || lifecycle == "DELETED" {
		return "DELETED"
	}
	if instance.DeletionPending || lifecycle == "DELETING" {
		return "DELETING"
	}
	return instance.ProductState
}

func runtimeStateDescription(instance authclient.Instance) string {
	if instance.RuntimeStatusStale {
		reason := instance.StaleReason
		if reason == "" {
			reason = "unknown"
		}
		return fmt.Sprintf("unknown (stale: %s)", reason)
	}
	desired := instance.DesiredState
	observed := instance.ObservedState
	switch {
	case desired != "" && observed != "":
		return fmt.Sprintf("desired=%s observed=%s", desired, observed)
	case desired != "":
		return "desired=" + desired
	case observed != "":
		return "observed=" + observed
	default:
		return "unknown"
	}
}

func quoteCommandArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " ")
}
