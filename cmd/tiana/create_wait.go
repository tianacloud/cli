package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/urfave/cli/v3"
)

func createWaitOption() cli.Flag {
	return &cli.BoolFlag{Name: "wait", Aliases: []string{"w"}, Usage: "Wait for creation to succeed; Ctrl-C stops waiting", Local: true}
}

// Waiting changes observation only, never the persisted mutation identity.
// Preserve positional -- and everything after it, including a name of "-w".
func createIdentityArguments(args []string) []string {
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-m" || arg == "--message" {
			result = append(result, arg)
			if i+1 < len(args) {
				i++
				result = append(result, args[i])
			}
			continue
		}
		if arg == "--" {
			return append(result, args[i:]...)
		}
		if arg == "--wait" || arg == "-w" || strings.HasPrefix(arg, "--wait=") || strings.HasPrefix(arg, "-w=") {
			continue
		}
		result = append(result, arg)
	}
	return result
}

type creationObserver interface {
	GetJob(context.Context, uint64) (authclient.Job, error)
	GetInstance(context.Context, string) (authclient.Instance, error)
}

// The current MGR removes successful jobs atomically with publishing the
// instance. A missing job alone is not proof of successful creation.
func waitForInstanceCreation(ctx context.Context, client creationObserver, pending authclient.PendingCommand, scope databaseScope, interval time.Duration) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		checkInstance := pending.CreationJobID == 0
		if !checkInstance {
			job, err := client.GetJob(ctx, pending.CreationJobID)
			if err != nil {
				var apiErr *authclient.APIError
				if !errors.As(err, &apiErr) || apiErr.Status != 404 {
					return err
				}
				checkInstance = true
			} else {
				if job.JobID != pending.CreationJobID || job.InstanceID != pending.InstanceID || job.JobKind != "instance_create" || job.RequestID != pending.IdempotencyKey {
					return errors.New("creation job does not match the saved instance and request; pending record preserved")
				}
				switch job.Status {
				case "pending", "running":
				case "complete":
					checkInstance = true
				case "fail":
					if !job.RetryCompleted {
						return errors.New("instance creation job failed; inspect or retry the job in the console; pending record preserved")
					}
					checkInstance = true
				default:
					return errors.New("unrecognized creation job status; pending record preserved")
				}
			}
		}
		if checkInstance {
			instance, err := client.GetInstance(ctx, pending.InstanceID)
			if err != nil {
				return err
			}
			if instance.ID != pending.InstanceID {
				return errors.New("creation result does not match the saved instance")
			}
			if err := scope.check(instance); err != nil {
				return err
			}
			if pending.CreationOperationID != "" && instance.CreationOperationID != pending.CreationOperationID {
				return errors.New("creation result does not match the saved operation")
			}
			if instance.DeletionPending || instance.ProductState == "DELETED" || (!instance.RuntimeStatusStale && instance.LifecycleState == "DELETING") {
				return errors.New("instance was deleted or is being deleted while waiting for creation")
			}
			switch instance.ProductState {
			case "ACTIVE":
				return nil
			case "FAILED":
				return errors.New("instance creation failed; pending record preserved")
			case "PENDING", "UNKNOWN":
			default:
				return errors.New("unrecognized instance creation state; pending record preserved")
			}
			if pending.CreationJobID == 0 {
				return errors.New("creation accepted without a job ID; cannot wait for this server; inspect the instance or repeat without --wait")
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
