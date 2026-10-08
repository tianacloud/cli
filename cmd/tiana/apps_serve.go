package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/tianacloud/cli/internal/appbootstrap"
	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/clientconfig"
	requestdiag "github.com/tianacloud/cli/internal/diagnostics"
	"github.com/urfave/cli/v3"
)

func newWebServeCommand(output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "serve", Usage: "Preview a CSR/hash application through the fixed Tiana Bootstrap", Flags: []cli.Flag{stringOption("dir", "Built module and asset output", ""), &cli.IntFlag{Name: "port", Usage: "Loopback preview port", Value: 4174, Local: true}}, Action: func(ctx context.Context, cmd *cli.Command) error {
		port := cmd.Int("port")
		if cmd.NArg() != 1 || cmd.String("dir") == "" || port < 1 || port > 65535 {
			return argumentFailure(ctx, cmd, "serve requires a Web ID, --dir and a port between 1 and 65535")
		}
		managementOrigin, err := authclient.ResolveOrigin(ctx)
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana:", err)
			return statusError(1)
		}
		config := authclient.Config{Origin: managementOrigin}
		if trust := clientconfig.FromContext(ctx); trust != nil {
			config.RootCAs = trust.Roots
		}
		client, err := newAuthClient(ctx, diagnostics, true)
		if err != nil {
			writeCommandError(diagnostics, err)
			return statusError(1)
		}
		runner, e := (apppublish.Runner{Client: client}).Bind(ctx)
		if e != nil {
			return statusError(writeAppResult(apppublish.Failure(e), false, output, diagnostics))
		}
		project, e := runner.Resolve(ctx, cmd.Args().First())
		if e != nil {
			return statusError(writeAppResult(apppublish.Failure(e), false, output, diagnostics))
		}
		build, err := appbootstrap.LoadBuild(cmd.String("dir"), appbootstrap.Manifest{WebID: project.ID, Name: project.Name, ProjectRevision: project.ProjectRevision, Entry: project.Entry, DatabaseInstanceID: project.DatabaseInstanceID, GitInstanceID: project.GitInstanceID})
		if err != nil {
			writeCommandError(diagnostics, err)
			return statusError(1)
		}
		defer build.Close()
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		origin := "http://" + address
		base := "/web/" + build.Manifest.WebID
		handler, err := appbootstrap.NewServer(appbootstrap.Config{Origin: origin, BasePath: base, Build: build, StartLogin: appbootstrap.NewConsoleLogin(config, build.Manifest.DatabaseInstanceID, project.InstanceID)})
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana: cannot initialize preview")
			return statusError(1)
		}
		defer handler.Close()
		listener, err := net.Listen("tcp4", address)
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana: preview port unavailable; select another --port")
			return statusError(1)
		}
		defer listener.Close()
		previewURL := origin + base
		// A failed/missing saved login leaves the existing Console flow available.
		authCtx, cancelAuth := context.WithTimeout(ctx, 12*time.Second)
		accountConfig := config
		accountConfig.OnRequestID = func(id string) { requestdiag.Write(ctx, "mgr", id) }
		identity, authErr := appbootstrap.NewAccountIdentity(authCtx, accountConfig, build.Manifest.DatabaseInstanceID, project.InstanceID)
		cancelAuth()
		if authErr == nil {
			previewURL, err = handler.AuthorizeLocalAccount(identity)
			if err != nil {
				return err
			}
		}
		server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
		stopped := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				server.Close()
			case <-stopped:
			}
		}()
		defer close(stopped)
		message := "CLI account unavailable for this database. Sign in using the Console button. This build has not been published."
		if authErr == nil {
			message = "This one-use link authorizes trusted app code with your CLI account; expires in 5 minutes. This build has not been published."
		}
		if _, err := fmt.Fprintf(output, "Local preview: %s\n%s\n", previewURL, message); err != nil {
			writeCommandError(diagnostics, err)
			return statusError(1)
		}
		err = server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(diagnostics, "tiana: preview server stopped unexpectedly")
			return statusError(1)
		}
		return nil
	}}
}
