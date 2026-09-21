# Go Git remote-helper validation — 2026-09-15

The user's correction supersedes the earlier Rust implementation: Git helper
negotiation and Gateway TLS/H2 transport now live in Go, exposed by
`tiana git remote-helper`. The packaged `git-remote-tiana` is a POSIX exec
launcher that calls the sibling `tiana`.

CLI baseline: `088edf36ad1efa7b53ba716afb72a17872df840a`.
Implementation branch: `codex/dedcd017/git-remote-tiana`.
User authorized commit and push to main on 2026-09-15.
Gateway reference `0cf0d03` already owns the `git` profile / internal ID 5.

## Final implementation

- `internal/gitremote`: Go protocol handling, Endpoint validation, bounded
  credential/CA inputs, strict TLS 1.3 + H2 CONNECT and streaming/half-close.
- `cmd/tiana`: `git` subcommand with `remote-helper`; normal Go builds include it.
- `scripts/git-remote-tiana`: execs same-directory tiana, preserves argv/stdio,
  exit status and signals; never searches PATH for tiana.
- Git-only package: tiana, git-remote-tiana and SHA256SUMS. Complete single-binary
  packaging emits the companion launcher/checksum; Linux bundle manifest,
  installer and uninstaller include it.
- Removed the Rust Git crate, embedded Git assets, Rust Git build script and
  embedded Git process launcher from the CLI working tree. No new Go dependency.
  Git no longer depends on SDK publication or a sibling SDK source checkout.
- Existing SQLD/private Rust helper is unchanged. Previous separate SDK Git enum
  work remains on its own local branch and is not needed for this CLI implementation.

## Verified

- Full `go test ./...` and `go vet ./...` pass.
- Race detector passes `internal/gitremote` and launcher tests. Go unit coverage
  includes exact URL parsing, command bounds, upload-pack/receive-pack buffered
  byte preservation, client half-close with final response, remote EOF with stdin
  open, denied setup and closed CONNECT success-envelope validation.
- macOS arm64 Go-only package and complete SQLD+Git CLI package pass real Git
  conformance; complete CLI `verify-install` reports embedded SQLD contract v3.
- Linux arm64 Go package passes the same real Git and launcher suite in a container.
- Linux amd64 Go package passes that suite under the local cross-architecture
  container execution environment; this is not a native amd64 hardware test.
- Launcher tests cover spaces/empty arguments, binary stdin/stdout, stderr, exit
  code 37 and missing sibling failure despite another tiana on PATH.
- Native operations: push, clone, fetch, shallow clone, ordinary non-fast-forward
  rejection, force, stale and correct leases, explicit tiana::https selector.
  A 1 MiB incompressible blob is pushed/cloned and byte-compared across H2 windows.
- Transport/security: exact Git profile and logical hostname:443 authority,
  custom physical port, no native DATA before validated 200, rejected CONNECT and
  post-200 reset without replay, untrusted TLS refusal, token and private file,
  unsafe permissions/symlink/FIFO/invalid token rejection, no secret in diagnostics,
  and SIGTERM termination with stdin held open.
- Shell syntax, executable modes, diff whitespace, Go package SHA256SUMS and
  complete-package binary/launcher checksums pass.

Setup uses 10 seconds for dialing/TLS and 60 seconds for CONNECT, matching the
previous SDK defaults. The CONNECT deadline is on the owned socket, since the
Go Transport header timeout waits for upload EOF; it is cleared after successful
validation so large/long Git transfers are not cut off by setup timeout.

## Boundaries and artifacts

The network fixture uses a local TLS/H2 server implementing the Gateway contract
plus real git-daemon. Deployed Control/Gateway/Agent/app_git end-to-end acceptance
and storage durability/stop/recovery are outside this run. The full Linux bundle
installer/uninstaller were syntax-checked, not applied to a host installation.

No fs interface/schema, app_git authentication or timeline changes. Knowledge
working trees have no new changes; the earlier untracked knowledge/fs/target is
preserved. Containers are removed after tests; images are retained. Temporary Git
repositories, test credentials/certificates and generated embedded assets are
cleaned automatically. Go packages remain under the session cli-validation/.

Current macOS package: `cli-validation/go-git/` (Go-only) and
`cli-validation/go-complete/` (existing SQLD payload plus Go Git).
Linux packages: `cli-validation/go-git-linux-arm64/` and `go-git-linux-amd64/`.
Earlier Rust source changes are preserved in
`cli-validation/before-go-git-migration.tar.gz`; they are not current deliverables.
