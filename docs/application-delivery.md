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
tiana apps serve --dir dist --port 4174
tiana apps create --project billing --name Billing --json
tiana apps upload --project billing --version release-1 --dir dist --json
tiana apps status --project billing --version release-1 --json
```

Record the real instance IDs and Git remote URL. Push the matching source commit
to the user's Tiana Git before publishing its build. Reuse immutable version IDs
only with identical artifacts. Publication pins the account identity and does not
replay a management write after a rejected credential or uncertain response.
Upload links carry object scope and required private ACL/public-resource tags;
management credentials are never forwarded to object storage. Only a confirmed
published descriptor is reported as a hosted application URL under `/web/ID/`.

## Preview authentication boundary

The old preview loaded a local InstanceToken file. The new main deliberately
removed that cache and uses account credentials for native connections. Preview
cannot expose that broad account credential to arbitrary browser application code.
It therefore opens a separate browser authorization, keeps the account session in
memory, verifies access to the manifest's immutable SQLite instance, and requests
an endpoint-scoped credential lasting at most ten minutes. That preview-only
issuance uses the existing MGR scoped-token API, not a restored CLI token command.
It has one stable request ID per issuance, caches the result in memory and stops
on an uncertain/pending response; sign out and investigate before starting again.
The CLI account file and old instance-token files remain untouched by preview.

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
