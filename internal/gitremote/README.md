# Git through Tiana Gateway

Git support is implemented entirely in Go under `internal/gitremote`, exposed as:

```text
tiana git remote-helper <remote> [url]
```

Git normally invokes the packaged `git-remote-tiana` launcher. This POSIX script
executes the **same-directory** `tiana git remote-helper`, preserving arguments,
stdin/stdout/stderr, signals and exit status. It never searches PATH for another
tiana. No Rust Git binary, SDK Git source checkout or embedded Git payload is
required. Existing SQLD support retains its own private Rust helper.

## Build and install

From CLI, with Go installed:

```sh
scripts/build-git-cli.sh "$PWD/dist/git-package"
export PATH="$PWD/dist/git-package:$PATH"
tiana git --help
git clone 'tiana://ep-<endpoint-ulid>.<deployment-domain>[:port]/repo.git'
```

Replace placeholders with the deployment's hostname/port; omitted port is 443.
The output directory contains `tiana` and executable `git-remote-tiana`. Copy both
to the same installation directory on PATH. A normal `go build ./cmd/tiana`
also includes Git; copy `scripts/git-remote-tiana` beside that executable.

The existing complete CLI build includes the launcher beside its output:

```sh
scripts/build-single-binary.sh darwin arm64 /path/to/private-sqld-helper \
  "$PWD/dist/complete/tiana"
```

Create the output parent first. If building an artifact under a versioned file
name, install it as `tiana` beside `git-remote-tiana`. The Linux bundle, checksum
manifest, installer and uninstaller now include the launcher as well.

## Connection options

Only `/repo.git` is accepted. URL user-info, query, fragment, percent escapes,
alternate repository paths and noncanonical Endpoint identities are rejected.
The explicit selector `tiana::https://<endpoint>[:port]/repo.git` also works;
plain HTTPS selects Git's normal HTTP transport instead.

| Environment | Purpose |
| --- | --- |
| `TIANA_TOKEN_FILE` | Optional owned regular 0600 token file, at most 512 credential bytes plus optional LF/CRLF; no symlink or special file. |
| `TIANA_TOKEN` | Optional opaque connection credential; mutually exclusive with token file. |
| `TIANA_API_ORIGIN` | Management origin inherited from the launcher, selecting the account session. No MGR request is made. |
| `TIANA_CA_FILE` | Deployment CA PEM, regular file, at most 64 KiB / 8 certificates. |
| `TIANA_GATEWAY_ADDRESS` | Optional physical host:port override; preserves Endpoint identity. |

When neither explicit source is set, read the current management origin's
access token from `credentials.json` (`TIANA_CREDENTIALS_FILE` overrides its path).
The account token must be present and unexpired; otherwise run `tiana login`.
No implicit login, refresh, anonymous access or credential prompt occurs.
Invalid explicit input fails without falling back. No InstanceToken cache is
read or maintained. Gateway rejection never retries with another credential.
Only connect to a trusted deployment Endpoint; hostname validation is not a
deployment-domain allowlist.

To use the account session:

```sh
unset TIANA_TOKEN TIANA_TOKEN_FILE
export TIANA_API_ORIGIN=https://console.example.test
tiana login
git clone tiana://ep-00000000000000000000000000.git.example.test/repo.git
```

The launcher and sibling `tiana` must both be on the installed Git helper path.
 TLS certificate/hostname verification
is mandatory for Git, including builds with the legacy SQLD development TLS flag.
System trust roots plus the optional CA are used. TCP uses the published port or
override; SNI uses Endpoint hostname; CONNECT authority uses hostname:443.

## Protocol and validation

Go advertises `connect`, accepts upload-pack/receive-pack, validates regular H2
CONNECT 200 with profile `git`, sends one `/repo.git` daemon pkt-line, and then
relays native Git bytes. Commands are bounded to 4096 bytes. Buffered stdin is
preserved, client EOF half-closes and drains, and remote EOF ends the command
without waiting for stdin closure. Setup has separate 10-second dial/TLS and
60-second CONNECT deadlines; transfers are not capped by those setup deadlines.
No automatic reconnect/replay, HTTP fallback, stateless-connect or upload-archive.
An interrupted push can have an unknown outcome and returns failure.

```sh
go test ./...
go test -race ./internal/gitremote ./internal/gitremote/conformance
go vet ./...
TEST_GIT_REMOTE="$PWD/dist/git-package/git-remote-tiana" \
TEST_TIANA_CLI="$PWD/dist/git-package/tiana" go test -v ./internal/gitremote/conformance
```

The fixture uses real Git and a local TLS/H2 CONNECT server plus git-daemon. It
covers operations, 1 MiB object integrity across flow-control windows, credentials,
TLS, cancellation and failed streams without replay. It does not assert deployed
Control/Gateway/Agent/app_git acceptance. See [validation record](VALIDATION.md).

References: [Git remote helpers](https://git-scm.com/docs/gitremote-helpers),
[Go HTTP transport](https://pkg.go.dev/net/http#Transport).
