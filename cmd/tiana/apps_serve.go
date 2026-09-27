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
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/urfave/cli/v3"
)

func newWebServeCommand(output, diagnostics io.Writer) *cli.Command {
	return &cli.Command{Name: "serve", Usage: "Preview a CSR/hash application through the fixed Tiana Bootstrap", Flags: []cli.Flag{stringOption("dir", "Built output containing tiana.app.json", ""), &cli.IntFlag{Name: "port", Usage: "Loopback preview port", Value: 4174, Local: true}}, Action: func(ctx context.Context, cmd *cli.Command) error {
		port := cmd.Int("port")
		if cmd.NArg() != 0 || cmd.String("dir") == "" || port < 1 || port > 65535 {
			return argumentFailure(ctx, cmd, "serve requires --dir and a port between 1 and 65535")
		}
		build, err := appbootstrap.LoadBuild(cmd.String("dir"))
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana:", err)
			return statusError(1)
		}
		defer build.Close()
		managementOrigin, err := authclient.ResolveOrigin(ctx)
		if err != nil {
			fmt.Fprintln(diagnostics, "tiana:", err)
			return statusError(1)
		}
		config := authclient.Config{Origin: managementOrigin}
		if trust := clientconfig.FromContext(ctx); trust != nil {
			config.RootCAs = trust.Roots
		}
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		origin := "http://" + address
		base := "/web/" + build.Manifest.AppID + "/"
		handler, err := appbootstrap.NewServer(appbootstrap.Config{Origin: origin, BasePath: base, Build: build, StartLogin: appbootstrap.NewConsoleLogin(config, build.Manifest.DatabaseInstanceID)})
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
		identity, authErr := appbootstrap.NewAccountIdentity(authCtx, config, build.Manifest.DatabaseInstanceID)
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
		fmt.Fprintf(output, "Local preview: %s\n", previewURL)
		if authErr == nil {
			fmt.Fprintln(output, "This one-use link authorizes trusted app code with your CLI account; expires in 5 minutes. This build has not been published.")
		} else {
			fmt.Fprintln(output, "CLI account unavailable for this database. Sign in using the Console button. This build has not been published.")
		}
		err = server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(diagnostics, "tiana: preview server stopped unexpectedly")
			return statusError(1)
		}
		return nil
	}}
}
