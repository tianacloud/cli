# Tiana CLI development

Module and canonical remote: `github.com/tianacloud/cli`. This file records current
constraints and decisions, not historical task authorization. More specific
`AGENTS.md` files apply within their directories. Historical implementations and
superseded decisions remain available in Git history.

## Working and release boundaries

- Preserve existing work; inspect branch/status, README and build configuration
  before editing. Use a task branch. Do not modify read-only reference repositories.
- Commit, push, merge, tag, npm publication and deployment each require explicit
  authorization from the current user task. Prior task notes do not grant it.
- Keep public module/import/linker paths on GitHub. Use published/pinned SDKs with
  Go-generated checksums; no product `replace`, vendored fork or private history.
- Public source, documentation, decoded fixtures and binary strings must not leak
  secrets or private deployment addresses. Use `example.test`, documentation IPs
  and synthetic credentials in tests. Approved deployment defaults/trust assets
  belong only in `internal/clientconfig`; keep public-source policy tests current.
- TLS verifies certificates and hostnames by default. Insecure debug settings are
  never release defaults. The source scan is not a comprehensive secret audit.
- Record new interface, configuration, persistence, concurrency or resource-bound
  decisions here with invariants, tradeoffs, rollback and verification requirements.

## Deployment selection, trust and CLI surface

Resolve the management origin in the CLI and pass it explicitly to SDKs: present
`TIANA_API_ORIGIN`, otherwise the online clientconfig default. Saved accounts do
not select the origin. Explicit empty input fails closed.
CLI and native Git inherit the process environment. Skills propagate an origin
explicitly selected by the user to each task subprocess.
Ignore `TIANA_MGR_ORIGIN` and `TIANA_AUTH_ORIGIN`.
There is no CLI `--config`; native arguments after `connect --` remain untouched.

Trust precedence is `--ca-file`, `TIANA_CA_FILE`, then the embedded staging root
only when TIANA_API_ORIGIN selects staging; otherwise use system roots. Extra
roots are added to system trust. Preserve verified TLS in source and packaging builds.
Environment origin overrides permit HTTP only for loopback development.

Products are `web`, `sqlite` and `git`. Removed `apps`, `whoami`, `verify-install`,
SQLite Token commands and `branches` spellings have no compatibility aliases.
Instance create requires one nonblank NAME and fixed engine `sqlite` or `git`;
removed engine/display-name flags fail before state/network work. Descriptions
are verbatim valid UTF-8, <=2048 bytes for both instances and Web (Web description projects instance notes).

SQLite/Git instance list, SQLite branch list and Web list fetch all API pages before
printing one complete table, including on terminals. There is no More prompt or
list-input consumption. Web JSON output also includes all pages. Keep existing
product filters, authentication, cursor validation and error handling; a failed
page must not produce a partial successful table. API pagination remains internal.
Branch list preserves search and the optional starting --after cursor across pages,
rejects cursor cycles, and retains the same 10000-page bound as Web traversal.

SQLite/Git list, SQLite branch list and Web list show descriptions in a trailing
`MESSAGE` column (instance/branch `notes`, Web `description`). Instance lists omit
the engine column; engine decoding and product filtering remain required. Escape
descriptions at presentation only. No API, JSON output or persistence migration;
missing descriptions display empty. Test HTTP readback, pagination and terminal
controls. Rollback changes table columns only.

## Authentication and private local state

SDK auth owns account login/refresh/logout and credential storage. All CLI account
readers, native connections and Web previews use the default SDK credential store:
$XDG_CONFIG_HOME/tiana/credentials.json, otherwise ~/.config/tiana/credentials.json.
There is no per-file account credential environment override. Keep origin isolation
and existing file permissions; removing the override does not migrate or delete
previously selected files.

Connections
(shell, direct Endpoint, Git and connect) use one resolver: mutually exclusive
`TIANA_TOKEN` / `TIANA_TOKEN_FILE`, otherwise the selected origin's unexpired
account access token. Explicit presence, including empty/invalid input, prevents
fallback. Connections never trigger login, refresh, token issuance, anonymous
retry or a credential prompt. Expired/missing sessions require explicit login.

Never read, create, migrate or delete `instance-tokens.json`, including obsolete
`TIANA_INSTANCE_TOKENS_FILE` overrides; leave existing files untouched. Credentials
are outer Gateway CONNECT authentication only, never native argv/env, inner SQL/
Git traffic or diagnostics. Endpoint syntax does not establish deployment trust.

Explicit token files are owned regular nonsymlink mode0600 files, <=512 credential
bytes plus one LF/CRLF. Private directories are mode0700 on Unix; Windows uses
owner-only ACLs. Maintain origin isolation, credential paths/JSON and expiry checks.
Constructors must not access real stores. Do not expose peer messages or secrets.

Pending files have an 8 MiB bound, owned regular mode0600 files and no symlinks,
FIFOs or special files. Validate opened parent descriptors, use same-directory
atomic rename and file fsync; directory fsync is not currently guaranteed. Hold
one persistent per-pending-file lock through loading, mutation and receipt output;
never unlink lock files. Contention/unmatched intents fail before replacement.
Account refresh has a separate lock; ordering is pending then account, never reverse.
Cooperating processes serialize; older/custom writers still require coordination.

Escape controls/Unicode format characters at management presentation boundaries,
without changing stored/API metadata, SQL output or protocol bytes. Request IDs
and structured operation identities remain available without raw peer errors.
Uninstallation validates trusted ancestors and intermediate paths before removal.

## Mutations, receipts and waiting

Instance create persists the original request identity before POST and accepted
receipt before stdout. Accept a nonempty instance ID plus positive uint64 job ID
or nonempty operation ID, preserving legacy synchronous replies. Keep creation
job/operation identities distinct; no first Token or endpoint inspection.
Unknown responses repeat only the same payload/request identity; output failure
recovers the saved receipt without a new POST. Never resolve by name to invent a
replacement identity or discard an unknown-outcome record. Unrelated/unsupported
pending commands are refused generically, without migrating legacy Token state.

SQLite/Git create `--json` writes one structured result to stdout, with progress
on stderr. Status distinguishes accepted from wait-confirmed succeeded; data
retains instance/job/operation identity. Format flags do not change pending argv.
Verify both engines, waits, errors and receipt recovery when changing output.

Default create is asynchronous. `-w` / `--wait` are observation-only options,
excluded from saved argv only before `--`; preserve description values and names
that resemble flags. Successfully delivered/cleared receipts mean a later create
is a new request. Keep the pending lock and recovery IDs through wait/output errors.

Instance create polls its bound MGR job once per second. Validate job ID, instance,
kind and original request ID. Completed/removed jobs require the original instance
to be ACTIVE, with matching engine/known operation and no deletion underway.
A job 404 alone is not success. Legacy operation-only receipts are checked once;
waiting is unsupported unless already ACTIVE. Never mint a Token while waiting.

Instance deletion pins an immutable product-scoped ID before terminal confirmation;
only y/yes accepts, EOF/empty/no cancels. `-f` skips confirmation only. Reject reused
name assumptions after uncertain outcomes. Validate HTTP202 receipt identities;
default success means accepted, not completed or storage fully reclaimed.
DELETED state takes precedence over DELETING; stale runtime lifecycle is ignored,
durable MGR deletion_pending remains usable. Plain show does not need an absent
default branch during/after deletion. Old operation IDs alone do not imply progress.

SQLite branch mutations resolve exact scoped targets and protect root/default
branches. Parent omission selects main by immutable ID; `--by-id` never falls back
to name. Create accepts verbatim notes <=2048 bytes, TTL 1..2592000 seconds, and
uint64 Unix-second timestamp (omitted differs from explicit zero). Preserve these
fields through resolution/wait; only the server validates retained history.

Branch mutations have no local resume record or server deduplication guarantee.
Never replay create automatically or treat rerunning it as resume. Delete retries
use original IDs after inspection. SDK explicit 401 refresh preserves target/body/
request key; no transport/5xx mutation retry. Existing pending intents block writes.

Branch create/delete and instance delete waits poll bound operations once per
second; validate kind, instance and immutable parent/branch. CREATE_BRANCH success
needs a distinct child identity. Failed, 404, unknown/malformed replies and API
errors fail closed. No total wait timeout; Ctrl-C exits 130 without cancelling
server work. Print receipts before observation; preserve output errors. Holding
mutation locks serializes writes while read-only commands remain usable.

## SQLite and native Git data safety

SQLite uses the SDK's exclusive Hrana v3 Session; CLI owns preflight, original SQL,
readline and ordered lossless output. Transactions/savepoints stay on one stream.
Rollback of a known active transaction and close share a three-second cleanup
budget; unknown responses/COMMIT outcomes stay unknown. Closing transport proves
neither remote cancellation nor rollback; server locks may survive until TTL.

Interactive recovery only retries once after an explicitly unexecuted BATON_INVALID
outside a transaction, within the original timeout. Reconnection resets session
state and cannot restore temporary tables/settings/transactions. Transaction loss
or unknown outcome stops current input; the next interactive input can open a new
session. Scripts, pipes, `-e`, `-f` and noninteractive mode stop without replay.
Preserve input/result bounds, private exclusive output files and public exit codes.

SQL input is tokenized only for statement boundaries, completion and resource
limits; the CLI has no full SQL grammar/AST gate. The server validates syntax.
Keep quotes/comments/brackets, native parameter suffixes, parentheses and
CREATE [TEMP] TRIGGER bodies indivisible; never split a semicolon inside opaque
module arguments. Keep original source bytes, 8 MiB/10000 statements/128 nesting,
UTF-8/NUL checks and the encoded-request budget before executing any script.
Preserve explicit VACUUM/ATTACH/DETACH policy, including EXPLAIN prefixes, and
-e single non-transaction-control behavior (END and savepoints included).

Scripts execute sequentially and stop at the first error; a later syntax error
can follow earlier committed statements. Do not auto-wrap user SQL in a
transaction. Runtime transaction state and cleanup/recovery use get_autocommit,
not keyword guesses. Known server SQL_PARSE_ERROR needs the matching SQLite SDK
fix to preserve a coherent session; unrecognized/unknown outcomes still poison
it and never authorize replay. The immutable remote SDK v1.0.1 includes that fix and is now pinned. Standalone
GOWORK=off verification must resolve that published tag, with no product local
replace/workspace dependency. Do not declare a nonexistent published version or commit local
replace/workspace dependencies. Validate lexical boundaries, extensions, PTY,
resource preflight, error-after-write and parse-error transaction continuity.
No protocol/storage format change; rollback restores the prior CLI grammar gate.

`--endpoint` accepts canonical SDK Endpoint DNS with optional TCP port or HTTPS
root URL. Reject userinfo/query/fragment, aliases/IPs/bare IDs and invalid ports;
normalize DNS case/default 443. INSTANCE/branch conflict with direct mode. Original
hostname stays SNI/CONNECT authority; TCP overrides never change logical identity.

Native Git uses verified TLS 1.3/H2 CONNECT and a pre-200 barrier. One invocation
dials once: no replay/proxy/fallback. Preserve cancellation, flow control, half-close,
opaque bytes and native exit status. The launcher executes its sibling CLI rather
than searching PATH. Failed/unknown pushes require remote inspection before retry.

## Built-in connect helper

Ordinary Go builds re-execute the current binary as the helper (Linux /proc/self/exe),
using frozen contract 3 private control pipes; supervisor never handles database
bytes. Embedded release helpers retain installation/hash and release verification.
`tiana version` alone does not establish a working helper session. `tiana version` prints the current CLI version; root `--version` and `-v` are rejected. Web
upload/status retain their local `--version` release selector. Native client
arguments remain unchanged.

Keep HELLO/config/credential/BOUND/READY/CHILD_STARTED/SERVING order. Bind loopback
only; accept after child identity handoff and honestly report loopback_unisolated.
Linux validates starttime around pidfd_open and monitors pidfd; macOS uses the
existing zero identity/kqueue exit contract. Pipe EOF, owner exit and drain stop
accepts/cancel sessions. Keep control open on owner exit until supervisor DRAIN/EOF.
Native children never inherit credentials/control pipes. Clear mutable credential
buffers; immutable SDK Go strings are not guaranteed zeroized before process exit.

Verified TLS 1.3/H2 is mandatory; built-in mode rejects insecure helper config.
Classifier limits: 16 KiB headers, 64 fields, one second, <=64 KiB retained pre-200.
Support Hrana v2/v3 pipeline/WebSocket/cursor with exact explicit profiles; max 32
active local sessions with bounded copy buffers. Preserve after-200/unknown errors,
cleanup and child exit status. No SQL/request replay or connect credential persistence.
Sanitized control vectors have recomputed hashes and are not original byte copies.

## Web delivery and cross-module contracts

Use `/api/v1/web-projects`, project/release JSON `id`, manifest/Bootstrap `web_id`.
New IDs are 12 unpadded base64url characters from 9 random bytes; existing IDs remain.
Use `/web/<id>/` hosted/preview and `/console/web/<id>` Console routes. Inject the
absolute Bootstrap script at `id=tiana-bootstrap-script`; preserve version/hash
routes and byte-identical shared CLI/Web runtime files.

Creation/list traversal pins origin/user/tenant. Retain exact description/request
identity across unknown outcomes. List uses owner-scoped cursor pages, caps 10000
pages, rejects malformed/nonadvancing cursors and foreign records; exact-name
resolution checks all pages and rejects ambiguity. Names never replace IDs.

Upload preserves exact scoped URLs/headers, immutable fingerprints, content
checksums, private ACL, forbid-overwrite and reconciliation. Management
credentials never reach object storage; rejected/uncertain management writes are
not replayed automatically. Confirmed publication returns the MGR project endpoint. Web list also displays
the configured hosted address; its presence does not assert publication or readiness.

Preview has one-use local launch authorization; account refresh secrets stay local.
Hosted auth uses HttpOnly Web sessions to renew short-lived tenant grants, exact
Origin/CSRF and connection POST `{}`. No browser refresh token or instance-token
fallback. Coordinate CLI/Web/MGR provider contracts and data-plane synchronization;
trusted app code has tenant scope. Recover lookups but never replay SQL.

Web project entry and optional Git/SQLite associations are fixed at create.
source_commit and web update are retired. Web list uses owner-filtered generic
instance pagination with engine=web; name/description/revision project generic
instance metadata. Generic revision strings must retain uint64 precision.


CLI Web delete preserves SQLite/Git and sends false association deletion flags,
without a product version selector or expected_version_id. The server persists
the accepted immutable instance selections and resumes their durable deletion
plan after response loss or restart. Changed selections read the original receipt;
they do not replace accepted intent. Instance lifecycle owns Web object cleanup,
including publication stop and deletion of the sole current object. Project
request/deletion timestamps and the saved plan remain durable.
No separate deletion table/error/lease
state is introduced; owner maintenance logs retain errors. Accepted receipts do not
revoke cached/issued URLs. Keep WEB_* product errors separate from engine App events.
The MGR worker visits <=10 applications per 20-second pass and uses <=5-second
row-locked transactions with <=500-object/metadata batches. SKIP LOCKED excludes
competing workers; a transient cursor prevents failed rows starving later work.
Keep release metadata until origin cleanup completes, validate storage namespaces,
and rely on durable deleting state plus idempotent OSS deletion after restart.

## Account status and diagnostics

`status` verifies the account noninteractively then reads tenant usage summary;
never infer tenant or usage from detail counts. Preserve decimal-string uint64 and
Unix-millisecond precision, zero vs unknown, blocked/reason and partial failures.
Missing login or unavailable quota exits 1; retrieved blockage itself exits 0.
Print available account details and quota, omitting User ID/Management/Tenant lines.
Use exact bounded math for three rows, one-decimal percentage and 20-cell bars;
preserve tiny/nonzero/below/above-limit distinctions and cap only bar width.

`TIANA_DIAGNOSTICS=1` exposes request identities on stderr without secrets, SQL
results or Git protocol bytes. No request ID exists for purely local failures.

## Verification and delivery

Use `GOWORK=off`, downloaded pinned modules and `-mod=readonly`. Run relevant tests,
full CLI race/vet, module verification and `scripts/check-public-source.py` plus its
positive/negative PEM/hex scan regressions. Changes affecting packaging/helper/Web
runtime also need corresponding Node, protocol, process and browser fixture checks.
Build macOS/Linux and affected supported targets; native help/version checks alone
are not deployed acceptance. Keep packaging TLS policy and manifest digests valid.

Identify fixture, cross-compile and real Gateway/App/Turso/OSS acceptance separately.
Report branch/base, changes, validation, unknowns and rollback impact. Never claim
live upload recovery from anonymous 403 or local tests; actual PUT/publication and
public/VPN network scenarios require their own verification and authorization.

## Web Project metadata v1.1

The approved design supersedes earlier Web manifests, versioned publication and
styles guidance: MGR Web Project stores create-only entry and optional DB/Git,
revision CAS and owner/admin Site gates. Generic instance config has no Web fields.
tiana.app.json is retired; entry JS loads CSS. Publish packages empty root web.yaml
plus data/, validates the fixed entry, and uploads channel 1. Channel 0 remains
native GET/HEAD, v4 unchanged. CLI serve requires ID and saved project parameters.
No entry override on update/publish/serve. No migration or deployment is implied.


## Type-independent data Token authorization

Token scope is independent of instance engine and business channel. Endpoint,
Instance, Group and Tenant credentials may authorize SQLite, Git or Web when
valid and their scope covers the target. Web publication/control still requires
an authenticated credential even when resource reads are anonymous; do not add
an instance-kind whitelist. Preserve expiry, revocation, owner revisions, quota,
instance enablement and protocol checks. Site/project owner/admin policies and
MGR session/tenant/role checks remain separate from data authorization.

CLI Web publication/status uses the same explicit TIANA_TOKEN / TIANA_TOKEN_FILE
precedence as other connections, otherwise the pinned account's access token.
It never reads local instance-token caches or retries with alternate credentials.
The account still resolves MGR project metadata. Unknown publication outcomes
retain their identity/archive and are never automatically replayed. No wire,
schema, token issuance, storage or Gateway–Agent version changes are involved.
This current decision supersedes earlier instance-only Web publication and
account-credentials-never-reach-Gateway guidance.
## Web templates and update notifications

`web list-template` reads one current MGR catalog page, including descriptions,
status, variables and next cursor. `web init-template NAME --dir DIR` downloads
an MGR-signed private OSS ZIP, verifies size/SHA-256, preserves source bytes and
metadata, and atomically creates the absent target. Agent instructions own all
variable substitution and database initialization. Public template requests use
account auth; signed object reads use an independent HTTP client. Never persist
or print the signed URL. CLI has no template version selector.

npm delivery uses an exact-version main package and seven os/cpu optional native
packages. The launcher resolves the installed package and preserves native I/O,
arguments, signals and exit status. Release Actions build source, retain eight
integrity-indexed tarballs, verify native installations and publish platform
packages before main. Retry publication from the original artifacts. Conflicting
published content requires a new version; changing a source tag is not recovery.

`version check --skills-version VERSION --json` checks npm latest for the main
CLI and Skills packages with a shared two-second budget. Per-package attempts,
success and known latest versions live in UserConfigDir/tiana/update-state.json;
all packages share one 24-hour reminder allowance. Nonblocking persistent locks
and atomic file replacement reserve notification before returning should_notify.
Failure or lock contention does not block a product operation. Ordinary terminal
commands use cached results and launch one bounded refresh process; JSON,
noninteractive, version/check and native protocol invocations skip passive
notifications. Background refresh never reserves a reminder. Version checks do
not install packages; users confirm upgrades through npm/Skills installation.

Validate catalog freshness, exact-byte extraction, credential separation, package
selection/integrity/launcher behavior, daily concurrency and native protocol
silence. npm installation/publication is a separate acceptance gate from local
fixtures and cross compilation. Rollback a published release by selecting a
previous package version; installed templates remain ordinary local source.

## Instance enablement convergence

Control instances.enabled is the sole instance access switch for all engines.
Gaia and public owners reuse the existing enable/disable operations. There is no
independent administrator block or special unblock workflow; an owner can enable
an instance after an administrator disables it. MGR Web project metadata has no
access columns; fresh Control enabled is required for Site access, and missing or
stale observations fail closed. Metadata remains readable while status is stale.
Preserve fixed Web associations, generic metadata CAS and durable deletion plans.
Existing authentication, private management ingress and mutation audit remain.

This task removes the newly introduced generic admin-block without reverting
Web metadata convergence or CLI MESSAGE display. No live DDL/deployment is
authorized. Published contracts tags remain immutable. Control's final schema 13
removes schema-12 admin fields and request index; MGR remains schema 35.

## CLI 1.0.0 dependency and version baseline

Both Go SDK dependencies now use v1.0.0 after the SDK history reset. Do not
restore deleted SQLite SDK prerelease tags, local replacements or old checksums.
Source defaults, release script defaults and npm package/optional dependencies
are 1.0.0; release overrides stay supported. No protocol, persistence, credential
or TLS policy changes accompany this dependency/version update. Verify clean
module resolution, race/vet, packaging and actual App SQLite compatibility.


## Reviewed CLI output and local state fixes (unpublished source)

Management JSON uses one status/data/error envelope, all-page lists and noninteractive account checks. New fields preserve decimal-string 64-bit quantities; existing create job_id keeps its number contract. Raw connect/Git streams and SQL formats remain independent. JSON flags do not alter request identities or permit retries. JSON delete requires force, accepted/completed/failed/unknown remain distinct, cancellation only stops observation. Preview startup JSON carries a short-lived launch capability solely on stdout; never log it.

All new default state paths derive from SDK DefaultCredentialPath. A legacy platform pending record blocks new default mutations until explicit original-path recovery. Do not move/delete records or lock inodes; old/new writer coordination is required across upgrades. Publication receipts in previous directories remain untouched. Preserve private permissions, locks, same-directory atomic writes and request-before-effect ordering. No new credential override, SDK pin, database schema or wire protocol.

Login outputs only verified login/pending facts: no pending is not logged out, failed storage may leave resumable credentials, and unknown states are omitted. Error diagnostics retain safe local paths and allowlisted causes, never contents or arbitrary peer errors. Preserve the single-use exchange guard and recovery without another exchange after account-save failure.

Web current and receipt status are distinguished additively with query_kind; reject null/non-object control responses. Upload success is not activation; use running plus target remote/serving checksums. Verification includes malformed replies, persistent recovery, no replay, precision, machine-output failures, root argument parsing, full race/vet, public source checks and supported-target builds. These source changes need a coordinated CLI/Skills release; no publication is authorized by this note.

## CLI 1.0.2 publication (2026-10-10)

Current user authorizes committing/pushing this task to main and npm publication.
Release 1.0.2 from a new immutable matching source tag; do not rewrite v1.0.1.
Keep sdk-go v1.0.0 and the now-published sdk-go-sqlite v1.0.1 remote pins. All seven
platform packages and main use exact version 1.0.2. Prefer the existing GitHub
release.yml trusted-publisher flow, preserving exact tarballs and registry
integrity verification. Independently validate remote fresh installation and
actual SQL parse-error session behavior. No system-wide installation is implied.
