package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/tianacloud/cli/internal/apppublish"
	"github.com/tianacloud/cli/internal/authclient"
	"github.com/urfave/cli/v3"
)

func newSecretsCommand(input io.Reader, output, diagnostics io.Writer) *cli.Command {
	commands := []*cli.Command{}
	for _, name := range []string{"init", "list", "set", "remove"} {
		name := name
		args := ""
		flags := []cli.Flag{boolOption("json", "Write a structured JSON result")}
		switch name {
		case "list":
			flags = append(flags, stringOption("keyword", "Search key names containing this text", ""), stringOption("after", "Next-page cursor", ""), &cli.IntFlag{Name: "page-size", Value: 50, Usage: "Number of keys per page"})
		case "set":
			args = "KEY"
			flags = append(flags, stringOption("file", "Read the exact UTF-8 value from a file", ""), boolOption("stdin", "Read the exact UTF-8 value from standard input"))
		case "remove":
			args = "KEY"
			flags = append(flags, &cli.BoolFlag{Name: "force", Aliases: []string{"f"}, Usage: "Skip removal confirmation"})
		}
		commands = append(commands, &cli.Command{Name: name, ArgsUsage: args, Usage: map[string]string{"init": "Initialize the user secret store or return its existing state", "list": "List one page of key names and current versions", "set": "Write a secret without printing its value", "remove": "Remove a secret"}[name], Flags: flags, Action: func(ctx context.Context, cmd *cli.Command) error {
			count := 0
			if name == "set" || name == "remove" {
				count = 1
			}
			if cmd.NArg() != count || (count == 1 && cmd.Args().First() == "") {
				return argumentFailure(ctx, cmd, "provide the displayed secrets arguments")
			}
			if name == "list" && cmd.Int("page-size") < 1 {
				return argumentFailure(ctx, cmd, "page-size must be positive")
			}
			if name == "set" && (cmd.IsSet("file") == cmd.Bool("stdin")) {
				return argumentFailure(ctx, cmd, "choose exactly one of --file or --stdin")
			}
			return statusError(runSecrets(ctx, cmd, input, output, diagnostics))
		}})
	}
	return &cli.Command{Name: "secrets", Usage: "Manage the user secret store", Action: groupAction, Commands: commands}
}

type secretMetadata struct {
	Key     string `json:"key"`
	Version string `json:"version"`
	Changed bool   `json:"changed"`
}

func runSecrets(ctx context.Context, cmd *cli.Command, input io.Reader, output, diagnostics io.Writer) int {
	fail := func(err error) int {
		if cmd.Bool("json") {
			return managementFailure(err, output, diagnostics, nil)
		}
		writeCommandError(diagnostics, err)
		return 1
	}
	name := cmd.Name
	if name == "remove" && !cmd.Bool("force") && (!isTerminal(input) || cmd.Bool("json")) {
		return fail(errors.New("removal requires terminal confirmation; use --force"))
	}
	var value []byte
	if name == "set" {
		source := input
		if cmd.IsSet("file") {
			f, err := os.Open(cmd.String("file"))
			if err != nil {
				return fail(errors.New("cannot open secret input file"))
			}
			defer f.Close()
			st, err := f.Stat()
			if err != nil || !st.Mode().IsRegular() {
				return fail(errors.New("secret input must be a regular file"))
			}
			source = f
		}
		var err error
		value, err = io.ReadAll(io.LimitReader(source, (8<<20)+1))
		defer clear(value)
		if err != nil || len(value) > 8<<20 || !utf8.Valid(value) {
			return fail(errors.New("secret input must be UTF-8 and fit the 8 MiB request budget"))
		}
		encoded, _ := json.Marshal(map[string]string{"value": string(value)})
		fits := len(encoded) <= 8<<20
		clear(encoded)
		if !fits {
			return fail(errors.New("encoded secret exceeds the 8 MiB request budget"))
		}
	}
	account, err := newAuthClient(ctx, diagnostics, true)
	if err != nil {
		return fail(err)
	}
	credential, err := account.EnsureCredential(ctx)
	if err != nil {
		return fail(err)
	}
	if name == "remove" && !cmd.Bool("force") {
		fmt.Fprintf(diagnostics, "Remove secret %s? [y/N] ", safeDisplay(cmd.Args().First()))
		line, err := bufio.NewReader(input).ReadString('\n')
		if err != nil {
			return fail(errors.New("cannot read removal confirmation"))
		}
		line = strings.ToLower(strings.TrimSpace(line))
		if line != "y" && line != "yes" {
			return 0
		}
	}
	request := func(method, path string, body, result any) error {
		return account.RequestSecretsJSON(ctx, method, path, body, credential, result)
	}
	result := apppublish.Result{}
	switch name {
	case "init":
		var receipt struct {
			InstanceID string `json:"instance_id"`
			JobID      string `json:"job_id"`
			State      string `json:"state"`
			Created    bool   `json:"created"`
		}
		err = request(http.MethodPost, "/api/v1/secrets-store/init", struct{}{}, &receipt)
		result = apppublish.Result{Status: "accepted", Data: receipt}
		if receipt.State == "ACTIVE" {
			result.Status = "succeeded"
		}
	case "list":
		q := url.Values{"keyword": {cmd.String("keyword")}, "after": {cmd.String("after")}, "page_size": {fmt.Sprint(cmd.Int("page-size"))}}
		var page struct {
			Items []struct {
				Key     string `json:"key"`
				Version string `json:"version"`
			} `json:"items"`
			NextCursor *string `json:"next_cursor"`
		}
		err = request(http.MethodGet, "/api/v1/secrets-store/secrets?"+q.Encode(), nil, &page)
		result = apppublish.Success(page)
	case "set":
		var metadata secretMetadata
		err = request(http.MethodPut, "/api/v1/secrets-store/secrets/"+url.PathEscape(cmd.Args().First()), map[string]string{"value": string(value)}, &metadata)
		result = apppublish.Success(metadata)
	case "remove":
		err = request(http.MethodDelete, "/api/v1/secrets-store/secrets/"+url.PathEscape(cmd.Args().First()), nil, nil)
		result = apppublish.Success(map[string]any{"key": cmd.Args().First(), "removed": true})
	}
	if err != nil {
		var api *authclient.APIError
		if name != "list" && (!errors.As(err, &api) || api.Code == "RESULT_UNKNOWN") {
			return writeAppResult(apppublish.Failure(&apppublish.Error{Code: "RESULT_UNKNOWN", Message: "Operation outcome is unknown", NextAction: "Inspect the store state or list key metadata before another write", ExitCode: 4}), cmd.Bool("json"), output, diagnostics)
		}
		message := "secret operation did not complete"
		if api != nil {
			switch api.Code {
			case "SECRETS_NOT_INITIALIZED":
				message = "initialize the secret store with tiana secrets init"
			case "SECRETS_NOT_READY":
				message = "secret store is not ready; inspect its existing creation task"
			case "INVALID_KEY":
				message = "secret key is empty or contains a period"
			}
		}
		return fail(errors.New(message))
	}
	return writeAppResult(result, cmd.Bool("json"), output, diagnostics)
}
