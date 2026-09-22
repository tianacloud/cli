# Tiana CLI

The command tree and account/SQLite/Git management flags use `urfave/cli/v3` v3.11.0.
Run `tiana --help`, `tiana sqlite <command> --help`, or
`tiana git <command> --help` for generated usage.
Flags accept `--name=value` and may follow positional arguments; duplicate
options are rejected. Parser diagnostics do not echo user-provided values.
Unset `URFAVE_CLI_TRACING`: the CLI rejects this framework debug mode because
it would log raw SQL and potentially credentials.
The `connect` adapter retains its strict native-client argument boundary:
arguments after `--` are passed unchanged. Git remote-helper argv is also opaque.

## SDK dependencies

The CLI pins `github.com/tianacloud/sdk-go` and `sdk-go-sqlite` to the
published `v0.1.0-rc.1` candidates. Standalone builds use these immutable
versions directly, without a local workspace or replacement module.

Tokens are opaque credentials. Automatic selection asks MGR for candidate
Token IDs for the target and chooses a saved credential under the current
management origin and tenant. Explicit SQL and Git credentials are passed
through without this selection request. The SDK auth package retains its
credential-file checks, redacted diagnostics and coordinated refresh/writes.

## Build with Go modules

Use Go 1.25+; dependency versions and checksums are tracked in `go.mod` and
`go.sum`. There is no checked-in vendor tree or local `replace`. SQLite, Git
and `connect` source builds do not require Rust. On Linux and macOS the CLI
re-executes itself as a private Go helper. Optional legacy embedded-helper
packaging is described below.

The SDK dependency is now `github.com/tianacloud/sdk-go`. Account login,
refresh/logout and local credential stores are provided by its `auth` package;
CLI resource operations and deployment defaults remain here. Existing local
credential paths/JSON and TIANA_TOKEN priority are unchanged.

SDK dependencies are pinned to GitHub commit pseudo-versions in go.mod:

- sdk-go: `v0.0.0-20260921154600-3a503ed25585`
- sdk-go-sqlite: `v0.0.0-20260921133545-2636f0ee5047`

A standalone build downloads those published sources and verifies the recorded
go.sum hashes. The CLI selects the fixed sdk-go version for both its own
authentication and the SQLite SDK through Go module version selection. No
placeholder exclusion is needed.

These repositories currently require authenticated Git access. With GitHub SSH
access already configured, the following environment applies only to this shell:

```sh
export GOWORK=off
export GOPRIVATE=github.com/tianacloud/sdk-go,github.com/tianacloud/sdk-go-sqlite
export GONOPROXY="$GOPRIVATE" GONOSUMDB="$GOPRIVATE"
# Command-scoped HTTPS-to-SSH routing; does not alter global Git config.
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0='url.git@github.com:.insteadOf'
export GIT_CONFIG_VALUE_0='https://github.com/'
go mod download
go mod verify
CGO_ENABLED=0 go build -mod=readonly -o bin/tiana ./cmd/tiana
go test -mod=readonly ./...
go vet -mod=readonly ./...
```

Public dependencies keep the normal module proxy/checksum database verification.
The two authenticated modules retain their pinned go.sum hashes. Once repositories
are publicly downloadable, their private-access environment settings are unnecessary.

Cold builds need public dependency registry access; offline builds require a
populated cache. Keep normal TLS and checksum verification enabled. Release
scripts use `-mod=readonly`. The Go test suite includes public helper control vectors; no companion Rust
checkout is needed. Optional actual-App/helper tests need their external fixtures.

## Git instance management

```sh
tiana git create my-repo
tiana git list
tiana git show my-repo
tiana git show my-repo --url
```

These commands use MGR account authentication. `create NAME` creates an instance
with the fixed `git` engine and issues and saves its first Token. The engine
is not configurable. Successful output
includes the instance ID, Git URL and Token, so treat captured output as private.

`list` filters all server pages to `git` instances. Interactive terminals
page through the results; redirected output prints the complete list only after
all pages succeed. `show` accepts an instance ID or exact name and rejects
non-Git instances; ambiguous Git names require an ID. Name lookup requires an
updated MGR with `display_name` filtering: names match exactly, case-sensitively,
after trimming surrounding whitespace. Only matching-name pages are read, and
non-Git instances do not count toward ambiguity. ID lookup needs only one detail
request. On older MGR deployments, use the ID from `git list`. `--url` prints
only `tiana://<endpoint>[:port]/repo.git`,
derived from MGR's validated connection metadata with its deployment port.
Missing or inconsistent metadata fails for `show --url`; list/detail display
`-` when no valid connection URL is available. Git management uses the instance
Endpoint, without SQLite branch APIs or a `--branch` option.

Creation accepts both legacy synchronous replies and current MGR asynchronous
receipts. The accepted instance ID is saved before observing its creation
operation; the first Token is issued only after successful creation. Waiting is
bounded to 60 seconds per attempt; interruption or an unconfirmed result retains
the instance for recovery. Creation and Token operation IDs remain separate.
Repeat the same command after an uncertain response to reuse its request identity. Git
uses `git.create` pending records, separate from SQLite's legacy `db.create`;
commands never consume the other engine's pending record. Do not delete an
unresolved record or use an older binary to resume Git creation.

Native clone/fetch/push still use the existing `git-remote-tiana` helper; both
it and `tiana` must be installed together. Explicit `TIANA_TOKEN` and
`TIANA_TOKEN_FILE` are mutually exclusive and take priority. When neither is
set, the helper matches the remote URL's `endpoint_id` in the local
`instance-tokens.json` (override with `TIANA_INSTANCE_TOKENS_FILE`). It uses the
same scoped selection as SQLite direct mode: optional management origin filter,
unique environment/tenant/instance, newest usable Token. It never calls MGR,
prompts for credentials or writes the store. Missing/expired Tokens retain
unauthenticated access for auth-disabled Endpoints; ambiguous, corrupt or unsafe
stores fail before CONNECT. Gateway retains the final authorization decision.

Git remote-helper 会自动读取本地 `instance-tokens.json`，按远端 URL 的
`endpoint_id` 选择 Token。显式 `TIANA_TOKEN` 或 `TIANA_TOKEN_FILE` 优先，
两者不能同时设置；无效的显式来源不会回退。环境、租户、实例隔离与 SQLite
直连一致，不访问 MGR、不改写凭据。没有可用 Token 时保留无 Token 连接，
由 Gateway 决定是否允许访问；本地记录歧义、损坏或权限不安全时直接报错。
See [Git transport setup](internal/gitremote/README.md) for installation and
credential configuration. Management commands do not clone or push Git data.

## Delete an instance

```sh
tiana sqlite delete my-db
tiana git delete my-repo

# Skip confirmation for scripts or an intentional immediate request:
tiana sqlite delete my-db -f
tiana git delete my-repo --force
```

`delete INSTANCE` accepts an instance ID or exact name and deletes the entire
instance, including its branches and data. SQLite and Git commands validate
the corresponding engine; duplicate names require an explicit instance ID.
There is no `--branch` option. Without `-f`/`--force`, stdin must be a terminal:
the prompt shows the resolved name and ID, and only `y` or `yes` (case-insensitive)
followed by Enter confirms. Enter, a negative answer or EOF cancels; Ctrl-C exits
with code 130. Piped input cannot approve deletion; scripts must use `-f`.
The flag only skips confirmation, not authentication or instance checks.

MGR processes deletion asynchronously. Exit 0 with `Deletion accepted` means
HTTP 202 returned a matching instance ID and an operation ID; it does not mean
background cleanup has finished. Cancellation also exits 0, with no deletion
request. Errors exit nonzero; if the result is unconfirmed, check the instance
status and retry only with the printed immutable ID, not a potentially reused
name. The CLI does not add a command-level retry loop. The SDK retains its 401
refresh policy with the same request identity, and MGR deduplicates deletion
by instance ID. Local Tokens are not removed by this command.

SQLite/Git `list` and `show` display deletion progress: `DELETED` takes
priority, then `deletion_pending=true` or a current `lifecycle_state=DELETING`
displays `DELETING`. Other states retain the reported `product_state`. Runtime
lifecycle observations marked stale are ignored; MGR's deletion-pending flag
still applies. Older responses without these fields retain their old display.
An old deletion operation ID alone does not mean deletion is running.

Plain `sqlite show INSTANCE` displays the instance directly while deleting or
deleted, since its default branch may already be gone. Explicit `--branch` and
`--url` retain their branch lookup behavior. `DELETED` rows remain in the list
when MGR returns them; the CLI does not hide them or infer physical storage
reclamation from this state.

An existing unresolved create/Token operation blocks deletion and is preserved.
`-f` cannot bypass this guard. Finish the original operation first.

## Native SQLite commands

Build using the Go modules instructions above.

```sh
tiana sqlite create my-db
tiana sqlite list
tiana sqlite show my-db --url
tiana sqlite tokens create my-db --name app --expiration 7d
tiana sqlite shell my-db
tiana sqlite shell my-db -e 'SELECT 1' --format json
tiana sqlite shell my-db -f migration.sql --format ndjson
```

`create/list/show/tokens create` are the SQLite management entrypoints formerly
accessed through `db`. They use account authentication, not a SQL InstanceToken.
Creation always uses the fixed `sqlite` engine. List displays only SQLite instances,
filtering each server page locally without adding unsupported MGR query parameters.
Interactive pagination follows server pages, which may contain no SQLite rows;
non-interactive output reads all pages before printing and discards partial results
on failure. Show and Token creation reject non-SQLite or missing engine metadata,
including when resuming a pending operation.

The `db` command has been removed, with no compatibility alias. Update scripts
from `tiana db ...` to `tiana sqlite ...` for SQLite instances. General
multi-engine management is no longer exposed by this CLI.
Both Git and SQLite `create` commands reject the removed `--display-name` and
`--engine` flags before network requests. Update scripts to `create NAME`.
Unresolved create operations recorded with those flags must finish using the
previous compatible binary and their original arguments; do not remove flags
from saved intents or delete pending files to force a new operation.

Pending records retain the internal `db.create` / `db.tokens.create` identifiers,
original argument vector and idempotency keys; they are storage keys, not command
entrypoints. A SQLite operation can resume with the same arguments under `sqlite`.
Do not delete an unresolved record. Non-SQLite pending operations require a
compatible older CLI; no credential migration or bulk pending-file rewrite occurs.
Token writes use the instance's authoritative `endpoint_id` and the MGR
endpoint-scoped route. Pending Token intents now also pin that endpoint before
the request; older records without it are filled from MGR instance metadata.
Missing or changed endpoints stop writes; existing operation IDs are read back
without creating another Token. Do not resume a new endpoint-bound pending write
using an older CLI that ignores this field.
For names with significant surrounding whitespace or leading dashes, put the
positional name after `--`, for example `create -- ' name '`. All options must
precede the separator.
Ambiguous trimmed names fail before requests; older pending creates using them
must finish with the older CLI so the request payload is not silently changed.

`INSTANCE` is an ID or exact name resolved by MGR; its engine must be `sqlite`.
Endpoint IDs (`ep-...`) are not instance IDs. Name lookup requires MGR support
for `display_name` (exact, case-sensitive matching after trimming surrounding
whitespace); on deployments without it, use the ID from `sqlite list`.
Sleeping instances are allowed: Gateway handles activation and authorization.
The command uses a dedicated `sdk-go-sqlite.Session`, backed by native sdk-go
TLS 1.3 / HTTP/2 CONNECT with the `hrana-http` profile and Hrana 3
`/v3/pipeline`. The SDK owns transport, rotating batons and value validation.
It does not launch the helper, Turso or the standalone App CLI. Account login
is used only for MGR; the InstanceToken goes only to outer CONNECT.

Token selection in INSTANCE mode: a set `TIANA_TOKEN` is an explicit override. Empty/invalid values
fail without fallback; unset it to use the local store. Otherwise, CLI first
resolves the instance through the currently authenticated MGR account, then
selects a saved Token matching origin, instance ID and endpoint. Tenant metadata
must be unambiguous. Cross-account access is checked by MGR; Tokens are
tenant/instance-scoped capabilities, not bound to their creator.
The newest saved Token with more than 30 seconds remaining is selected (Token ID
breaks timestamp ties, including older records with no meaningful saved time).
A missing/expired Token prompts an actionable error: explicitly run
`tiana sqlite tokens create INSTANCE`. Connecting never creates or rotates Tokens,
checks the remote Token list, or retries another Token after Gateway rejection.
Gateway remains authoritative for revocation.

Automatic lookup reads the existing mode-0600, current-user-owned, regular
`instance-tokens.json` (maximum 8 MiB); symlinks/FIFOs are rejected. No storage
migration is required. `--token-env`, `--token-file`, `--token-stdin` and
`--non-interactive` are removed from the public CLI. SQL stdin is never consumed
as a credential. Shell prompts and list pagination are selected by terminal state.
Account login/refresh behavior remains unchanged.

`--ca-file PATH` is a global flag for MGR login/refresh/management, SQLite,
Git and legacy connect. It overrides `TIANA_CA_FILE`; absent both, system
trust is used. The file must be regular/non-symlink, at most 64 KiB, containing
1..8 certificates of at most 16 KiB each. Deployment roots augment system trust,
and explicit CA configuration forces certificate verification even in legacy
insecure builds. Invalid CA input fails closed before requests. The same parsed
trust snapshot is passed through the invocation, without changing environment
variables or default HTTP transports. Use the global option before legacy
connect/Git commands so native argv ownership stays unchanged:

```sh
tiana --ca-file ./root.crt login
tiana --ca-file ./root.crt sqlite shell INSTANCE
tiana --ca-file ./root.crt sqlite shell INSTANCE -e 'SELECT 1'
tiana --ca-file ./root.crt sqlite shell INSTANCE -f migration.sql
```

`-e/--execute` and `-f/--file` are mutually exclusive. The old `sqlite exec`
and `sqlite run` commands are removed, not aliases. With neither flag, shell reads
SQL from stdin. The release-selected Gateway port still applies.

`--timeout MS` bounds each request including connection/send/body (default 30000,
range 1–3600000). `--output PATH` exclusively creates a mode-0600 regular output
file before connecting; existing files/symlinks fail. Failed files remain as
partial artifacts. `--format table|json|ndjson|csv` selects output; table is the
default. JSON retains ordered columns (including duplicate names), typed values,
i64/row-count strings, unpadded-base64 BLOBs and NULL. NDJSON emits `columns`,
`row`, `statement_end`; CSV quotes all fields and uses CRLF. Table escapes
terminal controls and is limited to 1,000 rows. CSV/table are presentation formats.
Stdout contains results; diagnostics and shell prompts use stderr. Explicit CLI
`--help` uses stdout; usage errors and their help use stderr.

Shell commands are `.help`, `.tables`, `.schema`, `.mode FORMAT`, `.read PATH`,
`.quit`. `.read` uses a literal path with no shell expansion. Multiline statements,
including supported triggers, complete at a semicolon; EOF submits a final
statement without one. Interactive input uses readline: left/right edit the
line and up/down navigate up to 500 submitted lines in this process. History
is not written to disk. Ctrl-C while editing discards pending multiline input;
Ctrl-D on an empty prompt or `.quit` exits. There is no SQL-aware completion.
Readline owns the main and continuation prompts. Piped input retains its exact
source bytes and does not use the terminal editor. The prompt shows
`tx=?` for compatibility; cleanup uses the SDK's confirmed `get_autocommit` state. `shell -e` accepts exactly one
non-transaction-control statement and closes in the same pipeline. Scripts are
preflighted in full and stop at the first failure; earlier autocommits can persist.

**Grammar gate:** the pinned [rqlite/sql parser](https://github.com/rqlite/sql/tree/1b2524a41372)
supports a conservative subset, not the App's complete SQLite grammar. Unsupported
syntax is rejected before the first SQL in a script, including VACUUM, ATTACH,
DETACH, bracket-quoted identifiers and some keyword identifiers such as qualified
`new.rowid`. Mixed identifier quote styles and non-SQLite Unicode whitespace are
also rejected. `--atomic` is deliberately unavailable until a matching grammar
passes differential tests; do not advertise this version as full standalone-CLI
parity. Statements are sent as original source slices, never AST-rendered SQL.
Input, encoded requests and bodies cap at 8 MiB (encoding reserves envelope/baton
space); headers cap at 32 KiB, scripts at 10,000 statements, nesting at 128.

Every session serializes a rotating baton on one tunnel. There is no automatic
retry, reconnect, replay or keepalive. Raw BEGIN IMMEDIATE/EXCLUSIVE, SAVEPOINT, ROLLBACK TO, RELEASE and COMMIT
remain on the same dedicated session. On normal run/shell exit, a known active
transaction is rolled back and reports exit 4; autocommit sessions need no
ROLLBACK probe. Rollback and close share one cleanup deadline of at most three seconds.
Unknown cleanup is reported as `cleanup=unconfirmed`; transport closure alone
does not prove rollback, and a server session may retain locks until its TTL.
Unknown/expired sessions are never reused. Lost responses, including COMMIT,
remain unknown; Ctrl-C does not prove remote cancellation. SQL success retains
the App's local durability meaning and is not an S3 publication barrier.

SQL/runtime diagnostics are JSON with `code`, `outcome`, `message`, `exit_code`
and optional `statement`/`cleanup`; raw peer messages, SQL and tokens are omitted.
Exit codes: 0 success, 1 MGR initialization/resolution, 2 arguments/input,
3 CONNECT failure or explicit rejection, 4 SQL failure/uncommitted rollback,
5 outcome unknown, 6 output failure, 130 interruption. Always check the exit
status even if a complete JSON result document was printed.

Verification is described in [SQLITE_VALIDATION.md](SQLITE_VALIDATION.md).

## Direct SQLite Endpoint connection / SQLite Endpoint 直连

```sh
# Uses a saved InstanceToken matching endpoint_id, or an explicit TIANA_TOKEN.
tiana sqlite shell --endpoint https://ep-00000000000000000000000000.db.example.test:9443
tiana sqlite shell --endpoint ep-00000000000000000000000000.db.example.test -e 'SELECT 1' --format json
tiana --ca-file ./gateway-ca.pem sqlite shell --endpoint https://ep-00000000000000000000000000.db.example.test:9443 -f query.sql
```

`--endpoint` accepts a canonical Endpoint hostname, optionally with a port, or
an HTTPS URL with no path except an optional trailing `/`. Default port: `443`;
explicit ports must be in `1..65535`. Plain HTTP, IP addresses, aliases, bare
Endpoint IDs, URL credentials, query strings and fragments are rejected.
It is mutually exclusive with positional `INSTANCE` and `--branch`: the Endpoint
already selects the target branch. Interactive shell, piped SQL, `-e`, `-f`,
output formatting and timeout options remain available.

Direct mode uses `TIANA_TOKEN` when set; an empty or invalid explicit value is
an error and never falls back. Otherwise, it matches `endpoint_id` in the local
InstanceToken store (`TIANA_INSTANCE_TOKENS_FILE` or the SDK's default config
path). The configured `TIANA_MGR_ORIGIN` (or legacy `TIANA_AUTH_ORIGIN`) restricts
selection to that environment. Without an origin, the matching environment,
tenant and instance must be unique. Ambiguous matches are rejected; within one
scope the newest usable Token is selected, excluding expired or soon-expiring
Tokens. Unset `TIANA_TOKEN` to enable this lookup.

It does not call MGR, prompt for account login, read account credentials, or
create/save Tokens. No management origin is required for a unique local match.
Gateway verifies the Token and target policy; selecting the correct deployment
Endpoint remains the caller's responsibility.
TLS verification stays enabled; use `--ca-file` or `TIANA_CA_FILE` for your
Gateway CA. SQL session/transaction cleanup and no-replay behavior are unchanged.

`--endpoint` 支持完整 HTTPS 地址或 Endpoint 主机名，可带端口，默认 `443`。
仅允许空路径或末尾 `/`，拒绝 HTTP、IP、别名、裸 Endpoint ID，以及含账号、查询参数、片段的 URL。
它与实例 ID/名称、`--branch` 互斥：分支由 Endpoint 决定。
交互 shell、管道输入、`-e`、`-f`、输出格式和超时参数均可继续使用。

直连优先使用 `TIANA_TOKEN`；显式设置为空或无效时直接报错，不回退。
未设置时按 `endpoint_id` 匹配本地 InstanceToken，读取 `TIANA_INSTANCE_TOKENS_FILE`
或 SDK 默认配置路径。配置了 `TIANA_MGR_ORIGIN`（或兼容的 `TIANA_AUTH_ORIGIN`）
时仅匹配该环境；未配置时要求环境、租户和实例匹配唯一，否则拒绝连接。
同一范围中选择最新可用的 Token，排除已过期或将在 30 秒内过期的记录；使用本地 Token 前请 `unset TIANA_TOKEN`。
直连不访问 MGR、不触发账号登录、不读取账号凭据，也不创建或改写 Token。
调用方应选择正确部署的 Endpoint；Gateway 继续执行鉴权。
自签名证书使用 `--ca-file` 或 `TIANA_CA_FILE`，TLS 校验始终保留。
事务清理、未知结果处理及不自动重放 SQL 的行为不变。

## Existing CLI features

This source tree provides a self-contained Linux amd64 and macOS arm64 CLI
for browser authentication, instance and InstanceToken management, and Turso
CLI v1.0.32 with the `hrana-http` /
`hrana-websocket` profiles.
It keeps Turso's native `db shell <replica-url> [sql]` shape: Tiana reads the
canonical Endpoint from that replica URL, creates the tunnel, and replaces
only that URL with the helper-owned loopback locator before spawning Turso.

The CLI creates instances and their first InstanceToken, reports connection
URLs, and issues additional Tokens. Deployment and packaged-helper acceptance require separate verification;
source-only tests do not establish those guarantees.

## Management origin

Public builds have no default management server. Set the deployment origin
explicitly before login or management commands:

```sh
export TIANA_MGR_ORIGIN=https://console.example.test
# Replace the illustrative URL with your deployment's HTTPS origin.
tiana login
```

TIANA_AUTH_ORIGIN remains a fallback environment variable. Management origins must
use HTTPS; HTTP is allowed only for localhost or literal loopback development
addresses. Redirects remain disabled. Missing origin fails
before requests; --help and local Git transport setup do not need a management
server. Configure --ca-file or TIANA_CA_FILE for a deployment-specific CA.

## Browser authentication

Commands that manage Tiana resources use a browser-mediated login when no
local User credential is available:

```sh
tiana login
tiana whoami
tiana sqlite create my-database
tiana logout
```

`tiana login` prints a verification URL and waits for approval, so it works
from SSH sessions and other headless environments. Access and refresh
credentials are stored in `~/.config/tiana/credentials.json` on every platform.
`XDG_CONFIG_HOME`, when set, replaces `~/.config`. The directory has mode
`0700` and credential files have mode `0600`. Users with credentials stored
in the system keychain must run `tiana login` again.
Resource commands keep an unfinished idempotent operation locally until the
control-plane result is confirmed.

## SQLite instance management

```sh
tiana sqlite create my-db
tiana sqlite list
tiana sqlite show my-db
tiana sqlite show my-db --url
tiana sqlite tokens create my-db --name app --expiration 7d
```

`sqlite create NAME` requires one positional database name, then creates the
instance with the fixed `sqlite` engine and immediately issues
its first Token named `default` with no expiry, printing the instance ID,
connection URL and the Token. Only the `sqlite` engine is accepted and it must
be published in the environment before any instance is created. If the
instance is created but the first Token fails, the command keeps the instance
ID locally and a later run completes only the Token step.
For asynchronous MGR responses, the CLI saves the accepted instance ID and
waits for creation to complete before issuing the Token. Temporary visibility
or polling failures retain the same intent. Older incomplete intents without
an instance ID recover by replaying their original create idempotency key;
repeat the original command, not a new name or a different product.
`sqlite show` accepts an instance ID or an exact display name; `--url` prints only
the connection URL. `sqlite list` pages on an interactive terminal and prints the
complete list once when stdin or stdout is redirected.

`sqlite tokens create` writes only the raw InstanceToken to stdout so it can be
captured directly; Token metadata goes to stderr:

```sh
export TIANA_TOKEN="$(tiana sqlite tokens create my-db --name app --expiration 7d)"
tiana sqlite shell my-db -e "SELECT 1"
unset TIANA_TOKEN
```

`--expiration` accepts `never` (the default) or a positive number followed by
`s`, `m`, `h`, `d` or `w`. Delivered InstanceTokens are stored separately from
the account credential, keyed by MGR origin, Tenant, instance and Token, in the
local file `~/.config/tiana/instance-tokens.json` with mode `0600`
(or `$XDG_CONFIG_HOME/tiana/instance-tokens.json` when set).

Token API requests and responses, saved Token credentials, and pending commands use
Unix seconds for `expires_at`; `-1` means no expiry. The CLI resolves a duration
once and preserves that absolute value when retrying the same pending command.
Displayed finite expiry dates remain UTC.

## Native Turso command

With `TIANA_TOKEN` or a locally saved InstanceToken matching the Endpoint:

```sh
tiana connect -- \
  turso db shell https://ep-00000000000000000000000000.db.example.test
```

Execute one SQL statement by keeping the native Turso argument in place:

```sh
tiana connect -- \
  turso db shell https://ep-00000000000000000000000000.db.example.test \
  "SELECT 1"
```

The remote locator must be an `https://` URL containing exactly one canonical
Tiana Endpoint hostname and an optional deployment port (default `443`). User-info, paths,
queries, fragments, IP addresses, aliases, Turso Cloud database
names, and a second locator are rejected before helper launch or network I/O.

The current registered adapter accepts only the reviewed
`turso db shell <replica-url> [sql]` grammar. Turso Cloud's `--instance` and
`--location` selectors and the native `--proxy` selector are not supported in
Tiana mode. A future Tiana-owned proxy option may be added before `--`; this
release does not implement it.

## Legacy connect credentials

`connect` uses `TIANA_TOKEN` when set; empty/invalid explicit values never fall
back. Otherwise it looks up a saved InstanceToken by the native URL's
`endpoint_id`, using `TIANA_INSTANCE_TOKENS_FILE` or the SDK default path.
The optional management origin filter and unique origin/tenant/instance scope
rules are the same as SQLite direct mode. No MGR request or account login occurs.
If no usable Token exists, an interactive terminal receives the hidden-input
prompt; non-interactive execution reports missing credentials. Ambiguous,
corrupt, unsafe or invalid saved credentials fail before helper launch, without
prompting. Lookup never consumes native stdin or writes the store.

`connect` 优先读取 `TIANA_TOKEN`；未设置时，按原生客户端 URL 的 `endpoint_id`
查找本地 `instance-tokens.json`，也支持 `TIANA_INSTANCE_TOKENS_FILE` 自定义路径。
环境、租户、实例隔离和过期筛选与 SQLite 直连一致，不访问 MGR。
没有可用记录时，交互终端保留隐藏输入提示，非交互模式报错；文件损坏、
权限不安全、匹配歧义或选中 Token 无效时直接报错，不提示输入替代凭据。

Raw `--token` and the removed Token-source flags are rejected before the native
`--` separator; arguments after it remain the native client's responsibility.
The Token is not saved by connect.

For piped native commands, `--allow-unisolated-loopback` is still required.
An interactive terminal retains its documented isolation policy.

## Endpoint routing and profiles

The Endpoint hostname resolves directly to Gateway. Use the connection URL
provided by your deployment: its hostname supplies TLS SNI and its port supplies
the TCP destination (default `443`). HTTP/2 CONNECT uses the same hostname with
logical port `443`. For a local environment, pass its CA PEM file with
`--ca-file DIR/secrets/tls/root.crt`, or set `TIANA_CA_FILE` to that path.
An explicit CA enables certificate verification for the connection.

Use a deployment CA for self-signed development certificates. Both source and
release scripts default `TIANA_INSECURE_TLS` to `false`. The explicit build-time
override `TIANA_INSECURE_TLS=true` disables certificate verification for MGR
requests and legacy helper tunnels while retaining TLS encryption. It is for
controlled testing only and must not be used in distributed artifacts. Native
Git, SDK-backed SQLite and the built-in Go helper retain certificate verification.
The built-in helper rejects an insecure TLS configuration; this override applies
only to the legacy helper path and MGR.

Without `--profile`, the helper's bounded classifier selects Hrana HTTP or
WebSocket from the native client's complete initial request header. The
diagnostic `--profile hrana-http|hrana-websocket` option can force one reviewed
profile. The CLI does not automatically retry or replay a database session
after CONNECT 200.

## Built-in connect helper / 内置连接 helper

A normal `go build -o bin/tiana ./cmd/tiana` includes the connect helper on
Linux and macOS. No separate `tiana-helper`, Rust build, or installation manifest
is required. The supervisor launches the same CLI executable as a private child
process, with the existing contract-3 control pipes. The helper owns its loopback
listener and SDK transport; it accepts connections only after the native child
handoff. Account credentials and `TIANA_TOKEN` are removed from the native child
and helper environments; the outer InstanceToken reaches the helper only through
the private control pipe and Gateway only through TLS CONNECT.

```sh
# Uses TIANA_TOKEN or a saved endpoint Token; omit --ca-file for a publicly trusted CA.
tiana --ca-file ./gateway-ca.pem connect --allow-unisolated-loopback -- \
  turso db shell https://ep-00000000000000000000000000.db.example.test:9443 'SELECT 1'
```

The reviewed native adapter remains `turso db shell`; the native client itself
must be installed. The local listener reports `loopback_unisolated`, not
`same_user` or `strict_process`. Non-interactive use still requires
`--allow-unisolated-loopback` or an explicit compatible minimum-security policy.
Linux requires pidfd support to pin/monitor the native process identity; macOS
uses process-exit notifications. Parent control EOF, owner exit, cancellation or
drain closes listeners and active sessions.

The bounded classifier accepts HTTP/1.1 POST `/v2/pipeline`, `/v3/pipeline`,
`/v3/cursor`, or the Hrana WebSocket upgrade at `/`. It allows at most 16 KiB
of headers, 64 fields and one second to receive the header. At most 32 local
sessions run concurrently; excess connections are closed. The retained prefix
and all following bytes remain opaque and are forwarded once, only after a
validated CONNECT 200. The CLI does not replay failed requests. HTTP framing,
SQL and transaction semantics remain the native client/App's responsibility.

普通 Go 构建已包含 helper，无需安装独立 `tiana-helper` 或编译 Rust。
CLI 以自身二进制启动独立 helper 子进程，保留私有控制管道、Token 隔离、TLS 校验
和 CONNECT 200 前不发送数据库请求的约束；`verify-install` 会实际握手并报告 `mode=builtin`。
仍需安装 `turso` 原生客户端；非交互模式继续显式使用 `--allow-unisolated-loopback`。
内置 helper 不提供比 `loopback_unisolated` 更强的本地访问隔离，也不关闭 TLS 校验。

## Verify installation

```sh
tiana verify-install
# A normal Go build reports: helper-contract=3 mode=builtin
# A legacy embedded-helper build reports: helper-contract=3 mode=embedded

tiana --version
tiana --help
```

## Optional legacy embedded-helper release build

For a native release build from both source checkouts, run:

```sh
./scripts/build-release.sh
```

The script supports Linux amd64 and macOS arm64 hosts. It installs the matching
Rust target, builds the release `tiana-helper` from the sibling `../sdk`
checkout, and writes the CLI, `git-remote-tiana`, and checksum files to `dist/`.
The generated development version contains a UTC timestamp. Set
`TIANA_RELEASE_VERSION` to choose an explicit version. Certificate verification
is enabled by default; `TIANA_INSECURE_TLS` remains available for controlled
internal development builds. Linux builds require a musl C compiler; on
Ubuntu/Debian, install the `musl-tools` package once before building.

For lower-level or cross-build use, build the matching Rust helper (helper
contract 3) as a static `x86_64-unknown-linux-musl` executable, then embed that
exact helper into the next CLI artifact:

```sh
TIANA_RELEASE_VERSION=0.1.0-dev.7 \
./scripts/build-single-binary.sh \
  linux amd64 \
  ../sdk/dist/tiana-helper-linux-amd64-0.1.0-dev.5 \
  ./dist/tiana-cli-linux-amd64-0.1.0-dev.7
```

The scripts verify certificates by default (`TIANA_INSECURE_TLS=false`).
Do not distribute builds made with the explicit insecure override.

The builder validates both input architectures, injects the exact helper
SHA-256, uses `CGO_ENABLED=0`, verifies the result in embedded mode, and emits a
matching `.sha256` file. Linux executes the helper from a sealed `memfd`.
macOS stages it in a private temporary directory and removes it after exit.

The older root-owned multi-file bundle remains an optional hardened/system
installation workflow through `build-linux-bundle.sh`.

## Internal TLS debugging (not for distribution)

Only internal CLI builds compiled with `-tags tiana_debug` accept `--insecure`.
This explicit option disables certificate identity verification while retaining
TLS encryption; use only with test credentials in a controlled environment.
Place `--insecure` before `--`; it applies only to the current `connect`
invocation, not to browser authentication or resource-management requests.
Normal builds reject the option. Release scripts clear `GOFLAGS` and never
select the debug build tag. Never distribute either debug artifact.

`TIANA_INSECURE_TLS=true` is the binary-wide equivalent: it applies the same
verification bypass to the Gateway tunnel and to MGR requests, and requires no
per-invocation option. Keep it out of artifacts that leave the development
environment.

The SDK's default build excludes the insecure verifier. Build release helpers
without `internal-debug-tls`; do not reuse internal debug helpers for releases.

The CLI preserves the native client’s exit status. A local socket closure during
shutdown is successful only when the native client exits with status 0; an
upstream tunnel interruption still fails even after SQL output was received.
SQL is never automatically retried.

## Git through Gateway

`tiana git remote-helper` is implemented in Go. The packaged `git-remote-tiana`
launcher execs the sibling `tiana` with that subcommand. See [build, connection
and validation instructions](internal/gitremote/README.md). Git requires no Rust helper.
Git always verifies TLS certificates and hostnames, including builds using
`TIANA_INSECURE_TLS=true` for MGR/SQLD. Supply the deployment CA through
`TIANA_CA_FILE` if it is not in the system trust store.

## SQLite 分支连接

`--branch` 接受精确分支名称；省略时选择 `branch_id=main`，与默认分支的可变显示名称无关。

```sh
tiana sqlite branches list INSTANCE --search preview
tiana sqlite show INSTANCE --branch development --url
tiana sqlite shell INSTANCE --branch development
tiana sqlite shell INSTANCE --branch development -e 'SELECT 1'
tiana sqlite shell INSTANCE --branch development -f query.sql
tiana sqlite tokens create INSTANCE --branch development --name cli
```

列表只取一页，输出的下一页游标可传给 `--after`。连接时通过 MGR 的精确名称查询定位分支，再从分支详情读取 `connection`；名称不存在或查询期间改变时报告错误。

`TIANA_TOKEN` 优先于本地凭据；未设置时，按管理入口、实例和所选 Endpoint 查找已保存的数据库 Token。实例连接模式下，账号登录会话用于 MGR 查询，即使提供数据库 Token，首次访问该管理环境仍需登录。shell 不自动创建 Token；显式签发会保存目标 Endpoint 和 Token，结果未确认时重试保持原 Endpoint、幂等键和 Operation。

使用 `TIANA_CREDENTIALS_FILE`、`TIANA_INSTANCE_TOKENS_FILE` 和 `TIANA_PENDING_COMMAND_FILE` 可将三类状态放在独立目录；`TIANA_MGR_ORIGIN` 选择管理环境，`--ca-file` 或 `TIANA_CA_FILE` 选择该环境 CA。

## Authentication and local-state safety

Account and pending reads require a current-user-owned regular mode-0600 file,
reject final-component symlinks/FIFOs, and cap JSON at 8 MiB. Oversized writes
fail before replacing old data. Unsafe legacy/custom files fail closed; inspect
the configured path and repair its ownership/permissions before retrying. The
existing JSON keys and credential locations remain compatible. Owned parent
directories retain the private mode-0700 policy; symlink final directories fail.

The matching SDK serializes account refresh/logout and file read-modify-write
across cooperating Linux/macOS processes, using a persistent adjacent `.lock`
file. Refresh reloads credentials after locking, so a losing process does not
reuse an old refresh token or delete a replacement. Acquisition honors context
cancellation with a 30-second maximum wait. Different origins in one account
file briefly share this lock; no SQL/Git transfer data path is locked.
Custom credential stores must provide their own cross-client/process atomicity.

Mutating CLI management commands hold a separate pending-file lock for the full
operation. A competing invocation fails locally before requests; retry after
the first command exits. Any mismatched unresolved pending operation, including
another origin, blocks replacement. Keep its original IDs and resume it with
the matching environment; do not delete an unresolved record or the lock inode.
Locks release on process exit/crash without deleting their files. Old binaries
that ignore locks must not run concurrently against the same stores. This does
not add parent-directory fsync or change server commit/durability semantics.

Management diagnostics omit arbitrary peer error messages and only print known
error codes. Displayed resource names, IDs and other text escape terminal
controls/format characters. API payloads, stored state, SQL result encodings and
intentional Token delivery remain unchanged. Keep successful Token command
output private; it is intentionally delivered to the caller.

The Linux bundle uninstaller validates root ownership, non-writable ancestors
and all existing intermediate directories before deletion. Symlinked prefixes
or path components are refused; use a direct trusted installation path.
