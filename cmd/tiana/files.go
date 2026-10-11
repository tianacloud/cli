package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/fileclient"
	"github.com/urfave/cli/v3"
)

func newFilesCommand(input io.Reader, output, diagnostics io.Writer) *cli.Command {
	commands := []*cli.Command{}
	for _, name := range []string{"list", "get", "get-url", "put", "delete"} {
		name := name
		flags := []cli.Flag{boolOption("json", "Write a structured JSON result")}
		args := "REMOTE"
		switch name {
		case "list":
			args = "[PATH]"
			flags = append(flags, stringOption("cursor", "Next-page cursor", ""))
		case "get":
			args = "REMOTE LOCAL"
		case "put":
			args = "LOCAL REMOTE"
			flags = append(flags, boolOption("overwrite", "Delete the existing file, then upload its replacement"))
		case "delete":
			flags = append(flags, &cli.BoolFlag{Name: "force", Aliases: []string{"f"}, Usage: "Skip deletion confirmation", Local: true})
		}
		commands = append(commands, &cli.Command{Name: name, Usage: map[string]string{"list": "List one directory page", "get": "Download a file", "get-url": "Print an authenticated file-origin URL", "put": "Upload a file directly to OSS", "delete": "Delete a file"}[name], ArgsUsage: args, Flags: flags, Action: func(ctx context.Context, cmd *cli.Command) error {
			count := 1
			if name == "get" || name == "put" {
				count = 2
			}
			if (name == "list" && cmd.NArg() > 1) || (name != "list" && cmd.NArg() != count) {
				return argumentFailure(ctx, cmd, "provide the displayed file arguments")
			}
			return statusError(runFiles(ctx, cmd, input, output, diagnostics))
		}})
	}
	return &cli.Command{Name: "files", Usage: "Manage files in the current tenant", Action: groupAction, Commands: commands}
}

func runFiles(ctx context.Context, cmd *cli.Command, input io.Reader, output, diagnostics io.Writer) int {
	fail := func(err error) int {
		if cmd.Bool("json") {
			return managementFailure(err, output, diagnostics, nil)
		}
		writeCommandError(diagnostics, err)
		return 1
	}
	name := cmd.Name
	remote := cmd.Args().First()
	if name == "put" {
		remote = cmd.Args().Get(1)
	}
	if err := fileclient.ValidatePath(remote, name == "list"); err != nil {
		return fail(err)
	}
	if name == "delete" && !cmd.Bool("force") && (!isTerminal(input) || cmd.Bool("json")) {
		fmt.Fprintln(diagnostics, "tiana: deletion requires terminal confirmation; use --force (-f)")
		return 2
	}
	auth, err := newAuthClient(ctx, diagnostics, true)
	if err != nil {
		return fail(err)
	}
	credential, err := auth.EnsureCredential(ctx)
	if err != nil {
		return fail(err)
	}
	if credential.User.ID == "" || credential.User.TenantID == "" {
		return fail(authclient.ErrAuthenticationRequired)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if trust := clientconfig.FromContext(ctx); trust != nil {
		transport.TLSClientConfig = &tls.Config{RootCAs: trust.Roots, MinVersion: tls.VersionTLS12}
	}
	defer transport.CloseIdleConnections()
	client := &fileclient.Client{HTTP: &http.Client{Transport: transport}, Request: func(call context.Context, method, path string, body, result any) error {
		call, cancel := context.WithTimeout(call, 30*time.Second)
		defer cancel()
		current, err := auth.EnsureCredential(call)
		if err != nil {
			return err
		}
		if current.User.ID != credential.User.ID || current.User.TenantID != credential.User.TenantID {
			return errors.New("file operation account changed")
		}
		return auth.RequestFileJSON(call, method, "/api/v1/files"+path, body, current, result)
	}}
	var result any
	switch name {
	case "list":
		page, err := client.List(ctx, remote, cmd.String("cursor"))
		if err != nil {
			return fail(err)
		}
		if cmd.Bool("json") {
			result = page
			break
		}
		for _, item := range page.Items {
			size := strconv.FormatInt(item.Size, 10)
			if item.Kind == "directory" {
				size = "-"
			}
			if _, err = fmt.Fprintf(output, "%s\t%s\t%s\n", safeDisplay(item.Path), size, safeDisplay(item.ModifiedAt)); err != nil {
				return fail(err)
			}
		}
		if page.Cursor != "" {
			if _, err = fmt.Fprintf(diagnostics, "Next page: --cursor %s\n", safeDisplay(page.Cursor)); err != nil {
				return fail(err)
			}
		}
		return 0
	case "get-url":
		address, err := client.GetURL(ctx, remote)
		if err != nil {
			return fail(err)
		}
		if cmd.Bool("json") {
			result = map[string]string{"url": address}
			break
		}
		if _, err = fmt.Fprintln(output, address); err != nil {
			return fail(err)
		}
		return 0
	case "get":
		local := cmd.Args().Get(1)
		destination, err := os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fail(errors.New("download destination must be a new writable file"))
		}
		success := false
		defer func() {
			destination.Close()
			if !success {
				_ = os.Remove(local)
			}
		}()
		response, err := client.Get(ctx, "GET", remote, "")
		if err != nil {
			return fail(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return fail(&fileclient.Error{Status: response.StatusCode, Code: "FILE_DOWNLOAD_FAILED"})
		}
		size, err := io.Copy(destination, response.Body)
		if err == nil {
			err = destination.Close()
		}
		if err != nil {
			return fail(errors.New("download incomplete"))
		}
		success = true
		result = map[string]any{"path": remote, "local_path": local, "size": size}
	case "put", "delete":
		return runFileMutation(ctx, cmd, client, auth, credential, remote, input, output, diagnostics, fail)
	}
	if cmd.Bool("json") {
		return writeAppResult(apppublish.Success(result), true, output, diagnostics)
	}
	if _, err = fmt.Fprintln(output, "Downloaded:", safeDisplay(remote)); err != nil {
		return fail(err)
	}
	return 0
}

func runFileMutation(ctx context.Context, cmd *cli.Command, client *fileclient.Client, auth *authclient.Client, credential authclient.Credential, remote string, input io.Reader, output, diagnostics io.Writer, fail func(error) int) int {
	store, err := newPendingStore()
	if err != nil {
		return fail(err)
	}
	release, err := store.Acquire(ctx)
	if err != nil {
		return fail(err)
	}
	defer release()
	pending, err := store.Load()
	existing := err == nil
	if err != nil && !errors.Is(err, authclient.ErrPendingNotFound) {
		return fail(err)
	}
	operation := "files." + cmd.Name
	var source *os.File
	identity := &authclient.PendingFile{RemotePath: remote, Overwrite: cmd.Bool("overwrite")}
	if cmd.Name == "put" {
		local, err := filepath.Abs(cmd.Args().First())
		if err != nil {
			return fail(err)
		}
		source, err = os.Open(local)
		if err != nil {
			return fail(errors.New("cannot open upload source"))
		}
		defer source.Close()
		info, err := source.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return fail(errors.New("upload source must be a regular file"))
		}
		hash := sha256.New()
		if _, err = io.Copy(hash, source); err != nil {
			return fail(errors.New("cannot read upload source"))
		}
		if _, err = source.Seek(0, io.SeekStart); err != nil {
			return fail(err)
		}
		identity.LocalPath = local
		identity.Size = info.Size()
		identity.SHA256 = hex.EncodeToString(hash.Sum(nil))
		identity.ContentType = mime.TypeByExtension(filepath.Ext(local))
		if identity.ContentType == "" {
			identity.ContentType = "application/octet-stream"
		}
	}
	if existing {
		f := pending.File
		if pending.Command != operation || pending.Origin != auth.Origin() || pending.UserID != credential.User.ID || pending.TenantID != credential.User.TenantID || f == nil || f.RemotePath != identity.RemotePath || f.LocalPath != identity.LocalPath || f.SHA256 != identity.SHA256 || f.Overwrite != identity.Overwrite {
			reportUnfinishedOperation(diagnostics, store.Path, pending)
			return 1
		}
	} else {
		if cmd.Name == "delete" && !cmd.Bool("force") {
			if _, err = fmt.Fprintf(diagnostics, "Delete file %q? [y/N] ", safeDisplay(remote)); err != nil {
				return fail(err)
			}
			yes, err := readDeleteConfirmation(ctx, input.(*os.File))
			if err != nil {
				return fail(err)
			}
			if !yes {
				fmt.Fprintln(diagnostics, "Deletion cancelled.")
				return 0
			}
		}
		key, err := authclient.NewIdempotencyKey()
		if err != nil {
			return fail(err)
		}
		pending = authclient.PendingCommand{Command: operation, Origin: auth.Origin(), UserID: credential.User.ID, TenantID: credential.User.TenantID, IdempotencyKey: key, CreatedAt: time.Now().UTC(), File: identity}
		if err = store.Save(pending); err != nil {
			return fail(err)
		}
	}
	f := pending.File
	if !f.Completed {
		if cmd.Name == "delete" {
			if err = client.Delete(ctx, remote, pending.IdempotencyKey); err != nil {
				return fail(err)
			}
		} else {
			var upload fileclient.Upload
			if f.UploadID != "" {
				upload, err = client.Status(ctx, f.UploadID)
				if err != nil {
					return fail(err)
				}
				if !upload.Completed {
					// A previous transfer may have succeeded before its response was lost.
					completed, completeErr := client.Complete(ctx, f.UploadID)
					if completeErr == nil {
						upload = completed
					} else {
						var api *authclient.APIError
						if !errors.As(completeErr, &api) || api.Status != 404 {
							return fail(completeErr)
						}
					}
				}
			} else {
				if existing && time.Since(pending.CreatedAt) >= 30*time.Minute {
					return fail(errors.New("upload preparation outcome is too old to replay; inspect the remote file and pending command"))
				}
				upload, err = client.Prepare(ctx, fileclient.Prepare{RequestID: pending.IdempotencyKey, Path: remote, Size: f.Size, ContentType: f.ContentType, Overwrite: f.Overwrite})
				if err != nil {
					var api *authclient.APIError
					if errors.As(err, &api) && api.Status >= 400 && api.Status < 500 && !f.Overwrite {
						if cleanup := store.Delete(); cleanup != nil {
							return fail(cleanup)
						}
					}
					return fail(err)
				}
				f.UploadID = upload.ID
				if err = store.Save(pending); err != nil {
					return fail(err)
				}
			}
			if !upload.Completed {
				if upload.Form == nil {
					return fail(errors.New("upload is no longer writable; inspect its status before starting another upload"))
				}
				if err = client.Upload(ctx, *upload.Form, remote, f.Size, source); err != nil {
					return fail(err)
				}
				upload, err = client.Complete(ctx, upload.ID)
				if err != nil {
					return fail(err)
				}
				if !upload.Completed {
					return fail(errors.New("file completion remains unconfirmed"))
				}
			}
		}
		f.Completed = true
		if err = store.Save(pending); err != nil {
			return fail(err)
		}
	}
	result := map[string]any{"path": remote, "completed": true}
	if f.UploadID != "" {
		result["upload_id"] = f.UploadID
	}
	if cmd.Bool("json") {
		if code := writeAppResult(apppublish.Success(result), true, output, diagnostics); code != 0 {
			return code
		}
	} else {
		if _, err = fmt.Fprintln(output, "Completed:", safeDisplay(remote)); err != nil {
			return fail(err)
		}
	}
	if err = store.Delete(); err != nil {
		return fail(err)
	}
	return 0
}
