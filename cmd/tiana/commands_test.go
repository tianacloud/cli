package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
)

func TestCommandTreeHelpAndRemovedDatabase(t *testing.T) {
	for _, path := range []string{"", "login", "logout", "status", "version", "sqlite", "sqlite create", "sqlite list", "sqlite show", "sqlite branch", "sqlite branch list", "sqlite branch create", "sqlite branch delete", "sqlite tokens", "sqlite tokens create", "sqlite shell", "connect", "git", "git remote-helper"} {
		t.Run(path, func(t *testing.T) {
			var out, diag bytes.Buffer
			args := append(strings.Fields(path), "--help")
			code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag)
			if code != 0 || out.Len() == 0 || diag.Len() != 0 {
				t.Fatalf("code=%d out=%s diag=%s", code, &out, &diag)
			}
		})
	}
	for _, args := range [][]string{{"db"}, {"db", "--help"}, {"db", "create", "private-name"}, {"help", "db"}} {
		var out, diag bytes.Buffer
		if code := runCLI(context.Background(), args, strings.NewReader(""), &out, &diag); code != 2 || out.Len() != 0 {
			t.Fatalf("args=%v code=%d out=%s diag=%s", args, code, &out, &diag)
		}
	}
	root := newCLICommand(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, nil)
	for _, child := range root.Commands {
		for _, name := range append([]string{child.Name}, child.Aliases...) {
			if name == "db" {
				t.Fatal("db remains registered")
			}
		}
	}
}

func TestCommandParserDoesNotEchoSensitiveArguments(t *testing.T) {
	secret := "PRIVATE_TOKEN_OR_SQL"
	for _, args := range [][]string{
		{secret}, {"help", secret}, {"--" + secret}, {"login", secret},
		{"sqlite", secret}, {"sqlite", "shell", "id", "--execute", secret, "--timeout", secret},
		{"sqlite", "shell", "id", "--execute", secret, "--unknown=" + secret},
		{"sqlite", "shell", "id", "--execute", secret, "--execute", secret},
		{"sqlite", "shell", "id", "--execute", secret, "--token-env", secret, "--token-file", secret},
		{"sqlite", "shell", "id", "--execute", secret, "--format", secret},
		{"sqlite", "create", secret, "--engine", secret},
		{"sqlite", "shell", "id", "--execute", secret, "--output"},
		{"sqlite", "create", " " + secret + " "},
		{"sqlite", "show", " " + secret + " "},
	} {
		var out, diag bytes.Buffer
		called := false
		code := runCLIWithSQL(context.Background(), args, strings.NewReader(""), &out, &diag, func(context.Context, sqliteOptions) int { called = true; return 0 })
		if code != 2 || called || out.Len() != 0 || strings.Contains(diag.String(), secret) {
			t.Fatalf("code=%d called=%v out=%q diag=%q", code, called, &out, &diag)
		}
	}
}

func TestCommandSQLValuesAndExitCodes(t *testing.T) {
	for _, sql := range []string{"SELECT ' --help; ';", "--help", " SELECT 1;\n"} {
		for _, status := range []int{0, 1, 2, 3, 4, 5, 6, 130} {
			var out, diag bytes.Buffer
			calls := 0
			code := runCLIWithSQL(context.Background(), []string{"sqlite", "shell", "instance", "--execute", sql, "--format=json", "--timeout=12"}, strings.NewReader(""), &out, &diag, func(_ context.Context, o sqliteOptions) int {
				calls++
				if o.sql != sql || o.reference != "instance" || o.format != "json" || o.timeout != 12*time.Millisecond || o.command != "exec" {
					t.Fatalf("wrong parsed options: %+v", o)
				}
				return status
			})
			if code != status || calls != 1 || diag.Len() != 0 {
				t.Fatalf("status=%d code=%d calls=%d diag=%s", status, code, calls, &diag)
			}
		}
	}
}

func TestCommandNativeArgumentBoundary(t *testing.T) {
	for _, path := range [][]string{{"connect"}, {"git", "remote-helper"}} {
		var out, diag bytes.Buffer
		root := newCLICommand(strings.NewReader(""), &out, &diag, nil)
		cmd := root
		for _, name := range path {
			cmd = cmd.Command(name)
		}
		want := []string{"--token-env", "TOKEN", "--", "turso", "db", "shell", "  native argument  ", "--help", "--format=raw"}
		cmd.Action = func(_ context.Context, cmd *cli.Command) error {
			if !reflect.DeepEqual(cmd.Args().Slice(), want) {
				t.Fatalf("native argv changed: %#v", cmd.Args().Slice())
			}
			return nil
		}
		if err := root.Run(context.Background(), append(append([]string{"tiana"}, path...), want...)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCommandPreservesRawPendingArguments(t *testing.T) {
	for _, path := range [][]string{{"sqlite", "create"}, {"sqlite", "tokens", "create"}} {
		root := newCLICommand(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, nil)
		cmd := root
		for _, name := range path {
			cmd = cmd.Command(name)
		}
		want := []string{"  name with 'quotes'  "}
		cmd.Action = func(_ context.Context, cmd *cli.Command) error {
			if !reflect.DeepEqual(leafArguments(cmd), want) {
				t.Fatalf("pending argv changed: %#v", leafArguments(cmd))
			}
			return nil
		}
		if err := root.Run(context.Background(), append(append([]string{"tiana"}, path...), want...)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCommandVersionAliases(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		var out, diag bytes.Buffer
		if code := runCLI(context.Background(), []string{arg}, strings.NewReader(""), &out, &diag); code != 0 || !strings.HasPrefix(out.String(), fmt.Sprintf("tiana %s (helper-contract ", version)) || diag.Len() != 0 {
			t.Fatalf("code=%d out=%s diag=%s", code, &out, &diag)
		}
	}
}

func TestCommandPreservesExplicitWhitespaceReference(t *testing.T) {
	var out, diag bytes.Buffer
	calls := 0
	code := runCLIWithSQL(context.Background(), []string{"sqlite", "shell", "--execute=SELECT 1", "--", " spaced name "}, strings.NewReader(""), &out, &diag, func(_ context.Context, o sqliteOptions) int {
		calls++
		if o.reference != " spaced name " {
			t.Fatalf("reference changed: %q", o.reference)
		}
		return 0
	})
	if code != 0 || calls != 1 {
		t.Fatalf("code=%d calls=%d error=%s", code, calls, &diag)
	}
}

func TestCommandRejectsFrameworkTracing(t *testing.T) {
	if os.Getenv("TIANA_TEST_TRACING_CHILD") == "1" {
		os.Exit(runCLI(context.Background(), []string{"sqlite", "shell", "id", "--execute", "PRIVATE_SQL_SENTINEL"}, strings.NewReader(""), os.Stdout, os.Stderr))
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCommandRejectsFrameworkTracing$")
	cmd.Env = append(os.Environ(), "URFAVE_CLI_TRACING=on", "TIANA_TEST_TRACING_CHILD=1")
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), "unset URFAVE_CLI_TRACING") || strings.Contains(string(out), "PRIVATE_SQL_SENTINEL") || strings.Contains(string(out), "## URFAVE") {
		t.Fatalf("err=%v output=%s", err, out)
	}
}

func TestCommandHelpDoesNotEchoParsedValues(t *testing.T) {
	const secret = "PRIVATE_HELP_SENTINEL"
	var out, diag bytes.Buffer
	code := runCLI(context.Background(), []string{"sqlite", "shell", "instance", "--execute", secret, "--output", secret, "--help"}, strings.NewReader(""), &out, &diag)
	if code != 0 || out.Len() == 0 || diag.Len() != 0 || strings.Contains(out.String(), secret) {
		t.Fatalf("code=%d out=%s diag=%s", code, &out, &diag)
	}
}
