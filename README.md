# Tiana CLI

Use `tiana` to sign in, manage SQLite and Git instances, run SQL, and connect
native clients to Tiana Endpoints.

Root help groups `web`, `sqlite` and `git` under `Products`. Use `tiana web`
for application listing, deletion, preview, creation, upload and publication status. Deployment
selection uses `TIANA_API_ORIGIN`, a single saved account origin, or the built-in
deployment default, in that order.
The former command and origin-variable names are not compatibility aliases.

The `web` commands use the Web project `id` returned by `/api/v1/web-projects`. Hosted Web
URLs are `/web/<id>/` and Console details are `/console/web/<id>`.
Hosted URLs have a trailing slash. Existing Web IDs retain their values.

- [Build and install](#build-and-install)
- [Configure and sign in](#configure-and-sign-in)
- [Application delivery and npm packaging](docs/application-delivery.md)
- [Manage instances](#manage-instances)
- [Manage SQLite branches](#manage-sqlite-branches)
- [Run SQL](#run-sql)
- [Clone, fetch and push Git repositories](#clone-fetch-and-push-git-repositories)
- [Connect with Turso](#connect-with-turso)
- [Credentials and environment](#credentials-and-environment)
- [Troubleshooting](#troubleshooting)

## Build and install

Install with Node.js 20 or later:

```sh
npm install -g @tianacloud/cli
tiana --version
```

The installer downloads the matching archive from the public
[Gitee release repository](https://gitee.com/tianacloud/cli-releases/releases).
Targets are macOS amd64/arm64, Linux amd64/arm64/riscv64 and Windows amd64/arm64.
Starting with 0.2.2-beta.4, the Windows installer extracts ZIP files inside Node.js;
macOS/Linux use tar. Earlier npm packages retain their original installer.

For source builds, use Go 1.25 or later, Git, and access to the repository's Go
dependencies. A Go build includes the SQLite shell, Git transport and connect
helper. The following source-install commands are for macOS/Linux; use the npm
installer on Windows to install both command shims.

See [release instructions](docs/cli-release.md) for one-command publishing.

```sh
git clone https://github.com/tianacloud/cli.git
cd cli
GOWORK=off go build -mod=readonly -o bin/tiana ./cmd/tiana

mkdir -p "$HOME/.local/bin"
install -m 0755 bin/tiana "$HOME/.local/bin/tiana"
install -m 0755 scripts/git-remote-tiana "$HOME/.local/bin/git-remote-tiana"
export PATH="$HOME/.local/bin:$PATH"

tiana --version
tiana --help
```

Keep `git-remote-tiana` beside `tiana` so native Git can find the transport.
Add the PATH setting to your shell configuration to keep it across sessions.
The built-in SQLite shell needs no separate SQLite or Turso executable;
`connect -- turso ...` requires Turso to be installed separately.

If downloading the Tiana SDK dependencies requires GitHub authentication, first
configure GitHub SSH access, then build in a subshell:

```sh
(
  export GOWORK=off
  export GOPRIVATE=github.com/tianacloud/sdk-go,github.com/tianacloud/sdk-go-sqlite
  export GONOPROXY="$GOPRIVATE" GONOSUMDB="$GOPRIVATE"
  export GIT_CONFIG_COUNT=1
  export GIT_CONFIG_KEY_0='url.git@github.com:.insteadOf'
  export GIT_CONFIG_VALUE_0='https://github.com/'
  go mod download
  go mod verify
  go build -mod=readonly -o bin/tiana ./cmd/tiana
)
```

## Configure and sign in

Select the deployment in the environment before starting your shell or coding
agent. All `example.test` addresses below are placeholders: replace them with
your deployment's HTTPS management origin or connection URL.

```sh
export TIANA_API_ORIGIN=https://console.example.test
# Start your coding agent from this environment, or run CLI commands here:
tiana login
tiana status
tiana sqlite shell INSTANCE -e 'SELECT 1'
```

The CLI and native Git helper inherit the same management origin. Skills must
not inject or override it per command. If the environment is missing or
ambiguous, configure the launcher or shell and restart the agent as needed.
The CLI has no management configuration-file option. Connection parameters
after `connect --` belong to the native client and are passed through unchanged.

`login` prints a URL and waits for browser approval; you can open the URL on
another device when working over SSH. `status` shows available account details
and compute, storage and instance quota usage and limits, with a percentage and
20-cell progress bar:

```text
RESOURCE         USED        LIMIT       USAGE  PROGRESS
Compute          2500        10000       25.0%  [█████░░░░░░░░░░░░░░░]
Storage (bytes)  1000000000  2000000000   50.0%  [██████████░░░░░░░░░░]
Instances        2           3           66.7%  [█████████████░░░░░░░]
```

Percentages are shown to one decimal place. Over-quota usage keeps its actual
percentage while the bar stays full. Very small usage appears as `<0.1%`;
values just below/above the limit use `>99.9%`/`>100.0%` to avoid rounding away
that distinction. Missing usage appears as `unknown`. A zero limit remains `0`
with `n/a` percentage; unknown or undefined percentages show `-` for the bar.
If login or quota is unavailable, the command exits nonzero.

For a chat agent, start without blocking and resume after browser approval:

```sh
tiana login --start --no-open --json
tiana login --resume --json
```

Pending authorization exits 3 and returns `data.verification_uri`. Show that link
to the user; a successful resume persists the account session and returns
`data.logged_in=true`. An unfinished exchange is never automatically replayed.
`logout` also removes this origin's unfinished login state.

For a deployment with a private CA:

```sh
export TIANA_CA_FILE=/path/to/root-ca.pem
tiana login

# Override the CA for a single command:
tiana --ca-file /path/to/root-ca.pem sqlite list
```

Management origins in configuration files use HTTPS. Explicit environment
configuration also permits HTTP on loopback for local development.
TLS verifies certificates and hostnames. `--ca-file` takes precedence over
`TIANA_CA_FILE`; otherwise the embedded `internal/clientconfig/default.crt` is
added to system trust. The binary does not need a separate CA file at runtime.
Put global options before the subcommand, especially when using `connect`.

Sign out with:

```sh
tiana logout
```

## Manage instances

### Create, list and inspect

```sh
tiana sqlite create my-db -m "Application database" -w
tiana sqlite list
tiana sqlite show my-db
tiana sqlite show my-db --url

tiana git create my-repo --message "Application source repository" --wait
tiana git list
tiana git show my-repo
tiana git show my-repo --url
```

`create NAME` requires a nonempty name. Each product selects its own engine.
Use `-m TEXT` / `--message TEXT` to add an instance description. Quote text that
contains spaces; UTF-8 text up to 2048 bytes is accepted, including newlines.
The description is optional and can be combined with `-w` / `--wait`.
Other instance commands accept an instance ID or an exact name; use the ID when
names are ambiguous. An `ep-...` Endpoint ID is not an instance ID.

`list` displays that product's instances and pages interactively in a terminal.
Redirected output includes all pages. `show --url` prints only the connection URL.
Connection metadata must be available before this URL can be used.

### Wait for creation

Without `-w` / `--wait`, creation returns after the server accepts the request.
The output includes the instance and its job and/or operation ID.
With `--wait`, the CLI polls every second and returns success only after the
instance is active. Failure or a query error returns a nonzero exit status.
There is no overall waiting timeout. Ctrl-C exits 130 and stops local waiting;
it does not cancel server-side creation.

An interrupted or uncertain instance creation retains a local pending record.
Repeat the same command with the same name, description options, account and management origin to
continue it; `-w` and `--wait` are interchangeable. A saved receipt avoids a new
creation request. Once a command has completed and cleared its pending record,
running `create` again starts a new request. Do not delete an unresolved pending
record to bypass it.

While a mutating command is waiting, other mutations using the same local pending
file are blocked. Read-only `list` and `show` remain available.

### Delete

```sh
tiana sqlite delete my-db -w
tiana git delete my-repo --wait

# Non-interactive deletion: skip confirmation and wait for completion.
tiana sqlite delete INSTANCE_ID -f -w
tiana git delete INSTANCE_ID --force --wait
```

Deletion removes the entire instance, including its branches and data. By
default, the CLI prompts in a terminal; enter `y` or `yes` to confirm. Empty input,
EOF or a negative answer cancels without submitting deletion. Scripts must use
`-f` / `--force`; this skips confirmation but not authorization or target checks.

Without `--wait`, success means deletion was accepted. With it, the CLI polls the
accepted deletion operation every second until success or failure. Ctrl-C only
stops waiting. If the outcome is unknown, inspect the result before retrying with
the original instance ID. The CLI does not automatically repeat deletion.

`list` and `show` display `DELETING` while deletion is pending. `DELETED` entries
can remain visible while returned by the server. Deletion success does not promise
that independent storage reclamation has finished.

## Manage SQLite branches

```sh
tiana sqlite branch list my-db
tiana sqlite branch list my-db --search preview
tiana sqlite branch list my-db --after BRANCH_CURSOR

tiana sqlite branch create my-db preview -w
tiana sqlite branch create my-db preview-child --parent preview --wait
tiana sqlite branch create my-db review -m "Review environment" --ttl 86400 -w
tiana sqlite branch create my-db historical --timestamp 1790000000 --wait

tiana sqlite show my-db --branch preview --url
tiana sqlite shell my-db --branch preview

tiana sqlite branch delete my-db preview -w
tiana sqlite branch delete INSTANCE_ID BRANCH_ID --by-id -f --wait
```

`branch list` returns one page; use its `Next cursor` value with `--after`.
`--search` filters names by substring. Creation, deletion and `--branch` selection
use exact names.

Creation forks the default branch unless `--parent NAME` selects another source.
The default branch has the immutable ID `main`, even if its name changes.
The server assigns the new branch ID.

- `-m` / `--message TEXT` sets the branch description (UTF-8, at most 2048 bytes).
- `--ttl SECONDS` sets the lifetime after creation succeeds, from 1 to 2592000
  seconds (30 days). Omit it for no automatic expiry; zero is invalid.
- `-ts` / `--timestamp UNIX_SECONDS` forks the selected parent's data at that
  exact unsigned Unix second. Omit it for the latest data. An unavailable
  historical time fails creation; it never falls back to the latest data.

These options can be combined with `--parent` and `--wait`. Historical creation
requires retained history for the selected parent. Branch descriptions and historical
creation require matching server support; upgrade the server before using these
options, because older servers may ignore unsupported request fields.

Both `branch create` and `branch delete` return after acceptance by default.
Add `-w` / `--wait` to poll the original operation every second until success or
failure. Ctrl-C stops waiting without cancelling the operation. A missing
operation or failed query is an error, not proof of completion.

Branch creation has no local receipt-resume mechanism. After interruption or an
uncertain result, inspect the branch list and the reported operation before
issuing another create. For an uncertain deletion, retry only after inspection,
using the original instance and branch IDs with `--by-id`.

Branch deletion requires confirmation unless `-f` is set. The default branch and
protected branches cannot be deleted. Waiting for deletion does not wait for
independent storage reclamation tasks.

## Run SQL

### Interactive shell, statements and scripts

```sh
tiana sqlite shell my-db
tiana sqlite shell my-db --branch preview
tiana sqlite shell my-db -e 'SELECT 1' --format json
tiana sqlite shell my-db -f migration.sql
printf 'SELECT 1;\n' | tiana sqlite shell my-db
```

Instance mode requires account login to resolve instance and branch metadata.
Connection credentials are described [below](#credentials-and-environment).

Use `-e` / `--execute` for one non-transaction-control statement, or `-f` / `--file`
for a SQL script. These options are mutually exclusive. Without either, the shell
reads interactive input or piped stdin.

Interactive commands:

| Command | Action |
| --- | --- |
| `.help` | Show shell help. |
| `.tables` | List tables. |
| `.schema` | Show schema. |
| `.mode FORMAT` | Choose table, json, ndjson or csv output. |
| `.read PATH` | Read SQL from a literal path, without shell expansion. |
| `.quit` | Exit the shell. |

Terminate multiline SQL with a semicolon. Arrow keys edit and recall input;
history remains in memory for the current process. Ctrl-C while editing clears
pending input; Ctrl-D on an empty prompt exits.

### Output and timeouts

```sh
tiana sqlite shell my-db -e 'SELECT 1' --format csv
tiana sqlite shell my-db -f query.sql --format ndjson --output results.ndjson
tiana sqlite shell my-db -e 'SELECT 1' --timeout 60000
```

| Option | Meaning |
| --- | --- |
| `--format table\|json\|ndjson\|csv` | Output format; default: table. |
| `--output PATH` | Create a private result file; the path must not already exist. |
| `--timeout MS` | Per-request timeout; default: 30000, range: 1–3600000. |

Results go to stdout; prompts and diagnostics go to stderr. Table output is
limited to 1,000 rows. JSON preserves column order, duplicate column names and
typed values; integer and row-count values are strings. NDJSON emits `columns`,
`row` and `statement_end` records. A failed output file may contain partial results.
Always check the exit status.

### Connect directly to an Endpoint

```sh
tiana sqlite shell --endpoint https://ep-00000000000000000000000000.db.example.test:9443
tiana sqlite shell --endpoint ep-00000000000000000000000000.db.example.test \
  -e 'SELECT 1' --format json
```

Use the canonical Endpoint URL or hostname supplied by your deployment.
`--endpoint` accepts an HTTPS URL with an empty/root path, or a hostname with an
optional port; the default port is 443. It cannot be combined with an instance
argument or `--branch`, because the Endpoint already selects the branch.
IP addresses, aliases, bare Endpoint IDs, URL credentials, queries and fragments
are not accepted.

Direct mode skips MGR metadata lookup. It can use an explicit connection token
without account login; otherwise it uses the configured account access token.
The same SQL, format, output and timeout options apply.

### Transactions and failure handling

Use the interactive shell or a script for `BEGIN`, `COMMIT`, `ROLLBACK` and
savepoints. A session keeps statements on one connection. If it exits with a
known open transaction, the CLI attempts rollback and returns a nonzero status.

Scripts are checked before execution and stop at the first failure. Earlier
autocommitted statements can remain committed; scripts are not automatically
atomic. The parser supports a subset of SQLite syntax: for example, `VACUUM`,
`ATTACH`, `DETACH` and bracket-quoted identifiers are rejected. SQL input is
limited to 8 MiB and scripts to 10,000 statements.

In an interactive terminal, syntax and SQL errors return to the prompt. A failed
multi-statement input or `.read` stops at the first unrecovered error; earlier
statements may have committed. The prompt shows `tx=on`, `tx=off`, or `tx=?`
according to the last confirmed transaction state. Correcting an input error
keeps an existing transaction open.

If an idle session expires outside a transaction and the server confirms that
the current statement was not executed (`BATON_INVALID`), the shell prints:

```text
Connection lost. Reconnecting...
Connected. Session state has been reset (temporary tables and connection settings are not restored).
```

It opens a new session to the same endpoint and retries that statement once,
within the original command timeout. A new session does not restore temporary
tables, connection settings or transactions. No keepalive prevents idle expiry.

A lost transaction or an unknown result stops the current input and leaves the
shell open. The next SQL input opens a new session automatically. A lost response,
including a lost `COMMIT` response, can mean the write already committed: inspect
the database before rerunning it. Ctrl-C or a closed connection does not prove
remote cancellation or rollback. Failed reconnects also return to the prompt.

These recovery rules apply only to interactive terminals. `-e`, `-f`, pipes and
`--non-interactive` stop on errors with the exit codes below and do not reconnect.
Interactive errors already displayed do not make a later normal `.quit` fail;
active-transaction cleanup, terminal I/O failures and process cancellation retain
their exit behavior.

| SQL exit code | Meaning |
| --- | --- |
| 0 | Success. |
| 1 | Management initialization or instance resolution failure. |
| 2 | Invalid arguments or input. |
| 3 | Connection or authentication rejection. |
| 4 | SQL failure or rollback of an uncommitted transaction. |
| 5 | Outcome unknown. |
| 6 | Output failure. |
| 130 | Interrupted. |

## Clone, fetch and push Git repositories

After [installing the launcher](#build-and-install) and signing in:

```sh
tiana git create my-repo -w
tiana git show my-repo --url

git clone "$(tiana git show my-repo --url)" my-repo
cd my-repo
git fetch origin
git push origin HEAD
```

The connection URL has the form `tiana://ENDPOINT[:PORT]/repo.git`. Use the URL
from `show --url`; the default port is 443. Native Git invokes `git-remote-tiana`,
which must be installed beside `tiana` on PATH. There is no need to invoke
`tiana git remote-helper` manually.

Git uses the same connection credential selection as SQLite. Set `TIANA_CA_FILE`
when the deployment CA is not in the system trust store. An interrupted push can
have an unknown outcome; inspect the remote before retrying.

## Connect with Turso

With Turso installed, use `connect` to run its shell through Tiana:

```sh
tiana connect --allow-unisolated-loopback -- \
  turso db shell https://ep-00000000000000000000000000.db.example.test

tiana --ca-file /path/to/root-ca.pem connect --allow-unisolated-loopback -- \
  turso db shell https://ep-00000000000000000000000000.db.example.test:9443 'SELECT 1'
```

Put Tiana options before `--`, followed by `turso db shell HTTPS_ENDPOINT [SQL]`.
Use exactly one canonical Endpoint URL with no credentials, path (including a
trailing `/`), query or fragment. Turso Cloud database names and instance/location/proxy selectors
are not supported by this adapter.

The built-in helper uses a local loopback listener. `--allow-unisolated-loopback`
explicitly accepts that other local processes may reach that listener and is
required for non-interactive use. The native client's arguments and stdin remain
its own. Tiana does not prompt for a token or consume SQL stdin for authentication.
A transport error is not automatically replayed, and native exit status is preserved.

## Credentials and environment

Management commands use account login. SQLite shell, Git transport and `connect`
select their connection credential in this order:

1. `TIANA_TOKEN` or `TIANA_TOKEN_FILE`, if explicitly set. Set only one.
2. The unexpired access token in `credentials.json` for the configured management origin.
3. If neither provides a credential, fail and ask you to run `tiana login`.

Invalid or empty explicit credentials fail without falling back. Connection setup
does not refresh an expired account session or open a login prompt. Instance-mode
SQLite shell still requires account login for metadata lookup, even with an
explicit connection token.

To use account login for connections:

```sh
unset TIANA_TOKEN TIANA_TOKEN_FILE
export TIANA_API_ORIGIN=https://console.example.test
tiana login
```

To use an existing token file for connections:

```sh
unset TIANA_TOKEN
chmod 600 /path/to/connection-token
export TIANA_TOKEN_FILE=/path/to/connection-token
```

A token file must be an owned regular file, not a symlink, and contain at most
512 credential bytes with an optional trailing newline. Account credentials use
`~/.config/tiana/credentials.json`, or `$XDG_CONFIG_HOME/tiana/credentials.json`
when set. On Linux/macOS, credential files require mode 0600 and their directory mode 0700.
On Windows, Tiana creates private files and directories with an owner-only ACL.

When `TIANA_API_ORIGIN` is unset, the CLI reuses the single HTTPS origin
in that credential file. This works across terminal and
desktop-agent restarts without searching shell configuration. Multiple saved
origins require `TIANA_API_ORIGIN` in the launching environment; no account is chosen automatically.
With no saved account, the CLI uses `https://console.service.internal.tiana.com`.
An explicitly empty origin variable is an error.

| Environment variable | Purpose |
| --- | --- |
| `TIANA_API_ORIGIN` | Management origin for login, resource commands and account selection. |
| `TIANA_CA_FILE` | Deployment CA PEM; overridden by global `--ca-file`. |
| `TIANA_TOKEN` | Explicit connection credential; mutually exclusive with `TIANA_TOKEN_FILE`. |
| `TIANA_TOKEN_FILE` | Path to an explicit connection credential file. |
| `TIANA_CREDENTIALS_FILE` | Override the account credential file path. |
| `TIANA_PENDING_COMMAND_FILE` | Override the instance-creation recovery file path. |
| `XDG_CONFIG_HOME` | Base directory for account configuration. |

## Troubleshooting

| Symptom | What to do |
| --- | --- |
| Management origin missing | Set `TIANA_API_ORIGIN` in the launcher or shell before starting the agent. |
| No valid login or expired access token | Run `tiana login`; check the management origin and explicit token settings. |
| TLS handshake or certificate verification failed | Check the Endpoint hostname, port and deployment CA; use `--ca-file` or `TIANA_CA_FILE`. |
| Unsafe credential file | Check ownership, regular-file type and mode 0600; avoid symlinks. |
| Unfinished operation blocks a mutation | Resume the original instance create with the same arguments and account; do not discard an unknown-outcome record. |
| Another local mutation is running | Wait for it to finish or stop its local wait; use `list`/`show` to inspect state. |
| Instance name is ambiguous | Use the immutable instance ID from `list`. |
| Git cannot find the remote helper | Put executable `git-remote-tiana` beside `tiana` and add their directory to PATH. |
| CLI asks to unset tracing | Unset `URFAVE_CLI_TRACING` before running commands. |
| SQL outcome is unknown | Inspect the result before retrying, especially after writes or COMMIT. |

Get command-specific help:

```sh
tiana --help
tiana sqlite shell --help
tiana sqlite branch create --help
tiana git delete --help
tiana connect --help
```

## Development checks

```sh
GOWORK=off go test -mod=readonly ./...
GOWORK=off go vet -mod=readonly ./...
python3 scripts/check-public-source.py
```

See [SQLite validation](SQLITE_VALIDATION.md) for SQL test coverage and optional
integration checks.

### Request diagnostics

Set `TIANA_DIAGNOSTICS=1` to print request IDs for MGR requests, SQLite sessions
and native Git connections to stderr, including successful calls. SQL results
and Git protocol bytes stay on stdout. MGR and SQLite runtime errors include
their request identity without enabling this option. Failed commands also print
the request IDs collected during the command, including lookups that end in a
local “instance not found” message. Local errors before any network request have
no request ID. `tiana connect` prints each helper connection ID to stderr before
network I/O, so a cancelled or failed handshake still leaves a diagnostic ID.
