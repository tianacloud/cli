# Tiana CLI

Use `tiana` to sign in, manage SQLite and Git instances, run SQL, and connect
native clients to Tiana Endpoints.

Root help groups `web`, `sqlite` and `git` under `Products`. Use `tiana web`
for application listing, deletion, preview, creation, publication and status. Deployment
selection uses `TIANA_API_ORIGIN` when set, otherwise `https://console.tianacloud.com`.
The former command and origin-variable names are not compatibility aliases.

The `web` commands use the Web project `id` returned by `/api/v1/web-projects`. Hosted Web
URLs are `/web/<id>/` and Console details are `/console/web/<id>`.
Hosted URLs have a trailing slash and serve the sole current content. There is
no Web product version selector or versioned URL. Existing Web IDs retain their values.

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
npm install -g @tianacloud/cli@latest --include=optional
tiana version
```

npm installs the matching native executable from an exact-version platform package.
Targets are macOS x64/arm64, Linux x64/arm64/riscv64 and Windows x64/arm64.
The main package provides `tiana` and `git-remote-tiana` launchers; installation
with `--ignore-scripts` also works. Include optional dependencies when installing.

To check updates for the CLI and the currently loaded Skills:

```sh
tiana version check --skills-version VERSION --json
```

Checks share local state across Agents and accounts. Each package is checked at
most once per 24 hours, including failed attempts; automatic reminders share one
24-hour allowance. Ordinary terminal commands use cached results and refresh in
a short background process. JSON and native protocol calls remain quiet. Confirm
before upgrading; `npm install -g @tianacloud/cli@latest --include=optional`
updates the CLI, and the Skills installer updates the chosen Agent directory.

To start from a current Web template:

```sh
tiana web list-template --json
tiana web init-template ledger --dir ./my-ledger --json
```

Read `metadata.json` and the generated README, replace every declared placeholder,
initialize SQLite from the optional schema/seeds, then build and publish. Listing
reads the current catalog each time; initializing creates the new directory.

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

tiana version
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

The default is online. Install the CLI, then sign in:

```sh
tiana login
tiana status
```

For staging, set the origin in the process environment:

```sh
export TIANA_API_ORIGIN=https://console.tianacloud-staging.net
tiana login
tiana status
```

The CLI and native Git helper inherit the same management origin. When a user
selects an origin in an Agent task, pass it to every CLI and Git subprocess;
separate shell calls do not retain an earlier export. See [staging CA setup](docs/staging-ca.md)
for VPN and browser requirements. Example addresses below are placeholders.
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

Pending authorization exits 3 and returns `data.pending_auth=true` and `data.verification_uri`. Show that link
to the user; a successful resume persists the account session and returns
`data.logged_in=true`. An unfinished exchange is never automatically replayed.
`logout` also removes this origin's unfinished login state. Missing pending authorization returns `NO_PENDING_AUTH` (exit 1) with `pending_auth=false`; it does not establish that the account is signed out. Unverified login/pending states are omitted. Local state failures return `LOCAL_STATE_UNAVAILABLE` with safe path/cause and an action; expired and declined authorization return `AUTH_EXPIRED` / `AUTH_DENIED`. Do not repeat start for a filesystem error. Use `status --json` to verify the account independently.

For a deployment with a private CA:

```sh
export TIANA_CA_FILE=/path/to/root-ca.pem
tiana login

# Override the CA for a single command:
tiana --ca-file /path/to/root-ca.pem sqlite list
```

Management origins use HTTPS. Explicit environment configuration also permits
HTTP on loopback for local development.
TLS verifies certificates and hostnames. `--ca-file` takes precedence over
`TIANA_CA_FILE`. Without an override, online uses system roots; selecting the
staging Console adds `internal/clientconfig/staging-root.crt` to system trust.
The same invocation trust reaches management, Git, SQLite, Web and native helpers.
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
Both products accept `create NAME --json`: stdout contains one result with
`status`, `data.instance_id`, optional `data.job_id` / `data.operation_id`, and
`error`. Status is `accepted` by default, or `succeeded` after `--wait` confirms
creation. Progress and diagnostics go to stderr. Changing `--json` or `--wait`
does not change a pending creation's request identity.
Other instance commands accept an instance ID or an exact name; use the ID when
names are ambiguous. An `ep-...` Endpoint ID is not an instance ID.

`list` automatically fetches all pages and displays every instance of that product,
including in a terminal, without prompting.
The `MESSAGE` column displays the instance description; no engine column is shown.
`web list` also displays all applications without prompting; `--json` retains its
structured output. `show --url` prints only the connection URL.
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

`branch list` automatically reads all matching pages and prints one complete table.
`--after` optionally selects the starting cursor; subsequent pages are read automatically.
Its `MESSAGE` column displays the branch description.
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
when set. Command recovery, publication receipts and update state share this root. Login pending files remain beside credentials. Credential readers share this default path; individual credential-file
overrides are not supported. On Linux/macOS, credential files require mode 0600
and their directory mode 0700.
On Windows, Tiana creates private files and directories with an owner-only ACL.

When `TIANA_API_ORIGIN` is unset, the CLI always selects online, including when
only staging credentials are saved. Credentials and unfinished login state are
selected by origin. Pending instance-creation recovery files and their locks are
also scoped by origin, so an unfinished staging command does not block online.
An explicitly empty origin variable is an error.

| Environment variable | Purpose |
| --- | --- |
| `TIANA_API_ORIGIN` | Management origin for login, resource commands and account selection. |
| `TIANA_CA_FILE` | Deployment CA PEM; overridden by global `--ca-file`. |
| `TIANA_TOKEN` | Explicit connection credential; mutually exclusive with `TIANA_TOKEN_FILE`. |
| `TIANA_TOKEN_FILE` | Path to an explicit connection credential file. |
| `TIANA_PENDING_COMMAND_FILE` | Override the instance-creation recovery file path. |
| `XDG_CONFIG_HOME` | Base directory for account configuration. |

## Structured management output

SQLite/Git `list`, `show`, `delete`, SQLite `branch list/create/delete`, and top-level `status/logout/version` accept `--json`. These results use `status/data/error`; stdout contains one result and diagnostics go to stderr. JSON management never opens browser login. All matching list pages are returned in `data.items`, including an empty array. Instance detail includes the selected `branch` when applicable. `--url` and `--json` are mutually exclusive.

New instance detail/list `current_job_id`, revision, quota quantities and millisecond timestamps preserve decimal-string precision. Existing create JSON keeps its original `job_id` number contract; use a lossless JSON parser for it. Default mutation output means `accepted`, waiting success means `succeeded`. A confirmed operation failure is `failed`; uncertain mutation/observation returns `unknown` and retains immutable targets and receipt IDs. Unknown outcomes exit 4; cancellation exits 130 and does not cancel server work. JSON deletion requires explicit `--force`.

`status --json` returns verified `logged_in`, `user` and `quota`; a quota failure retains the verified account with `quota:null`. `sqlite shell --json` is a non-interactive alias for `--format json`, preserving the SQL result contract; it conflicts with other formats. Native `connect` and Git remote-helper retain their protocol streams.

`web serve --json` emits one startup result with `port`, `preview_url`, `launch_url` and `authorization`. The CLI-authorized launch URL is a short-lived capability and must not be written to ordinary logs or source; Console fallback has `launch_url:null`. Running logs stay on stderr.

When upgrading directory defaults on macOS/Windows, an unresolved command in the previous platform directory raises `LEGACY_PENDING_COMMAND` before a new mutation. Stop older CLI processes, set `TIANA_PENDING_COMMAND_FILE` to that original path and recover the original command with the same account and arguments. Unset the override after recovery. No automatic move/deletion occurs; previous publication receipts remain available in the old directory. Different old/new records must be investigated, not overwritten. Coordinate upgrades because older writers do not lock the new path.

## Troubleshooting

| Symptom | What to do |
| --- | --- |
| Explicitly empty management origin | Unset `TIANA_API_ORIGIN` for online, or set the intended origin for every command. |
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

`tiana web list` displays each application's hosted `ENDPOINT` and description
in `MESSAGE`. Web descriptions share the instance notes limit of 2048 UTF-8 bytes;
`--json` includes
`data.items[].endpoint`. Successful publication includes `endpoint` in its result
(`data.endpoint` with `--json`). The address comes from MGR's configured Site
origin and `/web/<id>/`; upload confirmation does not mean activation has finished.

Web publication uses `tiana web publish ID --dir DIST [--json]` and `tiana web status ID [--publish-id ID]`. It packages one `.tweb` with empty root `web.yaml` and `data/`, then uploads via Gateway business control using `TIANA_TOKEN` or `TIANA_TOKEN_FILE` (mutually exclusive), otherwise the selected account access token. Explicit credentials are authoritative, and invalid or rejected input never falls back to another token. Endpoint, Instance, Group and Tenant scopes are all supported when they cover the target; no local instance-token cache is read. The logged-in account still resolves owner-scoped Web project metadata through MGR. MGR manages Web/instance identity, not publication. S3 upload confirmation precedes activation; retain publish ID and checksum and query status on unknown results. No automatic PUT replay or versions. Plain files are supported. `web create NAME --entry assets/app.js` sets the immutable Site entry; optional `--database-instance-id` and `--git-instance-id` fix the project associations in MGR at creation. There is no styles flag or tiana.app.json. Entry JS loads its CSS. Publish validates data/<entry> before uploading. `web serve ID --dir DIST` previews using saved project parameters. Entry and associations are immutable; source-commit and web update are retired. Web list uses generic instance pagination with engine=web; MESSAGE projects notes, and names/notes are edited through the instance PATCH API with If-Match. Deploy the matching MGR schema 34 and API changes together. The Go SDK dependency is pinned to its exact published revision in go.mod; standalone builds do not require a local workspace.

Web application assets require an authenticated session for their owning tenant;
unauthorized reads return 404. Uploads use private OSS ACL and no object tags.
See [application delivery](docs/application-delivery.md) for the publish workflow.

A failed or unknown publication keeps the private local `.tweb` and reports its
`archive_path`; the durable receipt records the same path and checksum. A confirmed
S3 upload removes the temporary archive. After resolving an uncertain operation,
remove its retained local archive explicitly; no command replays it automatically.

## Web 模板初始化

每次创建应用时查询当前目录，再按返回的 name 初始化：

```sh
tiana web list-template --json
tiana web init-template ledger --dir ./my-ledger --json
```

CLI 从当前登录环境的 MGR 取得 15 分钟下载签名，校验 ZIP 的大小与 SHA-256，原样解压到指定的新目录。读取根部 metadata.json 和 README 后，Agent 按声明的文件替换变量、初始化 SQLite，再完成 Git、构建和 Web 发布。CLI 按模板名称调用，不需要模板版本参数。

四个 Skills 与 CLI 使用统一更新记录。主动检查可执行：

```sh
tiana version check --skills-version <当前加载技能的版本> --json
```

普通终端命令使用缓存与后台检查，两次提醒至少间隔 24 小时。JSON、非交互调用和 Git 协议输出保持机器可读；检查失败不阻断业务命令，升级需用户确认。分发与维护流程见 [CLI release](docs/cli-release.md)。


Web status JSON adds `query_kind: current / receipt` without changing existing `state`. Current `state=running` describes the application; receipt `REMOTE_COMMITTED` / `SUCCEEDED` describes upload. Confirm serving only when running and both current checksums match the saved target SHA256. Receipt expiry remains unknown; target content may still be confirmed by current checksums. `publish_id:null` in current status is valid and is not reconstructed from identical content hashes.
