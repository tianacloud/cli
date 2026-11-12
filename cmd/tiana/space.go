package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/urfave/cli/v3"
)

func newSpaceCommand(input io.Reader, output, diagnostics io.Writer) *cli.Command {
	leaf := func(name, usage, args string, count int, flags []cli.Flag, action func(context.Context, *cli.Command, *authclient.Client, authclient.Credential) int) *cli.Command {
		return &cli.Command{Name: name, Usage: usage, ArgsUsage: args, Flags: append([]cli.Flag{boolOption("json", "Write a structured JSON result")}, flags...), Action: func(ctx context.Context, cmd *cli.Command) error {
			if (count >= 0 && cmd.NArg() != count) || (count < 0 && cmd.NArg() == 0) {
				return argumentFailure(ctx, cmd, "expected "+args)
			}
			for _, arg := range cmd.Args().Slice() {
				if strings.TrimSpace(arg) == "" {
					return argumentFailure(ctx, cmd, "arguments must not be blank")
				}
				if positionalWasTrimmed(cmd, arg) {
					return argumentFailure(ctx, cmd, "use -- before names to preserve surrounding whitespace")
				}
			}
			client, err := newAuthClient(ctx, diagnostics, true)
			var credential authclient.Credential
			if err == nil {
				credential, err = client.EnsureCredential(ctx)
			}
			if err != nil {
				return statusError(spaceFailure(ctx, err, false, nil, "Run tiana login, then retry", output, diagnostics))
			}
			return statusError(action(ctx, cmd, client.WithCredential(credential), credential))
		}}
	}
	force := func() []cli.Flag {
		return []cli.Flag{&cli.BoolFlag{Name: "force", Aliases: []string{"f"}, Usage: "Skip terminal confirmation", Local: true}}
	}
	list := leaf("list", "List your spaces", "no arguments", 0, nil, func(ctx context.Context, cmd *cli.Command, c *authclient.Client, credential authclient.Credential) int {
		items, err := c.ListSpaces(ctx)
		if err != nil {
			return spaceFailure(ctx, err, false, nil, "Retry space list", output, diagnostics)
		}
		data := map[string]any{"items": items, "current_tenant_id": credential.User.TenantID}
		rows := make([][]string, 0, len(items))
		for _, item := range items {
			current := ""
			if item.TenantID == credential.User.TenantID {
				current = "current"
			}
			rows = append(rows, []string{item.TenantID, spaceName(item), item.Role, current, strconv.FormatBool(item.IsDefault)})
		}
		return spaceResult(ctx, data, []string{"SPACE ID", "NAME", "ROLE", "CURRENT", "DEFAULT"}, rows, output, diagnostics)
	})
	show := leaf("show", "Show the current space", "no arguments", 0, nil, func(ctx context.Context, cmd *cli.Command, c *authclient.Client, _ authclient.Credential) int {
		space, err := c.CurrentSpace(ctx)
		if err != nil {
			return spaceFailure(ctx, err, false, nil, "Retry space show", output, diagnostics)
		}
		return spaceResult(ctx, space, []string{"FIELD", "VALUE"}, [][]string{{"Space ID", space.TenantID}, {"Name", spaceName(space.Space)}, {"Role", space.Role}, {"Default", strconv.FormatBool(space.IsDefault)}, {"Can manage members", strconv.FormatBool(space.CanManage)}, {"Can set admin", strconv.FormatBool(space.CanSetAdmin)}}, output, diagnostics)
	})
	switchSpace := leaf("switch", "Switch the current login to a space", "SPACE_ID", 1, nil, func(ctx context.Context, cmd *cli.Command, c *authclient.Client, _ authclient.Credential) int {
		id := cmd.Args().First()
		data := map[string]any{"tenant_id": id}
		switched, err := c.SwitchSpace(ctx, id)
		if err != nil {
			return spaceFailure(ctx, err, true, data, "If the switch outcome is unknown, run tiana login; do not replay the switch", output, diagnostics)
		}
		if err = c.SaveCredential(switched.Credential); err != nil {
			return spaceFailure(ctx, err, false, data, "The server switched spaces but the new credentials were not saved. Run tiana login", output, diagnostics)
		}
		data["sync_status"] = switched.SyncStatus
		if switched.JobID != 0 {
			data["job_id"] = strconv.FormatUint(switched.JobID, 10)
		}
		code := spaceResult(ctx, data, []string{"SPACE ID", "DATA CREDENTIAL SYNC"}, [][]string{{id, switched.SyncStatus}}, output, diagnostics)
		if code != 0 {
			fmt.Fprintln(diagnostics, "Space credentials were saved; use tiana status to check the current space.")
		}
		return code
	})
	create := leaf("create", "Create a space without switching the current login", "NAME", 1, []cli.Flag{stringOption("request-id", "Original request identity when recovering a creation", "")}, func(ctx context.Context, cmd *cli.Command, c *authclient.Client, _ authclient.Credential) int {
		requestID := cmd.String("request-id")
		var err error
		if requestID == "" {
			requestID, err = authclient.NewIdempotencyKey()
		}
		if err != nil {
			return spaceFailure(ctx, err, false, nil, "Retry space create", output, diagnostics)
		}
		data := map[string]any{"request_id": requestID, "display_name": cmd.Args().First()}
		if _, err = fmt.Fprintln(diagnostics, "Space creation request ID:", safeDisplay(requestID)); err != nil {
			return 1
		}
		space, err := c.CreateSpace(ctx, cmd.Args().First(), requestID)
		if err != nil {
			return spaceFailure(ctx, err, true, data, "Recover with the original name and --request-id; then use space switch explicitly", output, diagnostics)
		}
		data["space"] = space
		return spaceResult(ctx, data, []string{"SPACE ID", "NAME", "REQUEST ID"}, [][]string{{space.TenantID, spaceName(space), requestID}}, output, diagnostics)
	})
	rename := leaf("rename", "Rename the current space", "NAME", 1, nil, func(ctx context.Context, cmd *cli.Command, c *authclient.Client, _ authclient.Credential) int {
		before, err := c.CurrentSpace(ctx)
		if err != nil {
			return spaceFailure(ctx, err, false, nil, "Retry space show", output, diagnostics)
		}
		space, err := c.RenameSpace(ctx, cmd.Args().First(), before.Revision)
		if err != nil {
			return spaceFailure(ctx, err, true, map[string]string{"tenant_id": before.TenantID}, "Inspect space show before another rename", output, diagnostics)
		}
		return spaceResult(ctx, space, []string{"SPACE ID", "NAME"}, [][]string{{space.TenantID, spaceName(space)}}, output, diagnostics)
	})
	leave := leaf("leave", "Leave a space; leaving the current space signs out", "SPACE_ID", 1, force(), func(ctx context.Context, cmd *cli.Command, c *authclient.Client, credential authclient.Credential) int {
		spaces, err := c.ListSpaces(ctx)
		if err != nil {
			return spaceFailure(ctx, err, false, nil, "Retry space list", output, diagnostics)
		}
		id := cmd.Args().First()
		var found *authclient.Space
		for i := range spaces {
			if spaces[i].TenantID == id {
				found = &spaces[i]
				break
			}
		}
		if found == nil {
			return spaceFailure(ctx, errors.New("space is not in your space list"), false, nil, "Check space list", output, diagnostics)
		}
		prompt := fmt.Sprintf("Leave space %q (%s)?", safeDisplay(spaceName(*found)), safeDisplay(id))
		if id == credential.User.TenantID {
			prompt += " This signs out the current login."
		}
		confirmed, code := confirmSpaceRemoval(ctx, cmd, prompt, input, output, diagnostics)
		if !confirmed {
			return code
		}
		loggedOut, err := c.LeaveSpace(ctx, id)
		data := map[string]any{"tenant_id": id, "logged_out": loggedOut}
		if err != nil {
			return spaceFailure(ctx, err, true, data, "Inspect space list or sign in again before retrying", output, diagnostics)
		}
		if loggedOut {
			if err = c.DeleteCredential(); err != nil {
				return spaceFailure(ctx, err, false, data, "You left the space, but local credential cleanup failed. Run tiana logout", output, diagnostics)
			}
		}
		return spaceResult(ctx, data, []string{"LEFT SPACE", "SIGNED OUT"}, [][]string{{id, strconv.FormatBool(loggedOut)}}, output, diagnostics)
	})
	memberList := leaf("list", "List members of the current space", "no arguments", 0, nil, func(ctx context.Context, cmd *cli.Command, c *authclient.Client, _ authclient.Credential) int {
		items, err := c.ListSpaceMembers(ctx)
		if err != nil {
			return spaceFailure(ctx, err, false, nil, "Check the current space and your management role", output, diagnostics)
		}
		rows := make([][]string, 0, len(items))
		for _, item := range items {
			rows = append(rows, spaceMemberRow(item))
		}
		return spaceResult(ctx, map[string]any{"items": items}, []string{"USER ID", "NAME", "ROLE", "REVISION"}, rows, output, diagnostics)
	})
	changeMember := func(admin bool) func(context.Context, *cli.Command, *authclient.Client, authclient.Credential) int {
		return func(ctx context.Context, cmd *cli.Command, c *authclient.Client, credential authclient.Credential) int {
			items, err := c.ListSpaceMembers(ctx)
			if err != nil {
				return spaceFailure(ctx, err, false, nil, "Check space member list", output, diagnostics)
			}
			id := cmd.Args().First()
			var found *authclient.SpaceMember
			for i := range items {
				if items[i].UserID == id {
					found = &items[i]
					break
				}
			}
			if found == nil {
				return spaceFailure(ctx, errors.New("user is not in the current space member list"), false, nil, "Check space member list", output, diagnostics)
			}
			if !admin {
				confirmed, code := confirmSpaceRemoval(ctx, cmd, fmt.Sprintf("Remove member %s from space %s?", safeDisplay(id), safeDisplay(credential.User.TenantID)), input, output, diagnostics)
				if !confirmed {
					return code
				}
			}
			member, err := c.ChangeSpaceMember(ctx, id, found.Revision, admin)
			if err != nil {
				return spaceFailure(ctx, err, true, map[string]string{"tenant_id": credential.User.TenantID, "user_id": id}, "Inspect space member list; membership may have changed before credential cleanup finished", output, diagnostics)
			}
			return spaceResult(ctx, member, []string{"USER ID", "NAME", "ROLE", "REVISION"}, [][]string{spaceMemberRow(member)}, output, diagnostics)
		}
	}
	invitationList := leaf("list", "List unaccepted invitations sent by the current space", "no arguments", 0, nil, func(ctx context.Context, cmd *cli.Command, c *authclient.Client, _ authclient.Credential) int {
		items, err := c.ListSpaceInvitations(ctx)
		if err != nil {
			return spaceFailure(ctx, err, false, nil, "Check the current space and your management role", output, diagnostics)
		}
		rows := make([][]string, 0, len(items))
		for _, item := range items {
			rows = append(rows, spaceInvitationRow(item))
		}
		return spaceResult(ctx, map[string]any{"items": items}, []string{"INVITATION ID", "USER ID", "STATE", "EXPIRES AT"}, rows, output, diagnostics)
	})
	invite := leaf("invite", "Invite users, resending any existing unaccepted invitation", "USER_ID...", -1, nil, func(ctx context.Context, cmd *cli.Command, c *authclient.Client, credential authclient.Credential) int {
		return inviteSpaceUsers(ctx, c, credential, cmd.Args().Slice(), output, diagnostics)
	})
	preview := leaf("preview", "Inspect an invitation addressed to you", "INVITATION_ID", 1, nil, func(ctx context.Context, cmd *cli.Command, c *authclient.Client, _ authclient.Credential) int {
		invitation, err := c.PreviewSpaceInvitation(ctx, cmd.Args().First())
		if err != nil {
			return spaceFailure(ctx, err, false, nil, "Check the invitation ID and signed-in account", output, diagnostics)
		}
		return spaceResult(ctx, invitation, []string{"INVITATION ID", "SPACE ID", "SPACE", "STATE", "EXPIRES AT"}, [][]string{{invitation.ID, invitation.TenantID, invitation.TenantName, invitation.State, invitation.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")}}, output, diagnostics)
	})
	accept := leaf("accept", "Accept an invitation without switching the current space", "INVITATION_ID", 1, nil, func(ctx context.Context, cmd *cli.Command, c *authclient.Client, _ authclient.Credential) int {
		invitation, err := c.PreviewSpaceInvitation(ctx, cmd.Args().First())
		if err != nil {
			return spaceFailure(ctx, err, false, nil, "Check the invitation ID and signed-in account", output, diagnostics)
		}
		data := map[string]any{"invitation_id": invitation.ID, "tenant_id": invitation.TenantID}
		member, err := c.AcceptSpaceInvitation(ctx, invitation.ID)
		if err != nil {
			return spaceFailure(ctx, err, true, data, "Inspect the invitation preview and space list before retrying", output, diagnostics)
		}
		data["member"] = member
		return spaceResult(ctx, data, []string{"JOINED SPACE", "ROLE", "NEXT COMMAND"}, [][]string{{invitation.TenantID, member.Role, "tiana space switch " + invitation.TenantID}}, output, diagnostics)
	})
	return &cli.Command{Name: "space", Usage: "Manage spaces, members and invitations", Action: groupAction, Commands: []*cli.Command{
		list, show, switchSpace, create, rename, leave,
		{Name: "member", Usage: "Manage members of the current space", Action: groupAction, Commands: []*cli.Command{memberList, leaf("remove", "Remove a space member", "USER_ID", 1, force(), changeMember(false)), leaf("set-admin", "Promote a member to admin as the space owner", "USER_ID", 1, nil, changeMember(true))}},
		{Name: "invitation", Usage: "Send and accept space invitations", Action: groupAction, Commands: []*cli.Command{invitationList, invite, preview, accept}},
	}}
}

func spaceName(space authclient.Space) string {
	if space.DisplayName != "" {
		return space.DisplayName
	}
	if space.IsDefault {
		return "Default space"
	}
	return "Unnamed space"
}

func spaceMemberRow(member authclient.SpaceMember) []string {
	return []string{member.UserID, member.DisplayName, member.Role, strconv.FormatUint(member.Revision, 10)}
}

func spaceInvitationRow(invitation authclient.SpaceInvitation) []string {
	return []string{invitation.ID, invitation.UserID, invitation.State, invitation.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")}
}

func spaceResult(ctx context.Context, data any, headers []string, rows [][]string, output, diagnostics io.Writer) int {
	if managementJSON(ctx) {
		return writeAppResult(apppublish.Success(data), true, output, diagnostics)
	}
	return writeSpaceTable(headers, rows, output)
}

func writeSpaceTable(headers []string, rows [][]string, output io.Writer) int {
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, strings.Join(headers, "\t"))
	for _, row := range rows {
		values := make([]string, len(row))
		for i, value := range row {
			values[i] = safeDisplay(value)
		}
		fmt.Fprintln(table, strings.Join(values, "\t"))
	}
	if err := table.Flush(); err != nil {
		return 1
	}
	return 0
}

func spaceCommandError(ctx context.Context, err error, write bool, next string) *apppublish.Error {
	result := &apppublish.Error{Code: "SPACE_REQUEST_FAILED", Message: "Space command did not complete", NextAction: next, ExitCode: 1}
	var api *authclient.APIError
	switch {
	case errors.Is(err, authclient.ErrAuthenticationRequired), errors.Is(err, authclient.ErrCredentialNotFound):
		result.Code, result.Message = "AUTH_REQUIRED", "Account authentication is required"
	case errors.As(err, &api):
		result.Code = api.Code
		result.HTTPStatus = api.Status
		result.RequestID = api.RequestID
		result.Message = "MGR rejected the space request: " + safeDisplay(api.Code)
		if write && (api.Status >= 500 || api.Code == "COMMIT_STATUS_UNKNOWN" || api.Code == "RESULT_UNKNOWN") {
			result.ExitCode = 4
		}
	default:
		if write {
			result.ExitCode = 4
		}
	}
	if result.ExitCode == 4 {
		result.Code, result.Message = "SPACE_OUTCOME_UNKNOWN", "Space operation outcome is unknown"
	}
	if ctx.Err() != nil {
		result.ExitCode = 130
	}
	return result
}

func spaceFailure(ctx context.Context, err error, write bool, data any, next string, output, diagnostics io.Writer) int {
	writeCommandError(diagnostics, err)
	failure := spaceCommandError(ctx, err, write, next)
	result := apppublish.Failure(failure)
	result.Data = data
	return writeAppResult(result, managementJSON(ctx), output, diagnostics)
}

func confirmSpaceRemoval(ctx context.Context, cmd *cli.Command, prompt string, input io.Reader, output, diagnostics io.Writer) (bool, int) {
	if cmd.Bool("force") {
		return true, 0
	}
	if managementJSON(ctx) || !isTerminal(input) {
		return false, writeAppResult(apppublish.Failure(&apppublish.Error{Code: "CONFIRMATION_REQUIRED", Message: "Removal requires terminal confirmation or --force", NextAction: "Use --force only after confirming the target", ExitCode: 2}), managementJSON(ctx), output, diagnostics)
	}
	if _, err := fmt.Fprint(diagnostics, prompt+" [y/N] "); err != nil {
		return false, 1
	}
	accepted, err := readDeleteConfirmation(ctx, input.(*os.File))
	if err != nil {
		if ctx.Err() != nil {
			return false, 130
		}
		writeCommandError(diagnostics, err)
		return false, 1
	}
	if !accepted {
		fmt.Fprintln(diagnostics, "Operation cancelled.")
	}
	return accepted, 0
}
