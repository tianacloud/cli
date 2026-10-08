# Application delivery and npm packaging

This guide covers current Web application publication and preview. Native
SQLite/Git connections, account authentication and explicit deployment origin/CA
configuration follow the [CLI README](../README.md).

## Application workflow

Create a Web project with a fixed JavaScript entry and optional SQLite/Git
instance associations. The entry module owns its styles and hash routes. There
is no tiana.app.json or Web product version selector.

```sh
tiana status
tiana sqlite create billing --wait
tiana git create billing-source --wait
# Use the confirmed SQLite and Git instance IDs when creating the Web project.
tiana web create Billing -m "Team billing dashboard" --entry assets/app.js \
  --database-instance-id SQLITE_ID --git-instance-id GIT_ID --json
# Save data.id as WEB_ID; build the application into dist.
tiana web serve WEB_ID --dir dist --port 4174
tiana web publish WEB_ID --dir dist --json
tiana web status WEB_ID --json
```

CLI packages empty root web.yaml plus data/ into one .tweb archive and validates
that data/<entry> exists. Publication uploads through Gateway to the Web App;
MGR manages identity and project metadata, not publication. There is one current
object per application, no publication version list or historical URL.

Publication authenticates with explicit TIANA_TOKEN or TIANA_TOKEN_FILE,
otherwise the selected account access token. Explicit input never falls back
to another credential. The selected account still resolves owner-scoped MGR
project metadata. The hosted entry is `/web/WEB_ID/`, with application hash
routes and no version query parameter.

Retain the publish ID and checksum. `uploaded` confirms the remote object, while
status distinguishes remote and serving checksums. Query `web status WEB_ID
--publish-id PUBLISH_ID --json` after an unknown outcome; do not automatically
repeat PUT. A later status miss is unknown, not proof that publication failed.
Publishing does not modify the create-only entry or instance associations.

## Preview authentication boundary

`web serve` first reuses the saved CLI account through the SDK's locked store.
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

Web create uses account authentication. Hosted Bootstrap exposes the matching
connection provider through the independent HttpOnly Site session. Its ordinary
connection POST body is `{}` with Origin and CSRF proof. Deploy matching
Web/MGR/CLI before using provider-dependent applications.

## Package verification

See [npm package instructions](../packaging/npm/README.md) and the
[release workflow](cli-release.md). Build seven native Go targets, verify
file digests/platform/TLS metadata, and produce the main npm package and its
seven exact-version platform packages. Online is the default and uses system
roots; explicit staging adds its bundled public CA as described in
[staging trust](staging-ca.md).

Run Go tests/race/vet, public-source scans, `node --test packaging/*.test.mjs`,
native binary help checks, and the Bootstrap browser harness. A fixture run is not
a deployed Gateway/OSS acceptance. Real deployment and WorkBuddy runtime checks
must be recorded separately.

## Web identity, metadata and creation retries

MGR generates the permanent id at creation (12 unpadded base64url characters
from 9 random bytes). NAME is only a display name and need not be unique. Capture
data.id and reuse it for serve, publish, status and deletion.

The CLI persists a request ID before create POST and clears the pending record
only after writing a confirmed receipt. Repeat the identical create command to
recover an interruption; do not discard its pending intent or invent another
request while the outcome is unknown. Entry and SQLite/Git associations remain
fixed at creation. Source commits belong to the Git repository and are not Web
project or publication fields.

Name and description project ordinary instance display_name/notes. Description
is optional, at most 2048 UTF-8 bytes. Metadata changes use the instance PATCH
and product_revision CAS. Web list uses owner-filtered generic engine=web
pagination, shows MESSAGE and the hosted endpoint, and retains uint64 revision
precision. See [Web commands](../README.md) for current flags and output.

## Delete a Web application

```sh
tiana web list --json
tiana web delete WEB_ID                 # terminal confirmation
tiana web delete WEB_ID --force --wait --json
```

Deletion accepts a permanent ID or exact name; duplicate names require an ID.
It deletes the Web instance and its sole current object. CLI preserves associated
SQLite and Git instances by sending false deletion flags, with no expected
publication version. The Console can explicitly select associated instance IDs
for deletion; MGR saves the accepted immutable selections and resumes their
ordinary instance deletion jobs after restart. Unknown outcomes must not change
an accepted selection.

`--force` / `-f` skips confirmation and is required without a terminal.
`--wait` / `-w` waits for confirmed deletion; otherwise success means durable
acceptance. The local pending intent retains the pinned origin/account/tenant,
resolved ID and deletion choices. Response loss, output failure or interrupted
waiting preserves that intent. Repeat the same command rather than re-resolving
a name or deleting pending state. Stopping CLI does not cancel server cleanup.
Already cached content and authorized in-flight reads are not recalled.

Installer and packaging tests require
`npm ci --prefix packaging/npm --ignore-scripts` before
`node --test packaging/*.test.mjs`. This installs the ZIP dependency without
running the source template's release downloader.
