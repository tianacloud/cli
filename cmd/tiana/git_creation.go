package main

import (
	"context"
	"errors"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

// The pending instance ID is enough to recover creation: MGR durably stores
// creation_operation_id on that instance. Token OperationID stays separate and
// empty until its own write. No local state-format migration is needed.
func waitInstanceCreation(ctx context.Context, client *authclient.Client, receipt authclient.Instance, scope databaseScope) (authclient.Instance, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	instance, err := client.GetInstance(ctx, receipt.ID)
	if err != nil {
		return authclient.Instance{}, err
	}
	if instance.ID != receipt.ID {
		return authclient.Instance{}, errors.New("creation metadata has a different instance ID")
	}
	if err := scope.check(instance); err != nil {
		return authclient.Instance{}, err
	}
	operationID := receipt.CreationOperationID
	if operationID == "" {
		operationID = instance.CreationOperationID
	}
	if instance.CreationOperationID != "" && instance.CreationOperationID != operationID {
		return authclient.Instance{}, errors.New("creation operation changed; pending instance preserved")
	}
	if operationID == "" {
		return instance, nil
	} // Legacy synchronous MGR.
	for {
		operation, err := client.GetInstanceOperation(ctx, instance.ID, operationID)
		if err != nil {
			return authclient.Instance{}, err
		}
		switch operation.State {
		case "success":
			result, err := client.GetInstance(ctx, instance.ID)
			if err != nil {
				return authclient.Instance{}, err
			}
			if result.ID != instance.ID || result.CreationOperationID != operationID {
				return authclient.Instance{}, errors.New("completed creation metadata does not match the accepted instance")
			}
			if err := scope.check(result); err != nil {
				return authclient.Instance{}, err
			}
			return result, nil
		case "failed":
			return authclient.Instance{}, errors.New("instance creation failed; pending instance preserved, inspect its operation in the console")
		case "running", "pending", "queued":
		default:
			return authclient.Instance{}, errors.New("unrecognized creation operation state; pending instance preserved")
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return authclient.Instance{}, errors.New("instance creation is not confirmed; repeat the same command to resume")
		case <-timer.C:
		}
	}
}
