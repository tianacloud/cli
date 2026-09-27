# Public Tiana CLI development

Module and canonical target: github.com/tianacloud/cli. This first public source
candidate derives from the reviewed CLI baseline ade8130ee37ef6cf2b6f6b90249d1bf5912ffbfb
plus the shared-auth, SQLite-session and GitHub dependency migrations. Preserve
those behaviors. Do not publish the previous repository's Git history. No commit,
push, tag or release without user authorization. Original review copies stay intact.

## Public source and deployment configuration

Public source, documentation, binary strings and decoded fixtures must contain
no private deployment addresses or credentials. Tests use reserved example.test
hosts and synthetic secrets only. The module/import/linker paths use GitHub.
No compiled management origin is provided: only TIANA_API_ORIGIN from the
launching environment selects the deployment. Missing origin fails before MGR requests.
TLS verifies by default in source and packaging builds. Explicit debug/insecure
build settings are not release defaults and must not be distributed.
Trust roots are supplied explicitly through --ca-file or TIANA_CA_FILE.

## Authentication and persistence

sdk-go/auth provides login/refresh/logout, credential storage and InstanceToken
lookup. CLI keeps resource models, pending idempotent operations and formatting.
Maintain existing per-origin keys, credential paths/JSON, token expiry/-1 sentinel,
30s selection skew, tenant/instance/endpoint checks and TIANA_TOKEN priority.
Authorize with MGR before local token selection (INSTANCE mode). Account credentials never enter
Gateway CONNECT; InstanceTokens never enter inner SQL/Git HTTP. No implicit Token
creation, rotation or alternate-token retry. Constructors do not access real stores.
The security hardening below supersedes earlier store locking/diagnostic risks;
publication scans alone still do not establish a complete security audit.

## SQL and Git safety boundaries

sqlite shell/-e/-f use sdk-go-sqlite's exclusive Hrana v3 Session. CLI owns grammar
preflight, original SQL text, readline and ordered lossless output. Raw transaction
statements/savepoints remain on the same stream. There is no replay/reconnect.
A known active transaction is rolled back on exit; rollback and close share a
three-second total cleanup budget. Unknown responses/batons stay unknown, and
transport close does not establish rollback; server locks can remain until TTL.
Preserve input/result bounds, private exclusive output files and public exit codes.

Git is native Go, TLS 1.3/H2 CONNECT, strict hostname verification and a pre-200
barrier. Never log raw tokens or opaque Git traffic. One invocation dials once;
no replay/proxy/fallback. Honor cancellation, flow control, half-close and native
exit status. The launcher executes the sibling CLI without searching PATH.

## Legacy helper boundary and verification

Legacy connect retains its helper contract, trusted installation/hash checks,
separate control pipes and filtered environment. Do not change protocol/data
formats for this migration. Packaged helpers require their own release verification.
The public literal control vectors are a hostname-sanitized derivative with
recomputed hashes; see internal/supervisor/testdata/README.md. Do not claim they
are byte-identical to the original. Encoder/decoder/rejection tests remain active.

Use published GitHub commit dependencies and Go-generated checksums; no vendor
fork or machine-local replace. Validate GOWORK=off downloads, race/vet, source
export scans (including decoded PEM/hex), packaging default policy and native/Linux
binaries. Optional actual App/helper/deployed Gateway checks must be identified
separately. Never modify read-only specification/control/App references.

## Upstream rebase (2026-09-21)

Integrate tiana/cli main ade8130 (Git management and native release wrapper)
while retaining shared SDK authentication/SQLite sessions, public module paths,
explicit management origin, sanitized fixtures and verified TLS defaults.
Git create/list/show use published git metadata, exact-name pagination and
instance-bound creation operation receipts. Persist accepted instance IDs before
polling; keep creation and Token operations separate. Unknown outcomes preserve
pending identity, no duplicate creation/replay. Git pending uses git.create;
SQLite keeps db.create and its branch behavior. No storage format migration.
New resource requests must use sdk-go/auth DoJSON, preserving its authorization
and 401 refresh policy rather than restoring removed private auth internals.
The new release wrapper requires a matching sibling Rust sdk checkout; public
Go-only builds remain independent. All release scripts default verified TLS.
Validate upstream Git creation/recovery/list/show and wrapper regressions plus
full CLI race/vet and public source scans. Known security review findings remain
open; this rebase is not their remediation. No commit or push authorized.

## Public address scan follow-up (2026-09-21)

Public fixtures use documentation-only IPv4 addresses, not private deployment
IPs. Scan literal and decoded PEM/hex content for private hostnames and RFC1918,
shared-address and link-local IPv4. Loopback/unspecified addresses remain valid
for isolated test listeners; TEST-NET addresses are allowed for examples. CI
runs scanner positive/negative regressions before the source scan. This scan
is not a general secret detector or proof that all encodings are covered.

## Security remediation (2026-09-21)

Actual CLI module/imports are now github.com/tianacloud/cli on task branch
codex/1747aaac/cli-security-fixes, based on ade8130 plus the public candidate.
Preserve existing dirty work and source snapshots; no commit/push authorized.
The matching SDK auth security branch is required; pin a published fixed version
before any standalone release. A session-external workspace tests the combined
sources without adding machine-local replacements to product go.mod.

Management writes acquire a private persistent per-pending-file flock before
loading state and hold it through completion. Contention fails before requests.
Any unmatched existing intent (including another origin) refuses replacement.
This preserves idempotency/recovery IDs and existing JSON, avoids simultaneous
writers losing state, and never infers remote rollback from local failure.
Lock files must never be unlinked; descriptors release on crash. Only cooperating
new Linux/macOS processes participate; custom/older writers need serialization.
No new server calls, SQL replay, data-format or server durability changes.

Pending reads/writes are capped at 8 MiB; require owned regular mode0600 files
and no final symlinks/FIFOs. Private parent preparation validates the opened
owner/directory and restricts mode via descriptor; atomic same-directory rename
retains file fsync but still no directory fsync. Plaintext remote auth, unsafe
credential files and reflected peer messages are rejected by matching sdk-go.
Account refresh holds a separate file lock across reload/request/persistence;
lock ordering is pending then account, with no reverse acquisition path.

Escape controls and Unicode format characters only at management presentation
boundaries. Keep ordinary Unicode, persisted/API field values, SQL output and
intentional Token stdout unchanged. Fixed peer diagnostics preserve structured
operation metadata for recovery. Uninstaller validates all trusted ancestors
and intermediate dirs before any removal, including trailing-slash symlinks.

Require cross-origin zero-request regressions, interprocess pending/refresh
serialization, cancellation/crash release, private file/FIFO/size regressions,
terminal output safety and isolated uninstall tests. Complete full CLI/SDK race,
vet, macOS/Linux compilation and public export scans. Dependencies must be
published/pinned separately before claiming independent release validation.

## Published SDK security dependency

The user authorized committing and pushing the reviewed SDK security fixes.
SDK commit 76fdbba72a4811118df9a858a9cee7ff396379f9 is published on
codex/1747aaac/sdk-auth-security. CLI pins the corresponding pseudo-version
v0.0.0-20260921144903-76fdbba72a48, with checksums and no local replace.
This supersedes the pending-publication restriction above. Go module selection
also applies the fixed SDK to the unchanged SQLite driver dependency.
Keep CLI changes uncommitted until separately authorized. Validate with
GOWORK=off against downloaded remote sources, race/vet, module verification
and macOS/Linux builds before claiming standalone completion.

## Legacy local InstanceToken compatibility follow-up

User-reported shell failure was reproduced with pre-SDK timestamp-string
expires_at files. SDK local-file decoding now accepts these alongside Unix
seconds; lookup is read-only, while successful locked saves normalize stored
expirations without dropping existing credentials. Preserve private-file and
identity checks. Cover shell lookup with a literal legacy expiration fixture.
This follow-up SDK fix is not published yet: the delivered local bin/tiana
uses the session Go workspace and a distinct dev-token-store-compat version.
Product go.mod remains pinned to published 76fdbba pending separate publication;
GOWORK=off rebuilds do not yet include this legacy-format compatibility fix.

## Product-specific creation arguments

User explicitly removed display-name and engine flags from both Git and SQLite
create commands. Require exactly one nonblank positional NAME. SQLite sends
engine sqlite and Git sends git as code constants; no configurable or hidden
alias flags. Preserve backend display_name fields, engine availability checks,
first-token issuance, and identity validation. Use -- before names with leading
dashes or significant surrounding whitespace.

Removed flags fail during argument parsing before account/store/network work.
Do not normalize existing pending argv or reset idempotency keys: old flagged
intents require the previous compatible CLI with their original arguments. Keep
a rollback binary in the session artifacts. This is an intentional command-line
break authorized by the user, with no storage or server protocol changes and
no new hot-path allocation/network request. Test removed flags and help, exact
name/fixed engine payloads, zero remote calls/pending preservation on rejection,
and unchanged positional create recovery. Rebuild the delivered local binary.

## SQLite asynchronous creation receipts and pending recovery

A user hit a db.create pending record at step token without instance_id. MGR
returns HTTP 202 with instance_id/operation_id for SQLite as well as Git, but
the SQLite client previously decoded it as a full synchronous Instance. The
empty ID was persisted and blocked later operations.

Use validated receipt decoding for both products. Persist accepted instance ID
at step instance, observe the creation operation using scoped engine checks,
and enter token only after confirmed completion. Keep legacy synchronous
responses working. A transient 404 while awaiting accepted creation retains
pending state for both products. Reuse the existing Git 60-second bounded
waiter with an explicit product scope, preserving all identity checks.

Old incomplete records without instance ID recover only by repeating their
original create payload and idempotency key; never resolve by name to bind a
new ID, generate replacement keys, or automatically discard an intent. Preserve
existing token request/idempotency keys across receipt recovery. Creation and
token operation identities remain separate; MGR durably stores creation
operation identity, so no local schema migration is added. No real replay,
Token minting, cancellation or local pending deletion without the user's
choice about the preexisting SQLite operation.

Validate async success, interruption, running/failure/identity mismatch,
visibility delay, and old missing-ID recovery with exact key preservation and
no token write before confirmed creation; retain Git and sync SQLite tests.
This adds bounded metadata polling only for asynchronous SQLite creation;
SQL execution and database persistence semantics are unchanged.

## Deployed Git engine identifier

The current test deployment publishes engines git and sqlite. Earlier cached
specification material and CLI fixtures used app_git, causing Git creation to
fail preflight and Git list/show to reject or hide actual resources. The live
MGR catalog reported by the user is authoritative for this deployment. Fix
the Git command's hardcoded engine to git across create, scope validation,
name lookup, filtering, creation recovery and Git URL/output formatting.
No engine override or fallback to a different published engine is introduced.
Keep app_git as a rejected legacy value in boundary tests, not an alias.

Preserve pending keys/argv, product checks, token scope, async creation and
TLS. Do not rewrite pending state or retry real creates during validation.
Existing app_git deployment scripts are not auto-migrated; such environments
need a matching CLI. Test the literal deployed git/sqlite catalog and git
response metadata, including create payload, list/show, async recovery and
wrong-engine rejection. Changes do not affect storage or wire schemas beyond
selecting the correct engine value; no extra network calls or hot-path cost.

## GitHub source publication

The user authorized CLI commit and push to GitHub. Publish this reviewed source
snapshot as a new root on codex/1747aaac/cli-public-migration; never attach the
old repository history. SDK legacy expiration compatibility is now published
as 3a503ed2558557b3ac45c1205ae80725e799e446, and the CLI pins its generated
pseudo-version v0.0.0-20260921154600-3a503ed25585 with module checksums. This
supersedes the unpublished compatibility limitation above. Validate the exact
public source independently with GOWORK=off, downloaded GitHub modules, race,
vet, public-source/uninstall checks and macOS/Linux builds before publication.
No default-branch merge, tag, release or repository visibility change is implied.

## Instance deletion commands

User requested sqlite/git delete INSTANCE with default confirmation and -f
to skip it. Both use the existing MGR DELETE /api/v1/instances/{id} contract.
Resolve an ID or product-scoped exact name, reject ambiguous names/engine
mismatches, and pin the resolved immutable ID before prompting. Require a
terminal without --force; only a complete y/yes line confirms. EOF/empty/no
cancel, and context cancellation exits 130. Bound input to 64 bytes and poll
for cancellation without a reader goroutine; restore stdin descriptor flags.
Terminal metadata must pass presentation sanitization. The flag does not bypass
authentication, product checks, or existing pending-command protection.

Acquire the existing private pending lock through resolve/confirm/request; any
unresolved pending record blocks deletion unchanged. This intentionally also
serializes a human prompt with cooperating create/Token commands. No local
delete pending schema or token-store migration is introduced: MGR durably
stores deletion request/operation identity per tenant and immutable instance.
SDK auth refresh retains one stable request key and target; CLI adds no retry
loop after failure. Unknown outcomes instruct retry by ID after inspection,
never by re-resolving a possibly reused name. Receipt validation requires 202,
matching instance_id and nonempty operation_id. Report acceptance, not completed
cleanup; no polling or storage durability claims. Local Tokens remain unchanged.

Deletion applies to the whole instance and all branches; no branch flag. This
is additive CLI behavior and leaves API/storage formats unchanged. Rollback to
an earlier CLI removes these commands without altering pending records. Checks
cover real PTY confirmation/refusal/EOF/cancellation, -f with piped stdin, scoped
resolution and duplicates, auth refresh identity, failed/invalid/lost receipts,
pending preservation, race/vet and native/Linux builds. Never delete actual
instances during automated validation; use synthetic HTTP peers and stores.

## Deletion state presentation

User requested exposing deleting state after the delete command was added.
Consume MGR deletion_pending and lifecycle_state without changing server wire
semantics or stored product_state. A shared display projection serves both
SQLite/Git list (including interactive pagination) and details. DELETED product
state or fresh DELETED lifecycle takes priority over pending; otherwise pending
or fresh DELETING lifecycle displays DELETING, with all other states preserved.
Ignore lifecycle when runtime_status_stale=true; durable MGR pending still
applies. Historical deletion_operation_id alone is not evidence of progress.
Absent new fields preserve legacy presentation. Do not claim physical S3
reclamation has completed from the displayed lifecycle state.

Plain SQLite show resolves and checks the instance first, and directly prints
metadata during deletion/after deletion, avoiding dependence on a removed
default branch. Explicit branch or URL requests retain existing lookup/error
semantics. Shared shell/token branch resolution and persisted intents stay
unchanged. This is an additive read-only projection with no extra list requests
or mutations, constant per-row work, and no storage migration. Rollback only
loses progress visibility. Validate literal JSON through both product list/show
commands, deletion precedence, stale runtime, old/failed operation metadata,
legacy field omission, and deleted default branch absence; run full race/vet,
source scans and macOS/Linux builds. Preserve preexisting uncommitted delete
work; no actual instance mutation or automatic commit/push.

## Direct SQLite Endpoint selection

User requested shell --endpoint after the explicit-token direct mode was proposed.
Accept a canonical SDK Endpoint hostname with optional TCP port, or an HTTPS
URL (only empty/root path). Validate before SQL action; reject userinfo, queries,
fragments, non-HTTPS, aliases/IPs/bare IDs and invalid/empty ports. Normalize DNS
case and default TCP port 443. The canonical host remains SNI/CONNECT authority;
the optional port changes only TCP destination, not the logical CONNECT port.
INSTANCE and --branch conflict with --endpoint even if branch is explicitly empty.
The Endpoint selects its branch; no MGR product/branch resolution is available.

Direct mode prefers explicit TIANA_TOKEN; the endpoint-local selection decision
below adds saved Token lookup when it is unset. Never open account credentials,
log in, call MGR, or mint/save credentials. Gateway retains
Token/endpoint authorization and verified TLS. This additive opt-in capability
leaves account authorization before automatic local Token selection in INSTANCE
mode unchanged. The caller chooses the target; canonical DNS syntax is not a
trusted-deployment allowlist. Keep raw endpoint/Token values out of diagnostics.

Reuse the existing SDK session for interactive/piped shell, -e and -f, CA roots,
timeouts, bounded IO and output. No replay/reconnect, transport fallback, storage
migration, persistence change or new management calls/locks. Rollback removes the
flag only; existing instance-mode commands/stores remain compatible. Test real
Hrana execution over a synthetic TLS/H2 Gateway, zero resolution in direct mode,
argument conflicts, redaction, target normalization, missing/invalid Tokens,
TLS failures and refusals without retries, shell/script cleanup, and existing
instance-mode authorization regressions. Run full race/vet, source scan and
macOS/Linux builds. Actual deployment acceptance remains separately verified.

## Built-in Go connect helper

User explicitly requested implementing tiana-helper inside the CLI to fix ordinary
Go builds failing connect. This supersedes the Rust-only helper requirement above,
while retaining the independent helper process and frozen contract-3 framed pipes.
BuiltinHelperLauncher re-executes the current executable (Linux /proc/self/exe),
never a PATH helper. Embedded release assets retain their existing verified path.
Normal builds use mode=builtin instead of requiring a trusted installation manifest;
connect performs the private HELLO exchange before accepting native traffic.
The built-in helper owns database bytes; the supervisor still handles only control.

Maintain HELLO/config/credential/BOUND/READY/CHILD_STARTED/SERVING ordering. Bind
loopback only and accept only after child identity handoff; report honestly as
loopback_unisolated. Linux validates starttime around pidfd_open and monitors the
pidfd, requiring kernel support; macOS requires the existing zero identity and
uses kqueue process-exit notifications. Parent pipe EOF, owner exit and drain stop
accepts and cancel all sessions. On owner exit, keep the control pipe until the
supervisor sends DRAIN/EOF so final cleanup cannot race an EPIPE. Never inherit outer Tokens/control pipes into the
native client. Credential frames and bounded byte buffers are cleared; sdk-go's
immutable Token strings may remain in helper memory until process exit (Go does
not guarantee allocator zeroization). Helper stderr and raw peer messages stay
out of terminal diagnostics. No credentials are persisted by connect.

Reuse published sdk-go for verified TLS 1.3/H2 CONNECT; no insecure fallback. An
insecure legacy helper config is explicitly rejected by built-in mode. Optional
embedded Rust builds retain their separate verification/policy. Classifier limits
stay 16 KiB headers, 64 fields, one second and at most 64 KiB retained before 200.
Support existing Hrana v2 pipeline/WebSocket plus App v3 pipeline/cursor, preserving
opaque bytes. Explicit profile must match. Maximum 32 active local sessions with
bounded copy buffers; overflow connections close. No request/SQL replay on failure.
CONNECT errors marked committed by SDK must remain after-200/unknown outcomes.
Keep native exit status and distinguish local closure from upstream interruption.

No wire/dependency/storage migration or server durability change. Rollback only
loses built-in helper availability; existing embedded helper and private-control
vectors stay compatible. Verify real pipes/processes, Token filtering, HELLO/state
rejections, prefix integrity/pre-200 barrier, HTTP/WebSocket, half-close, TLS and
Gateway failures, drain/EOF/owner cleanup, resource bounds, full race/vet and both
platform builds. Actual Turso/deployed Gateway acceptance is separate from fake
native/synthetic Gateway verification. Do not publish private-history sources.


## Endpoint-local InstanceToken selection

The user requested direct Endpoint access with a locally saved InstanceToken.
Explicit TIANA_TOKEN (including empty/invalid values) remains authoritative;
only an unset variable enables lookup. Match the normalized endpoint_id exactly.
Restrict to configured DefaultOrigin when present; otherwise require one unique
origin/tenant/instance scope across matching records, including expired records.
Never guess across scopes. Direct mode is explicitly user-directed; no MGR
account authorization is performed and hostname syntax does not verify deployment
ownership. Gateway authentication and verified TLS remain mandatory.

CLI authclient reads a bounded (8 MiB), owned regular mode-0600 nonsymlink file
as an identity index, validates storage keys, then delegates credential decoding,
legacy expiry compatibility, 30-second expiry skew and newest selection to the
published SDK. This avoids an unpublished SDK dependency or duplicated expiry
logic. Two bounded reads add local I/O; pin and recheck origin/tenant/instance/
endpoint after SDK lookup so concurrent atomic replacement cannot change scope.
No store writes, locks, migrations or network fallback. Errors omit secrets and
raw input. The on-disk index shape is a compatibility dependency covered by tests.
Instance mode retains its existing MGR authorization before lookup.

Rollback removes only the fallback; existing stored credentials remain unchanged.
Verify explicit precedence, unset/empty/invalid inputs, ambiguous scopes, origin
filtering, expired/legacy records, corrupt identity, unsafe files, zero MGR calls,
read-only lookup, full race/vet and macOS/Linux builds. No live deployment writes.


## Git remote-helper local InstanceTokens

User requested the same endpoint_id-based local Token selection for native Git.
Keep TIANA_TOKEN and TIANA_TOKEN_FILE mutually exclusive and authoritative,
including empty/invalid explicit inputs. Only when both are unset, resolve the
SDK default instance-tokens.json path (or TIANA_INSTANCE_TOKENS_FILE) and call the
shared authclient.LookupEndpointToken with repo.ID and optional DefaultOrigin.
This reuses SQLite's bounded private-file read, identity checks, scope uniqueness,
legacy expiry decoding, newest usable selection and second-read scope validation.
No new file format, SDK version, dependency, MGR call, login, write or lock.

Missing/no-usable saved records preserve the existing auth-disabled Endpoint
capability by dialing without a Token once; Gateway decides access. Store errors,
ambiguous scopes and invalid selected Tokens fail before dialing. No retry after
rejection and no secret diagnostics. The canonical remote remains the TLS/SNI
identity; physical address override never affects Token selection. Users choose
the deployment; endpoint_id is not a domain trust allowlist. Git wire format,
pre-200 barrier, streaming/backpressure and half-close remain unchanged. Small
bounded local reads occur once per helper connection, outside the relay hot path.

Rollback removes fallback only and leaves credential stores untouched. Tests
must isolate local paths, verify explicit precedence/no fallback, default path,
missing/expired/mismatched records, ambiguity/unsafe data and read-only behavior.
Run actual compiled CLI/launcher + native Git over synthetic TLS/H2 Gateway to
verify the saved secret in CONNECT and explicit overrides; then full race/vet,
public-source scan and macOS/Linux builds. No live repository mutation is needed.


## Connect local InstanceToken selection

User requested the native Turso connect flow also read instance-tokens.json.
After adapter/Endpoint and Gateway trust validation, but before helper launch,
resolve default credentials in this order: explicit TIANA_TOKEN; shared
endpoint_id-based local lookup; then the existing hidden TTY prompt only when
no usable saved record exists. Non-interactive absence is an actionable error.
An invalid/empty explicit source never falls back; legacy internal explicit
source APIs retain their existing behavior. CLI still rejects removed Token flags.
Fix missing-credential guidance so it no longer recommends those removed flags.

Use authclient.LookupEndpointToken with the original resolved Endpoint ID,
TIANA_INSTANCE_TOKENS_FILE/default SDK path and optional DefaultOrigin. Reuse all
scope, expiry, file limits/permissions, legacy decoding and atomic-replacement
checks. No account login, MGR requests, token issuance, writes, migrations or
retries. Ambiguity, unsafe/corrupt storage or invalid selected Token fails before
helper/child creation and must not prompt. Preserve native stdin and hidden-input
terminal restoration; secret bytes use SecretToken and existing private pipes.
Original hostname remains TLS identity even with an explicit TCP address override.
The prompt fallback preserves existing missing-credential behavior; store errors
now fail closed instead of being ignored. Lookup adds bounded local reads outside
the forwarding hot path. Rollback changes selection only; stores remain compatible.

Validate local/default path, explicit precedence including empty/invalid values,
missing/expired/out-of-scope records, ambiguity and file errors, untouched stdin,
and read-only behavior. Exercise missing-store prompt cancellation through a PTY,
full CLI with built-in helper, actual Turso + compiled helper over synthetic TLS
Gateway, full race/vet/source scan and macOS/Linux builds. Preserve all earlier
uncommitted work; no real deployment access or commit/push required.


## Removal of the installation verification command

User requested removing verify-install. Remove its CLI registration, dedicated
probe and success/help test. Reject the old command and help path as unknown;
normal connect still performs HELLO negotiation and all existing helper trust,
version, TLS and lifecycle checks. No credential or data-plane contract changes.
Release/install scripts must not invoke the removed command: retain checksums,
architecture checks and --version startup smoke, with helper negotiation exercised
by connect and integration tests. The version smoke alone does not prove a helper
session works. Documentation uses --version/--help for inspecting the CLI.

This is a deliberate public CLI removal; external automation must stop calling
verify-install. Rollback restores the command and scripts with no persisted-state
migration. Verify command rejection, help absence, existing connect tests, shell
script syntax and packaging regressions; run full race/vet, public-source scan
and macOS/Linux builds. Do not alter previous token command rollback or publish
without a new explicit request.


## Account status and tenant quota

User requested replacing whoami with status. Remove the old CLI command; retain
SDK Whoami solely as the account API. status uses noninteractive SDK auth/refresh,
never starts browser login, and requests the authenticated tenant usage summary
at GET /api/v1/usage?page=1&page_size=1 after successful account verification.
The tenant comes from the server session; no tenant override or instance-count
inference from the detail page. Latest MGR main db947c3 routes/application and
usage projection were checked read-only; decimal-string uint64 counters and
Unix-millisecond timestamps follow the contracts MGRUsage projection. No private
module import or new SDK dependency. The local knowledge checkout is not edited.

Print available account email/name/username and compute/storage-byte/instance used and
limit, UTC period, blocked/reason and last update. Preserve uint64 precision and
zero limits; nullable storage/update values render unknown. Reject missing required
counters/limits rather than pretending they are zero. Decode only summary fields,
ignore detail pages and secrets, and apply terminal escaping to server strings.
No data-plane access, resource mutation, usage reset or forced recalculation is
requested. SDK refresh may update account credentials; InstanceTokens and pending
operation files are not read. Existing MGR usage reads may queue a server-side
quota check for blocked tenants; status makes no explicit mutation request.

Missing login prints Not signed in plus login guidance, exit 1. Account/network
errors do not become a false logged-out claim. Quota failure retains retrieved
account information, prints Quota: unavailable and exits 1; quota blockage itself
is a successfully retrieved status (exit 0). This handles older servers without
the usage route honestly. Standard --ca-file/TIANA_CA_FILE applies to both reads.
No persistent format changes; rollback restores whoami and removes status.
Validate canonical/removed help, no automatic login, partial failures/redaction,
null vs zero, uint64 maximum, blocked state, malformed data and custom CA, plus
full race/vet/source scans and macOS/Linux builds. Preserve pending verify-install
removal and the prior token/list rollback. No commit/push without user request.

User-requested display refinement: omit Signed in as, User ID, Management and
Tenant lines from status. Keep account validation and tenant-scoped quota requests
unchanged; test both successful and partial-failure output for omitted fields.

## SQLite branch mutations

Rename branches to branch without an alias, preserving list pagination/search.
Create INSTANCE NAME [--parent NAME] chooses main by immutable ID when omitted;
explicit parent and delete names use one exact indexed query. Delete --by-id
never falls back to a name. Verify instance engine, detail instance ID/branch ID,
and exact name before pinning an immutable mutation target. Reject empty/unsafe
path IDs and root/protected deletes. --force skips only terminal confirmation.

Use SDK DoJSON with a stable per-request key, POST branches/{parent}/children
with {name, ttl_seconds:null}, or DELETE branches/{id}. Require HTTP 202, matching
instance ID and nonempty operation ID. These receipts mean accepted, not complete.
MGR/Control branch contracts were checked read-only: MGR reserves child quota,
Control assigns the child identity. Do not claim branch creation deduplication:
no automatic transport/5xx retry, polling, or local resume record; SDK may refresh
once after explicit 401 with the same target/body/key. Unknown creation outcomes
require checking branch list before any manual retry. Delete retries should use
original IDs, never a freshly resolved reused name. Keep existing pending lock
and refuse any outstanding intent without changing it. No persistence schema
change or local InstanceToken access; parent data, references, atomicity and
cleanup remain owned by Control. No cascading delete or protection override.

Regression checks cover exact targets, parent/default selection, product scope,
protected/root rejection, private errors, bad receipts, no retry on 5xx, pending
conflicts, terminal yes/no/cancel and nonterminal refusal; full race/vet and builds.
Preserve all prior uncommitted status/verify-install changes. No commit/push.

## Stateless connection credentials and asynchronous instance creation

This decision supersedes earlier local InstanceToken selection/cache instructions.
The CLI must never create, load, maintain or remove instance-tokens.json, including
paths supplied by the obsolete TIANA_INSTANCE_TOKENS_FILE variable. Existing files
are left untouched. Explicit sqlite tokens create delivers the secret only on
stdout; it retains secret-free operation recovery but performs no local Token save.

SQLite shell (instance and direct endpoint), connect and Git remote-helper share
one credential resolver. TIANA_TOKEN and TIANA_TOKEN_FILE are mutually exclusive
explicit overrides. Presence, including an empty value, prevents account fallback;
invalid/unsafe input fails closed. Otherwise read credentials.json for the selected
management origin (TIANA_API_ORIGIN) with the existing SDK
account store. A valid unexpired access token is required; no connection-triggered
login/refresh, candidate request, anonymous retry or credential prompt is allowed.
Explicit files are owned regular 0600, no symlinks/special files, at most 512 token
bytes plus one LF/CRLF. The 512-byte limit matches helper control-frame decoding.
Missing/expired account sessions instruct users to run tiana login. Existing login,
management refresh, origin isolation and credential-file security remain intact.
The account token is sent only as the outer Gateway CONNECT credential; native
client argv/env and SQL must never contain it. TLS verification is unchanged.

Both sqlite/git create return after the accepted creation receipt; do not issue a
first Token, inspect endpoints or poll jobs. Persist the request identity before
POST and instance/operation receipt before stdout. On unknown outcomes reuse the
original request ID; on output failure recover the receipt without another POST.
Add optional creation_operation_id to the existing secret-free pending format,
separate from the legacy Token operation_id. Delete the pending record only after
successful receipt output and check cleanup errors. Preserve records belonging to
another command, origin or account. Old create records in the Token step or with
an unresolved Token operation are refused unchanged for manual investigation,
not silently discarded or resumed with another Token request. Rollback must not
resume an unresolved new creation record in an older auto-Token-issuing binary.

Account fallback deliberately performs bounded local reads only, avoiding network
latency/refresh writes before CONNECT. The tradeoff is that an expired session
requires explicit login. Gateway retains authorization/revocation enforcement;
users must choose a trusted deployment Endpoint. No automatic legacy-file deletion
or cache migration occurs. SDK public cache APIs are unchanged; CLI no longer calls
them. Verify explicit precedence and failure, origin separation, missing/expired
sessions, file security, unchanged legacy/account files, no credential leak, no
create polling/Token request, stable unknown-outcome retries and receipt recovery.
Run race tests, vet, source scan, macOS/Linux builds and available native fixtures.

### Queued creation receipts (2026-09-23)

MGR can acknowledge a durable instance-create job before Control assigns an
operation ID. Its HTTP 202 response contains instance_id, positive uint64 job_id,
and an empty/omitted operation_id. Requiring operation_id incorrectly reported
failure after resource reservation. Accept a nonempty instance_id plus either a
positive job_id or a nonempty operation_id; reject missing trackers and invalid
job types/ranges. Preserve legacy operation-only and synchronous Instance replies.

Map receipt job_id to Instance.CurrentJobID and persist it separately as optional
creation_job_id, never as the legacy Token job_id (which indicates an unresolved
Token step). Print job and/or operation identities exactly, without polling or
Token creation. A persisted job-only receipt is complete enough for local output
recovery: do not query metadata or resubmit POST. Existing records left by the
old parser keep their original request_id; retry the identical command so MGR's
tenant-scoped idempotency returns the same instance, not a name-based lookup/new
request. Never delete an unknown-outcome record to force creation. Preserve all
prior stateless-token work; no commit/push without authorization.

Verify MGR-compatible queued/submitted/legacy formats, exact uint64 values,
invalid/missing IDs, both products, stdout failure recovery without extra network
requests, and old rejected-receipt recovery using the original request identity.
No MGR/Control data or schema changes; one optional field in CLI's existing atomic
pending store. Older CLI binaries do not understand this job-only receipt; retain
the updated CLI until its pending receipt is delivered. No additional network
calls, retry loop or persistent secret is introduced.

### Opt-in create waiting (2026-09-23)

Git/SQLite create accept -w/--wait; default remains asynchronous. Exclude only
these flags before -- from saved command args, preserving the positional separator
and literal names. Flag spelling/position does not change a mutation's identity;
interrupted preexisting async receipts may be resumed with --wait. After a receipt
was successfully delivered and cleared, another create is a new request, not a
name-based resume. Never infer identity from display name.

Poll GET /api/v1/jobs/{creation_job_id} once per second using SDK account auth and
cancellable timers. No total wait limit; Ctrl-C exits 130 without cancelling the
server job. Bind every returned job to its ID, instance, instance_create kind and
original request ID. Fail stops with nonzero status; no automatic retry mutation.
Current MGR transactionally removes successful jobs, so 404 requires reading the
original immutable instance and verifying engine, matching known operation ID and
ACTIVE product state with no deletion underway. Complete/retry_completed also
requires this check. Pending/unknown product state is not success; missing or
malformed resources and API errors fail closed. Legacy no-job receipts are checked
once: only an already ACTIVE instance is success, otherwise --wait is unsupported.
Never request a Token or emit raw peer error details.

Keep the existing atomic pending receipt and exclusive lock until final successful
output; polling failures, interruption and output errors preserve recovery IDs.
The lock serializes other local mutations for the duration of waiting, avoiding
receipt overwrite; read-only commands remain usable. This introduces no persistent
schema change beyond the preceding creation_job_id field. Repeating without --wait
can acknowledge/clear an accepted receipt without claiming provisioning success.
Rollback restores asynchronous behavior while preserving receipt interpretation.

Verify flags/alias/false/boundaries, default no polling, pending→running→removed or
complete→ACTIVE, terminal fail, missing/mismatched/unknown replies, uint64 job IDs,
API error, timer/HTTP cancellation and resume without another POST or Token call.
Run full race/vet/scans/builds and binary help smoke. Prior stateless-token and
job-receipt fixes remain uncommitted; no commit/push is authorized by this task.

### Remove SQLite Token command (2026-09-23)

User explicitly requires complete removal without Token-command compatibility.
Remove sqlite tokens and its handler/flow, issuing/query API wrappers, Token result
and request models, expiration parsing/formatting, Token-only request ID generation,
and all dedicated legacy pending fields/step checks/recovery guidance. Do not add
a token alias or compatibility adapter. Keep Job/GetJob for instance create --wait
in a separate job.go. Pending state contains only instance-creation request and
receipt fields; no Token issuing or recovery state remains.

Keep explicit TIANA_TOKEN/TIANA_TOKEN_FILE and account access-token connection auth,
Git/SQLite create --wait and branch operations. Keep generic prevention of replacing
an unrelated pending mutation, without interpreting or migrating old Token records.
No automatic user-file deletion or server-side token revocation is part of command
removal. Old issuing commands/configuration are unsupported; rollback requires the
prior source, not a new compatibility layer.

Verify removed commands reject without requests/writes, no Token-specific symbols
or routes remain in production code, unsupported pending commands fail generically,
and create recovery/wait/branch/shell regression tests pass. Full race/vet/scans,
macOS/Linux builds and working-binary help checks; no global install/commit/push.


### SQLite branch creation waiting (2026-09-23)

Add -w/--wait to branch create only; default asynchronous acceptance and branch
mutation identity semantics remain unchanged. MGR forwards branch creation to
Control and returns operation_id, not an instance-create job_id. Query the existing
instance operation API every second, using SDK account auth, bound to the receipt's
instance/operation, CREATE_BRANCH kind and immutable parent. Pending/running/
retry_wait continue; success requires a valid child identity distinct from its
parent. Failed, malformed/unknown state, 404 and transport/API errors fail closed;
do not infer success from a branch name or replay mutation. Keep IDs as strings
without floating-point conversion. Peer result/error details are not printed.

No total timeout. Cancellable timers and requests make Ctrl-C exit 130 without
cancelling the remote operation. Print the receipt before polling and include its
identities on observation failure. Branch create remains non-resumable with no
new local persistence or server idempotency guarantee; document that re-running
create is not a safe resume. Existing pending lock remains held while waiting,
serializing local writes but leaving reads available. No data-plane, storage,
SDK dependency or server changes. Rollback removes the flag only. Preserve all
prior uncommitted work; do not commit/push without explicit authorization.

Validate default/false/short/long flags, custom parent, pending/running/retry_wait
through success, failed/missing/malformed/mismatched operations, HTTP and timer
cancellation, zero automatic mutation replay, rejected delete wait, full race/vet,
public-source scanning and native/Linux builds. Real deployment is a separate check.


### Deletion waiting (2026-09-23)

Git/SQLite instance delete and SQLite branch delete accept -w/--wait; this
supersedes the earlier branch-delete wait restriction. Default asynchronous
behavior, terminal confirmation, -f, protected/default-branch rejection and
product/immutable-target validation remain. Use the existing accepted operation
receipt and public operation API, not a new mutation, job or name lookup.
Poll once per second, binding instance ID, operation ID, DELETE_INSTANCE or
DELETE_BRANCH kind and, for a branch, its immutable ID. Pending/running/retry_wait
continue; only success completes; failed, 404, malformed/unknown states and other
API errors stop with nonzero status. Never infer success from absence. Print the
receipt before observation and fail if receipt/completion output cannot be written.

Cancellation exits 130 without cancelling server work; no total timeout. Preserve
the existing mutation lock while waiting, serializing local writes but allowing
reads. No new pending format, local recovery file, automatic DELETE replay, SQL
or data-plane changes. This waits for the server deletion operation; separate
reclamation operations may continue, so do not claim physical storage reclamation.
No server/SDK changes; rollback removes opt-in flags, with no persistence migration.

Test all three commands' aliases/false/default, confirmation requirements, bound
operation/branch identities, failure/unknown/404/malformed replies, progress states,
HTTP and timer cancellation, no mutation replay and stdout failure. Retain prior
creation waiting tests and run full race/vet/source scan/native and Linux builds.
Previous uncommitted changes and pending timestamp request remain untouched.


### Instance creation descriptions (2026-09-24)

Git/SQLite instance create accept -m/--message, mapped verbatim to existing MGR
notes. Omitted or empty means no description; valid UTF-8 up to 2048 bytes follows
MGR validation. Reject malformed/oversized values before auth, state or network
work. Preserve spaces, newlines and leading-dash values. No engine/name semantics,
server contract, SDK dependency or persistent schema changes; branch create is
outside this instance-description feature.

Descriptions are part of the existing pending argv and thus creation identity.
Strip only actual -w/--wait observation options, never description values that
look like those flags or --. Do not change the payload under a saved request ID.
Retries retain the original description options; altered descriptions cannot
replace an unresolved intent. Name-trimming checks must skip message values.
No extra management requests or token behavior changes. Existing private pending
storage protects recovery data; descriptions are user metadata, not secrets.
Rollback requires finishing pending commands with their original option syntax.

Verify both products, short/long/equals forms, omitted/empty/Unicode/whitespace,
byte boundaries and malformed UTF-8; zero requests/writes on invalid values;
unknown-response retries preserve notes/request ID; changed notes are blocked;
wait-like values and -- separator do not corrupt identity. Retain creation/wait
regressions, full race/vet, public scans and macOS/Linux builds. No commit/push
unless separately authorized.

## Branch creation options (2026-09-24)

- CLI accepts -m/--message as notes (valid UTF-8, <=2048 bytes, unchanged), --ttl as integer seconds in 1..2592000 and -ts/--timestamp as uint64 Unix seconds. Omission is nil; timestamp 0 is explicit history; TTL 0 is invalid. Invalid flags fail before authentication/network mutation.
- The typed request preserves options through parent resolution and wait; flag values are excluded from positional whitespace checks. No extra local persistence, token creation, retry or mutation is introduced.
- Server lifetime starts at successful branch publication. Historical failure is terminal and must never select latest data. Requires coordinated server support; only that server can validate retained history. Existing async/default, confirmation and wait semantics remain.
- Tests cover exact JSON, nil vs zero, limits, Unicode/whitespace, malformed flags, parent/wait combination and default requests. Build with released public SDK dependencies; no private contracts dependency in CLI.

## Quota progress display (2026-09-24)

- Status preserves exact uint64 USED/LIMIT numbers and adds percentage and a static 20-cell Unicode bar (█ used, ░ remaining) for compute/storage/instances. Compute the ratio with bounded-size math/big values (three rows per invocation) to avoid overflow and float64 precision loss. Round percentages to one decimal, preserving nonzero and below/above-limit distinctions. Floor exact fractions for filled cells, capped at 20, never truncate over-quota percentages.
- MGR zero limits are real zero quotas, not unlimited; preserve 0 and show n/a percentage/no bar. Unknown usage stays unknown/no bar. No inferred quota blocking; retain the server's blocked/reason fields. No extra requests, account fields, ANSI escapes, local state or backend changes.
- Preserve prior uncommitted branch creation work. Verify status request/output integration, zero/nil, uint64 maxima, small/nonintegral/over-limit ratios, output errors, full CLI tests/race/vet and public scan/build. Rollback affects presentation only; no data migration.


## Launcher-owned management origin (2026-09-26)

The CLI and skills must not independently route to a deployment. The user
explicitly removed TIANA_AUTH_ORIGIN and the CLI --config option. Read only
TIANA_API_ORIGIN from the launching Agent/shell environment; do not delegate to
older SDK DefaultOrigin implementations that accept the removed alias. Pass the
resolved origin explicitly to SDK clients and credential stores. This keeps
native Git helpers and direct CLI calls on the same environment-selected account
without changing process environment or requiring a new SDK release.

Retain unique saved HTTPS-origin discovery when TIANA_API_ORIGIN is unset;
explicit empty input and multiple saved origins still fail closed. The removed
alias is ignored, including when only that alias is set. A CLI --config argument
is now an unknown option; native client arguments after connect -- remain native
arguments. Retain CA flags/environment, TLS validation and saved-origin validation.
No credential-file migration, token changes, network retries or storage writes
are added. Existing launchers must move the old alias to TIANA_API_ORIGIN and
remove --config arguments before using this build. Rollback only restores the
previous CLI interface; account and pending-login formats stay compatible.

Verify alias rejection with missing/empty/explicit/unique/multiple origins,
account selection, native argument passthrough, removed-option rejection before
requests, existing login/SQL/Git/preview flows, race/vet and native/Linux builds.


## API origin and web command naming (2026-09-27)

User requires TIANA_API_ORIGIN, Products help grouping and web instead of apps,
with matching agent-skills guidance and no old-name compatibility. This supersedes
older naming notes. Only the new environment variable selects explicit routing;
TIANA_MGR_ORIGIN and TIANA_AUTH_ORIGIN are ignored. Preserve unique saved-origin
lookup when the new variable is absent, explicit-empty failure, TLS policy and
per-origin account selection. Resolve in CLI and pass the origin explicitly to
SDKs rather than adopting their older environment defaults.

The web command retains serve/create/upload/status and argument semantics. The
old apps spelling is rejected before resource execution; error recovery hints
must use web. Products groups web/sqlite/git only. Do not rename MGR routes,
artifact project IDs, idempotency keys, object keys, credential files or stored
origin keys: this is a CLI surface migration, not a persistence/protocol migration.
No extra requests, retries, locks, token handling or hot-path work is introduced.

Launchers/scripts must use the new environment/command spelling. Previously saved
accounts for the same origin remain valid; no login/file rewrite is required just
for the rename. Rollback needs the corresponding launcher/skill spelling change.
Verify old-name rejection, API selection over legacy values, missing/empty/saved/
ambiguous origins, status/web/native Git account selection, unchanged HTTP routes,
help output, full race/vet, public-source scan, packaging and native builds.


## Server-generated Web identity (2026-09-27)

User requires Web to follow SQLite/Git: create takes NAME; ID is generated only
by MGR (`web-` + 18 random bytes in unpadded base64url), and public Web project
and version metadata use `id`. CLI upload/status take positional ID; no old
--project/--name creation aliases. Manifest app_id must be the returned ID.
Display names may repeat and never select resource identity.

Creation is POST /api/v1/web-projects with name and request_id. Scope idempotency
to tenant + principal + request, persist in the same MySQL row under a unique
index, and return the original ID across concurrency/restart/response loss. A
changed name under a reused request conflicts. CLI uses the shared locked pending
store, binds to origin/user/tenant, persists before sending, retains uncertain
results and output failures, and clears after confirmed output. Skills must save
data.id before constructing the manifest and reuse it instead of recreating.

MGR schema 24 adds nullable request_id + unique index to mgr_web_projects; keep
physical project_id columns, existing IDs and object paths to protect stored data.
23→24 migration checks DDL then CASes schema version under the existing advisory
lock. Request identities live as long as their resources. Cost is one bounded
unique index per resource, no new hot-path table scans. No Control change or
production migration/deployment is included. Public naming change requires
coordinated CLI/MGR/Console/skill rollout. Rollback requires matching clients and
reviewed schema rollback; never rewrite IDs or drop request recovery silently.

Verify ID entropy/prefix, rejected caller IDs/old routes, isolated ownership,
concurrent replay/name conflict, lost-response recovery, durable MySQL migration,
CLI output/pending preservation, Console rendering, and all skill validators.


## Web listing and deletion work in progress (2026-09-27)

List uses the existing owner-scoped MGR cursor API. Plain output is ID/NAME;
interactive terminals page, scripts/JSON collect pages. Bind origin/user/tenant
through traversal, reject malformed/nonadvancing cursors and foreign records,
cap traversal at 10000 pages. Exact-name resolution must inspect all pages and
reject duplicates, never choose the first match. No new storage or Control API.
Validate multi-page output, terminal quit, bad cursors, and identity isolation.

User subsequently chose A; the approved deletion contract is recorded below.


## Approved Web deletion — project lifecycle (2026-09-27)

User approved deleting all Web versions/origin files while retaining SQLite/Git,
and merging deletion state into mgr_web_projects. Retain request_id creation
idempotency. Only add state (active/deleting/deleted), delete_requested_at and
deleted_at, plus index(state,tenant_id,project_id). Do not add a separate deletion
table or delete_storage_namespace/delete_error_code/delete_next_attempt_at fields.

Mark under the project row lock shared with version creation. Reject any unfinished
upload without side effects; incomplete CLI creation blocks deletion. Active-only
reads and publication prevent reusing deleted IDs. Owner-scoped receipts remain
on project rows; previously accepted create requests cannot resurrect them.

Every minute, cleanup visits <=10 projects within 20 seconds. An in-memory cursor
prevents failed rows starving later ones; restart needs only durable deleting
state. Each project uses a <=5-second transaction holding its row FOR UPDATE SKIP
LOCKED through bounded OSS I/O and metadata commit. This consumes a DB connection
and can delay mutation of that deleting row, but avoids stored leases and skips
competing workers. No new Actor Call/Monitor API. Cancellation/crash rolls back
SQL; OSS deletion is idempotent. Preserve <=500 object/metadata batch bounds.

Check existing version storage_namespace before every cleanup batch, preserving
version records until objects are cleared. Empty apps need no OSS access. Errors
remain in maintenance logs with resource identity; state stays deleting for a
later pass. No persisted error code, per-row retry schedule, upload grace or daily
sweep. CLI --wait polls state until completion/cancellation; it cannot present a
stored cleanup failure. Origin cleanup cannot revoke issued URLs/cached copies.

Schema head remains 26: revise the unpublished 24→25 migration to add lifecycle
columns/index, preserving 23→24 request identity and 25→26 description. Validate
partial DDL and defaults/index before version CAS. No compatibility for discarded
experimental deletion-table layouts, and no production migration or table drop.
Preserve previous work on codex/a1a6f054/web-project-deletion; no commit/push/release.

Verify real MySQL migration recovery/drift, state defaults, missing forbidden
fields/table, upload/delete serialization, worker exclusion/cancellation/restart,
storage mismatch, empty app handling, bounded version cleanup, fair traversal,
terminal receipts and CLI recovery. Run full MGR tests/race/vet and skills checks.


## Web creation description (2026-09-27)

User requested `web create -m` for application descriptions. Implement optional
`--description` / `-m`, default empty, max 1024 UTF-8 bytes; preserve whitespace,
line breaks and tabs, reject invalid UTF-8/other control characters. Store only
project metadata; do not change ID generation, routing or manifests. Create/list/
detail JSON expose description; plain list keeps its existing ID/NAME format.

Creation intent includes the exact description. The same tenant/owner/request ID
must match name and description; conflicts never overwrite metadata or mint a
replacement request. Verify the returned description before clearing intent.

MGR schema 26 adds a non-null varchar(1024) with empty default to existing rows.
Validate additive DDL under the schema lock, CAS 25→26 and read back; recovery
from partial DDL must be idempotent and drift must fail closed. No new index,
extra read round trip or Control/storage-object contract. Cost is a bounded
metadata field per app. Retain prior work on the new task branch
codex/a1a6f054/web-description. Coordinate MGR schema/service/CLI rollout; rollback
must not discard descriptions. No commit/push/production migration authorized.

Test empty/Unicode/multiline/max-byte/invalid inputs, exact receipt validation,
lost-response retry and pending-description conflicts, MySQL persistence/list/
detail and migration crash/drift, then full race/vet and skills validation.

## Preview and hosted account provider (2026-09-27)

User requested saved CLI-login preview and matching hosted window.tiana.auth.
CLI uses a one-use local launch capability to reuse its SDK-managed account;
refresh secrets stay in the CLI. Hosted MGR uses the existing HttpOnly WEB session
as renewal authority for short-lived tenant data grants; the connection POST body
is {} with current CSRF and exact Origin. No instance token fallback or browser
refresh token storage. Share provider calls, wait for data-plane synchronization,
isolate cancellation, recover lost lookups but never replay SQL. Trusted app code
has tenant scope. Keep embedded bootstrap.js byte-identical across CLI and Web.
No schema or Control changes. Web/MGR rollout must be coordinated; old hosted
request bodies are rejected. No commit/push/deployment in this task. Verify local
launch capability boundaries, hosted provider browser fixtures, full Go race/vet,
frontend build and local preview; fixtures alone do not establish live hosting.


## Immutable Web source association (2026-09-27)

The application detail page had no persisted source association despite source
being pushed to Tiana Git. Add optional paired git_instance_id/source_commit to
schema-1 CSR manifests. Git ID must identify a git- resource; commit is full
lowercase SHA-1 (40 hex) or SHA-256 (64 hex). Both omitted means no association.
CLI preserves both through upload/receipt verification; MGR stores them in the
existing release manifest JSON and includes them in its immutable fingerprint.
No table, column, index or schema-version change. Existing releases are not edited;
changing or adding provenance requires a new version. Console displays current
and historical version-specific repository links and commits.

At PUT time, MGR makes one indexed tenant-scoped product metadata read, requiring
an active Git instance not pending deletion. No Control/Actor Call/Monitor or Git
object read/grant is added. This is point-in-time validation, not a retention lock:
a repository may later be deleted without erasing historical provenance. The
publisher asserts the commit; skills verify the remote version ref before upload.
This metadata is not cryptographic proof that binaries were built from that commit.
No tokens, arbitrary remote URLs or new permissions are stored or exposed.

Persist the repository ID in source configuration, derive commit from clean HEAD
into ignored build output after committing; never create a self-referential source
commit. Preserve prior working changes. Build/receipt validation, immutable replay,
tenant isolation, typed-nil/unavailable validation, JSON persistence, browser links
and responsive layout require tests. Publish CLI/MGR/Console together before using
new manifests; old strict decoders reject them. Rollback must keep new manifest
fields and fingerprints readable and must not rewrite published versions. No
commit, push or service deployment is authorized by this implementation note.


## Coordinated main publication (2026-09-27)

The user explicitly authorized commit and push to main for cli, agent-skills,
mgr, web and serverless-js, followed by Gaia deployment of MGR and Web. This
supersedes earlier implementation-only publication restrictions for the pending
changes in this session. Preserve all already-reviewed pending work; integrate
remote main without rewriting history. No npm release/version bump is implied.
The paired source association requires the matching client/server rollout and a
new immutable application version. Existing deployment settings and schema 26
remain unchanged. Verify remote commit identities, clean source image provenance,
Gaia success/IN_SYNC and live readiness/auth boundaries after deployment.
