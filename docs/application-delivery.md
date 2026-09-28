# Application delivery and npm packaging

This branch consolidates application preview/publication and npm/chat login on
the current GitHub CLI. It preserves native Go SQLite/Git/connect, account-token
connections, asynchronous creation with optional waiting, and explicit deployment
origin/CA configuration. It does not reintroduce the old cloud command aliases,
`verify-install`, a Rust SQL executor, or the local instance-token cache.

## Application workflow

Use the matching Tiana Skill for the fixed index template, hash routing and
`tiana.app.json`. Business modules mount themselves and obtain an ephemeral
connection through `window.tiana.connection()`; the Bootstrap loading indicator
is isolated in a shadow root and removed after load. No persistent platform bar
or business framework requirement is added.

```sh
tiana status
tiana sqlite create billing --wait
tiana git create billing-source --wait
tiana app create Billing -m "Team billing dashboard" --json
# Save data.app_id as APP_ID; set manifest app_id to APP_ID before building dist.
tiana app serve --dir dist --port 4174
tiana app upload APP_ID --version release-1 --dir dist --json
tiana app status APP_ID --version release-1 --json
```

Record the real instance IDs and Git remote URL. Push the matching source commit
to the user's Tiana Git before publishing its build. Reuse immutable version IDs
only with identical artifacts. Publication pins the account identity and does not
replay a management write after a rejected credential or uncertain response.
Upload links carry object scope and required private ACL/public-resource tags;
management credentials are never forwarded to object storage. Only a confirmed
published descriptor is reported as a hosted application URL under `/apps/ID`.

## Preview authentication boundary

`app serve` first reuses the saved CLI account through the SDK's locked store.
It prints a one-use, five-minute local authorization URL. Open that exact URL to
establish a path-bound HttpOnly preview cookie without another Console login.
The launch proof travels in a fragment, is removed before app code loads, and is
exchanged only by a same-origin POST; it is not an account token. Treat the URL as
a temporary local capability. Other visitors must use the Console login flow.

The SDK owns account refresh/persistence and cross-process locking. Preview pins
the original user/tenant, checks the immutable SQLite binding and waits for grant
synchronization. Refresh credentials never reach JavaScript. Browser logout drops
only that preview cookie; it does not log out the CLI. Stopping the server clears
local sessions. A missing/unusable saved account retains separate Console login
with an in-memory credential store. Trusted app code receives tenant account scope.

Use the provider with the corresponding serverless SDK build so long-running
applications receive replacement access tokens:

```js
const connection = await window.tiana.connection();
const databaseFetch = createTianaFetch({
  origin: connection.origin,
  auth: window.tiana.auth,
  requestBodyMode: 'buffered',
});
```

`window.tiana.connection()` provides connection metadata and an access snapshot
in `tianaToken`. Use `auth: window.tiana.auth` for data adapters so long-running
clients can renew. No static-token fallback. `sql_api: hrana-v3` is unchanged.

The browser requests a replacement within 30 seconds of expiry. One per-browser
broker serializes management operations and shares concurrent lookups. A cancelled
browser request does not cancel a refresh already in flight; work is bounded to
ten seconds. After a successful rotation, MGR grant readiness gates delivery.
If a management operation fails and the pinned auth SDK cannot distinguish a
failed read from a consumed refresh, the broker conservatively requires a new
login; it never replays an uncertain refresh. A pending-sync polling timeout can
be retried using the saved replacement. No data/SQL operation is replayed here.

`app create/upload/status` use account authentication. The matching hosted App
Bootstrap now exposes the identical provider; MGR renews short-lived tenant data
grants using the existing HttpOnly Console session. The hosted connection POST
body is `{}`. Deploy matching App/MGR before publishing provider-dependent apps.

Account login start/resume uses private, bounded, per-origin pending files and an
exclusive lock. The one-use exchange is marked before transmission; restart cannot
replay it. Shared SDK transport handles ordinary management requests. A small
bounded exchange adapter is required because the pinned SDK does not export the
exchange operation for a previously started transaction. Redirects are rejected.

## Package verification

See [npm package instructions](../packaging/npm/README.md). Build both supported
native Go targets, verify file digests/platform/TLS metadata, and produce an npm
tarball with the two launchers. There is no installation script, bundled deployment
CA or default private origin. The 0.2.1-beta.0 prerelease is separate from the
previously published 0.2.0; publish it under the `next` dist-tag.

Run Go tests/race/vet, public-source scans, `node --test packaging/*.test.mjs`,
native binary help checks, and the Bootstrap browser harness. A fixture run is not
a deployed Gateway/OSS acceptance. Real deployment and WorkBuddy runtime checks
must be recorded separately.

### App identity and creation retries

`app create NAME [-m DESCRIPTION] --json` asks MGR to generate `app_id` (`app-` followed by 24 unpadded base64url characters from 18 random bytes). Capture `data.app_id` and use that exact ID for
`app upload ID`, `app status ID` and the manifest's `app_id`. NAME is a display
name; it neither chooses the ID nor implies name uniqueness. No `--project`,
`--name` or caller-supplied creation ID is accepted.

The CLI locks its existing pending-command store, persists a request ID before
POST, and clears it only after writing a confirmed receipt. Repeat the identical
create command after interruption; do not delete the pending record or create a
new request while the outcome is unknown. A completed command can be run again
to create a separate App resource with the same display name. Reuse an already
recorded ID instead when continuing the same application.

`app list` displays ID and name. Terminals page through results; redirected output
and `app list --json` collect all pages. Listing stays bound to the same account
and tenant; invalid/nonadvancing cursors fail instead of looping.

## Delete a App application

```sh
tiana app list --json
tiana app delete APP_ID                 # terminal confirmation
tiana app delete APP_ID --force --wait --json
```

Deletion accepts an immutable ID or exact name; duplicate names require an ID.
It removes the application, every version and its origin-hosted files. Associated
SQLite and Git resources are kept. `--force` / `-f` skips confirmation and is
required without a terminal. `--wait` / `-w` waits for origin cleanup; otherwise
success means a durable deletion was accepted (`state: deleting`).

An empty application or an application with unfinished uploads can be deleted.
The CLI confirms the current published version and retains both associated resources.
If publication changes before acceptance, MGR returns `APP_DELETE_PREVIEW_CHANGED`;
the CLI clears that rejected intent so the next attempt can confirm the new version.
An unresolved local creation command still needs recovery before another operation.

Accepted deletion hides the application and blocks uploads/hosting. Files and
version metadata are cleaned in bounded batches, with retries for cleanup errors.
There is no upload-signature grace or daily re-sweep of completed deletions.
Public copies already downloaded/cached expire under their cache policy.

The CLI persists origin/account/tenant, the resolved ID and confirmed version before DELETE. Unknown
responses, output failure and interrupted waiting preserve that intent. Repeat the
same command or use the saved ID; do not re-resolve a name or delete the pending
file to bypass it. Stopping the CLI does not cancel server deletion. A confirmed
receipt clears the local intent. If deletion stays pending, inspect the server cleanup logs. Cleanup failures stay
queued; --wait polls until completion or cancellation, without a stored error code.

### App application description

`app create NAME -m "Description"` also accepts `--description`. It is optional
(default empty) and limited to 1024 UTF-8 bytes. Unicode, line breaks and tabs are
preserved; other control characters are rejected. This is application metadata,
not a version identifier or manifest field. Creation, `app list --json` and the
MGR App detail response include `description`. Repeat the same name and
description to recover an interrupted creation; changing either cannot replace
an existing pending request. The matching MGR App API is required.


### Source repository association

For a Tiana Git-hosted application, include paired `git_instance_id` and
`source_commit` in the generated `tiana.app.json`. Use the actual `git-...` ID
and a full lowercase 40- or 64-character commit hash. Commit clean source first,
then derive HEAD in the build step and write it only to ignored build output;
do not embed a commit's hash into its own tracked manifest. Verify the remote
release ref points to that commit before uploading. The CLI validates format;
MGR validates tenant/product availability, not the Git object or build provenance.

The fields travel with each immutable App version and appear in Console details.
Adding/changing either requires a new version. Both omitted means unlinked; a
partial binding is invalid. Requires the matching CLI/MGR/Console rollout.
