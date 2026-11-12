package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/urfave/cli/v3"
)

func newTokenCommand(output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "token", Usage: "Create instance-scoped data access tokens", Action: groupAction, Commands: []*cli.Command{
		{Name: "create", Usage: "Create a token covering all branches of one instance", ArgsUsage: "INSTANCE",
			Description: "Uses account login. The token is returned once in stdout; capture the receipt before configuring a backend secret. Default expiry is never. A missing token is not a usable credential; inspect the instance's tokens in Console before creating another.",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "name", Required: true, Usage: "Token purpose or name"},
				&cli.Int64Flag{Name: "expires-at", Value: -1, Usage: "Expiry as Unix seconds, or -1 for no expiry"},
				stringOption("request-id", "Request identity for this creation", ""),
				boolOption("json", "Write a structured JSON receipt including the one-time token"),
			}, Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.NArg() != 1 || strings.TrimSpace(cmd.Args().First()) == "" || strings.TrimSpace(cmd.String("name")) == "" {
					return argumentFailure(ctx, cmd, "one instance ID or exact name and --name are required")
				}
				return statusError(createInstanceToken(ctx, cmd, output, diagnostics))
			}},
	}}
}

func createInstanceToken(ctx context.Context, cmd *cli.Command, output, diagnostics io.Writer) int {
	fail := func(err error) int {
		if cmd.Bool("json") {
			return managementFailure(err, output, diagnostics, nil)
		}
		writeCommandError(diagnostics, err)
		return 1
	}
	release, err := lockManagementMutation(ctx, diagnostics)
	if err != nil {
		return fail(err)
	}
	defer release()
	account, err := newAuthClient(ctx, diagnostics, true)
	if err != nil {
		return fail(err)
	}
	instance, credential, err := resolveInstanceAccountWithLogin(ctx, account, cmd.Args().First())
	if err != nil {
		return fail(err)
	}
	requestID := cmd.String("request-id")
	if requestID == "" {
		requestID, err = authclient.NewIdempotencyKey()
		if err != nil {
			return fail(err)
		}
	}
	fmt.Fprintln(diagnostics, "Token request ID:", safeDisplay(requestID))
	data := map[string]any{"instance_id": instance.ID, "request_id": requestID}
	receipt, err := account.WithCredential(credential).CreateInstanceToken(ctx, instance.ID, authclient.CreateInstanceTokenRequest{RequestID: requestID, Name: cmd.String("name"), ExpiresAt: cmd.Int64("expires-at")})
	if err != nil {
		var api *authclient.APIError
		if errors.As(err, &api) && api.Status < 500 && api.Code != "COMMIT_STATUS_UNKNOWN" && api.Code != "RESULT_UNKNOWN" {
			return fail(err)
		}
		return writeAppResult(apppublish.Result{Status: "unknown", Data: data, Error: &apppublish.Error{Code: "TOKEN_OUTCOME_UNKNOWN", Message: "Token creation outcome is unknown", NextAction: "Inspect this instance's tokens in Console before another creation; the original plaintext cannot be recovered", ExitCode: 4}}, cmd.Bool("json"), output, diagnostics)
	}
	data["token_id"], data["name"], data["expires_at"] = receipt.TokenID, receipt.Name, receipt.ExpiresAt
	data["sync_status"], data["secret_recoverable"] = receipt.SyncStatus, receipt.SecretRecoverable
	if receipt.JobID != 0 {
		data["job_id"] = strconv.FormatUint(receipt.JobID, 10)
	}
	if receipt.Token == "" || receipt.SyncStatus != "complete" {
		status := "pending"
		if receipt.SyncStatus == "complete" || receipt.SyncStatus == "failed" {
			status = "failed"
		}
		return writeAppResult(apppublish.Result{Status: status, Data: data, Error: &apppublish.Error{Code: "TOKEN_NOT_DELIVERED", Message: "Token metadata was returned without a usable credential", NextAction: "Inspect this token in Console; revoke it and explicitly create a replacement if plaintext was not delivered", ExitCode: 4}}, cmd.Bool("json"), output, diagnostics)
	}
	data["token"] = receipt.Token
	code := writeAppResult(apppublish.Success(data), cmd.Bool("json"), output, diagnostics)
	if code != 0 {
		fmt.Fprintln(diagnostics, "Token created but receipt output failed. Inspect token", safeDisplay(receipt.TokenID), "in Console; plaintext cannot be recovered.")
	}
	return code
}
