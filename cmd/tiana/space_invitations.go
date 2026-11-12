package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
)

type spaceInviteResult struct {
	UserID     string                      `json:"user_id"`
	Status     string                      `json:"status"`
	RequestID  string                      `json:"request_id,omitempty"`
	Invitation *authclient.SpaceInvitation `json:"invitation,omitempty"`
	Error      *apppublish.Error           `json:"error,omitempty"`
}

func inviteSpaceUsers(ctx context.Context, client *authclient.Client, credential authclient.Credential, users []string, output, diagnostics io.Writer) int {
	invitations, err := client.ListSpaceInvitations(ctx)
	if err != nil {
		return spaceFailure(ctx, err, false, nil, "No invitations were submitted; check the current space and your management role", output, diagnostics)
	}
	revisions := make(map[string]uint64, len(invitations))
	for _, invitation := range invitations {
		revisions[invitation.UserID] = invitation.Revision
	}
	items := make([]spaceInviteResult, 0, len(users))
	seen := map[string]bool{}
	for _, user := range users {
		if !seen[user] {
			items = append(items, spaceInviteResult{UserID: user, Status: "not_executed"})
			seen[user] = true
		}
	}
	exitCode := 0
	for i := range items {
		if ctx.Err() != nil {
			exitCode = 130
			break
		}
		item := &items[i]
		item.RequestID, err = authclient.NewIdempotencyKey()
		if err != nil {
			return spaceFailure(ctx, err, false, map[string]any{"items": items}, "Inspect the results before another invite", output, diagnostics)
		}
		if _, err = fmt.Fprintf(diagnostics, "Invitation request: user=%s request=%s revision=%s\n", safeDisplay(item.UserID), safeDisplay(item.RequestID), strconv.FormatUint(revisions[item.UserID], 10)); err != nil {
			return 1
		}
		invitation, err := client.InviteSpaceMember(ctx, item.UserID, revisions[item.UserID], item.RequestID)
		if err == nil {
			item.Status, item.Invitation = "succeeded", &invitation
			continue
		}
		writeCommandError(diagnostics, err)
		item.Error = spaceCommandError(ctx, err, true, "Inspect space invitation list and space member list; another invite sends a new message")
		item.Status = "failed"
		if item.Error.ExitCode == 4 || item.Error.ExitCode == 130 {
			item.Status = "unknown"
		}
		if item.Error.ExitCode > exitCode {
			exitCode = item.Error.ExitCode
		}
		if errors.Is(err, authclient.ErrAuthenticationRequired) || ctx.Err() != nil {
			break
		}
	}
	data := map[string]any{"tenant_id": credential.User.TenantID, "items": items}
	if managementJSON(ctx) {
		result := apppublish.Success(data)
		if exitCode != 0 {
			result = apppublish.Failure(&apppublish.Error{Code: "SPACE_INVITATIONS_INCOMPLETE", Message: "Some invitations did not complete; inspect per-user results", NextAction: "Inspect sent invitations and members before another invite", ExitCode: exitCode})
			result.Data = data
		}
		return writeAppResult(result, true, output, diagnostics)
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		id, detail := "", ""
		if item.Invitation != nil {
			id = item.Invitation.ID
		}
		if item.Error != nil {
			detail = item.Error.Message
		}
		rows = append(rows, []string{item.UserID, item.Status, id, detail})
	}
	if code := writeSpaceTable([]string{"USER ID", "RESULT", "INVITATION ID", "DETAIL"}, rows, output); code != 0 {
		return code
	}
	return exitCode
}
