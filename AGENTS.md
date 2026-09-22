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
No compiled management origin is provided: TIANA_MGR_ORIGIN, then
TIANA_AUTH_ORIGIN, select the deployment. Missing origin fails before MGR requests.
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

Direct mode requires explicit TIANA_TOKEN, never opens local account/Token stores,
never logs in or calls MGR, and never mints/saves credentials. Gateway retains
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
