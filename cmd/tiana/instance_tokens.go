package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
)

type tokensCreateOptions struct {
	reference      string
	branch         string
	name           string
	expiration     string
	nonInteractive bool
}

type tokensCreateState struct {
	reference  string
	instanceID string
	endpointID string
	name       string
	key        string
	requestID  string
	expiresAt  int64
	jobID      uint64
	tokenID    string
}

func executeSQLiteTokensCreate(ctx context.Context, options tokensCreateOptions, args []string, output, errorOutput io.Writer) int {
	scope := sqliteManagementScope
	client, err := newAuthClient(ctx, errorOutput, options.nonInteractive)
	if err != nil {
		fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
		return 1
	}
	tokenStore, err := newInstanceTokenStore()
	if err != nil {
		fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
		return 1
	}
	pendingStore, err := newPendingStore()
	if err != nil {
		fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
		return 1
	}
	releasePending, err := pendingStore.Acquire(ctx)
	if err != nil {
		writeCommandError(errorOutput, err)
		return 1
	}
	defer releasePending()
	commandArgs := append([]string{"tokens", "create"}, args...)
	origin := client.Origin()
	pending, pendingErr := pendingStore.Load()
	if pendingErr != nil && !errors.Is(pendingErr, authclient.ErrPendingNotFound) {
		fmt.Fprintln(errorOutput, "tiana: cannot read pending command:", safeDisplay(pendingErr.Error()))
		return 1
	}
	pendingMatches := pendingErr == nil && pending.Command == "db.tokens.create" && pending.Origin == origin && sameStrings(pending.Args, commandArgs)
	if pendingErr == nil && !pendingMatches {
		reportUnfinishedOperation(errorOutput, pendingStore.Path, pending)
		return 1
	}
	currentUserID, err := loadCurrentUserID(client)
	if err != nil {
		writeCommandError(errorOutput, err)
		return 1
	}
	state := tokensCreateState{reference: options.reference, name: options.name}
	initialUserID := currentUserID
	createdAt := time.Now().UTC()
	if pendingMatches {
		state.instanceID = pending.InstanceID
		state.endpointID = pending.EndpointID
		if pending.InstanceID != "" {
			state.reference = ""
		}
		state.name = options.name
		if pending.TokenName != "" {
			state.name = pending.TokenName
		}
		state.key = pending.TokenIdempotencyKey
		state.requestID = pending.TokenRequestID
		state.expiresAt = pending.ExpiresAt
		state.jobID = pending.JobID
		state.tokenID = pending.TokenID
		if pending.UserID != "" {
			initialUserID = pending.UserID
		}
		if !pending.CreatedAt.IsZero() {
			createdAt = pending.CreatedAt
		}
	}
	if state.expiresAt == 0 {
		state.expiresAt, err = authclient.ResolveExpiresAt(time.Now().UTC(), options.expiration)
		if err != nil {
			fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
			return 2
		}
	}
	if state.key == "" {
		if state.key, err = authclient.NewTokenIdempotencyKey(); err != nil {
			fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
			return 1
		}
	}
	if state.requestID == "" {
		if state.requestID, err = authclient.NewRequestID(); err != nil {
			fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
			return 1
		}
	}
	savePending := func(userID string) error {
		return pendingStore.Save(authclient.PendingCommand{
			Command: "db.tokens.create", Args: commandArgs, IdempotencyKey: state.key,
			Origin: origin, UserID: userID, CreatedAt: createdAt,
			Step: "token", InstanceID: state.instanceID, EndpointID: state.endpointID,
			TokenIdempotencyKey: state.key, TokenRequestID: state.requestID,
			TokenName: state.name, ExpiresAt: state.expiresAt, JobID: state.jobID, TokenID: state.tokenID,
		})
	}
	// Persist the intent before authentication and before the write, so a lost
	// response can be resumed with the same identifiers.
	if err := savePending(initialUserID); err != nil {
		fmt.Fprintln(errorOutput, "tiana:", safeDisplay(err.Error()))
		return 1
	}
	exitCode := 0
	operation := func(operationContext context.Context, credential authclient.Credential) error {
		if pendingMatches && pending.UserID != "" && credential.User.ID != pending.UserID {
			key, keyErr := authclient.NewTokenIdempotencyKey()
			if keyErr != nil {
				return keyErr
			}
			requestID, requestErr := authclient.NewRequestID()
			if requestErr != nil {
				return requestErr
			}
			expiresAt, expiresErr := authclient.ResolveExpiresAt(time.Now().UTC(), options.expiration)
			if expiresErr != nil {
				return expiresErr
			}
			state = tokensCreateState{reference: options.reference, name: options.name, key: key, requestID: requestID, expiresAt: expiresAt}
			createdAt = time.Now().UTC()
			pendingMatches = false
		}
		if state.instanceID == "" {
			instance, resolveErr := client.ResolveInstance(operationContext, state.reference)
			if resolveErr != nil {
				// Resolution has no side effect, so a failure leaves nothing
				// to resume and must not block a later command.
				_ = pendingStore.Delete()
				return resolveErr
			}
			if err := scope.check(instance); err != nil {
				_ = pendingStore.Delete()
				return err
			}
			state.instanceID = instance.ID
			detail, err := client.ResolveBranch(operationContext, instance.ID, options.branch)
			if err != nil {
				_ = pendingStore.Delete()
				return err
			}
			if err := bindTokenEndpoint(&state.endpointID, detail.Branch.EndpointID, state.jobID); err != nil {
				_ = pendingStore.Delete()
				return err
			}
		} else {
			instance, err := client.GetInstance(operationContext, state.instanceID)
			if err != nil {
				return err
			}
			if err := scope.check(instance); err != nil {
				return err
			}
			// Once persisted, the immutable Endpoint remains the target of this intent.
			if state.endpointID == "" {
				detail, err := client.ResolveBranch(operationContext, state.instanceID, options.branch)
				if err != nil {
					return err
				}
				if err = bindTokenEndpoint(&state.endpointID, detail.Branch.EndpointID, state.jobID); err != nil {
					return err
				}
			}
		}
		if err := savePending(credential.User.ID); err != nil {
			return err
		}
		outcome := attemptInstanceToken(operationContext, client, tokenAttempt{
			InstanceID: state.instanceID, EndpointID: state.endpointID, Name: state.name, ExpiresAt: state.expiresAt,
			RequestID: state.requestID, JobID: state.jobID, TokenID: state.tokenID,
		})
		switch outcome.Outcome {
		case tokenDelivered:
			exitCode = deliverStandaloneToken(output, errorOutput, tokenStore, outcome.Result, state.instanceID)
			if exitCode == 0 {
				_ = pendingStore.Delete()
			}
			return errCommandReported
		case tokenUnknown:
			state.jobID = outcome.JobID
			state.tokenID = outcome.TokenID
			_ = savePending(credential.User.ID)
			fmt.Fprintln(errorOutput, "tiana: Token result is not yet confirmed; re-run the command to confirm it")
			if outcome.JobID != 0 {
				fmt.Fprintf(errorOutput, "Job ID: %d\n", outcome.JobID)
			}
			exitCode = 1
			return errCommandReported
		case tokenCommittedWithoutSecret:
			_ = pendingStore.Delete()
			reportTokenWithoutSecret(errorOutput, outcome.Result)
			exitCode = 1
			return errCommandReported
		case tokenAcceptedFailed:
			state.jobID = outcome.JobID
			state.tokenID = outcome.TokenID
			_ = savePending(credential.User.ID)
			fmt.Fprintln(errorOutput, "tiana: Token creation was accepted, but its delivery job failed; retry the job in the Console, then re-run this command to confirm it")
			if outcome.JobID != 0 {
				fmt.Fprintf(errorOutput, "Job ID: %d\n", outcome.JobID)
			}
			exitCode = 1
			return errCommandReported
		case tokenRejected, tokenFailed:
			_ = pendingStore.Delete()
			fmt.Fprintf(errorOutput, "tiana: Token creation failed: %s\n", safeDisplay(tokenFailureReason(outcome)))
			exitCode = 1
			return errCommandReported
		default:
			return outcome.Err
		}
	}
	err = client.RunAuthenticated(ctx, operation)
	if errors.Is(err, authclient.ErrAuthenticationRequired) {
		err = client.RunAuthenticated(ctx, operation)
	}
	if errors.Is(err, errCommandReported) {
		return exitCode
	}
	if err != nil {
		reportResolveError(errorOutput, options.reference, err)
		return 1
	}
	return 0
}

func deliverStandaloneToken(output, errorOutput io.Writer, store authclient.InstanceTokenStore, result authclient.TokenResult, instanceID string) int {
	fmt.Fprintln(output, result.Token)
	fmt.Fprintf(errorOutput, "Token ID: %s\n", safeDisplay(result.TokenID))
	fmt.Fprintf(errorOutput, "Name: %s\n", safeDisplay(result.Name))
	fmt.Fprintf(errorOutput, "Expires at: %s\n", safeDisplay(authclient.FormatExpiration(result.ExpiresAt)))
	location, err := saveInstanceTokenCredential(store, result, instanceID)
	if err != nil {
		fmt.Fprintf(errorOutput, "tiana: could not save the Token locally: %v\n", safeDisplay(err.Error()))
		fmt.Fprintf(errorOutput, "Token ID: %s\n", safeDisplay(result.TokenID))
		return 1
	}
	fmt.Fprintf(errorOutput, "Saved to: %s\n", safeDisplay(location))
	return 0
}

func reportTokenWithoutSecret(errorOutput io.Writer, result authclient.TokenResult) {
	fmt.Fprintln(errorOutput, "tiana: the Token was committed but its secret was not delivered")
	if result.TokenID != "" {
		fmt.Fprintf(errorOutput, "Token ID: %s\n", safeDisplay(result.TokenID))
	}
	fmt.Fprintln(errorOutput, "Revoke that Token in the existing console, confirm the revocation, then create a replacement explicitly.")
}

func loadCurrentUserID(client *authclient.Client) (string, error) {
	credential, err := client.LoadCredential()
	if errors.Is(err, authclient.ErrCredentialNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if credential.AccessToken == "" || credential.RefreshToken == "" {
		return "", nil
	}
	return credential.User.ID, nil
}
