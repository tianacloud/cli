# CLI release

## GitHub Actions (recommended)

In npm's `@tianadb/cli` package settings, add a GitHub Actions trusted publisher:

| Field | Value |
| --- | --- |
| Organization | `tianacloud` |
| Repository | `cli` |
| Workflow filename | `release.yml` |
| Environment | Leave empty |
| Allowed actions | Allow direct `npm publish` |

The GitHub repository needs `SDK_READ_TOKEN` for the private Go SDKs and
`GITEE_TOKEN` for `tianacloud/cli-releases`. npm publication uses OIDC and needs
no npm secret. Workflow permissions and `repository.url` are already configured.
The workflow must be present on the default branch to use **Run workflow**.

Open GitHub Actions → **Release CLI** → **Run workflow**. Select the source branch,
enter a new version, and choose `latest` or `next`. Keep `dry_run` selected for the
first run; after it passes, repeat with the same source/version and clear `dry_run`.
Native Linux/macOS/Windows tests must pass before the release job runs. The release
job uploads Gitee attachments, verifies anonymous downloads, publishes npm, and
checks installation with a fresh npm cache. Node.js and Go run on GitHub's runner.

OIDC authentication happens during `npm publish`, so it does not run `npm whoami`.
The publish command sets the selected dist-tag. On a rerun, an identical version
and matching tag are accepted; changing an existing tag separately requires an
interactive npm login. See [trusted publishing](https://docs.npmjs.com/trusted-publishers/).

## Local release

Run from a CLI checkout on Linux or macOS:

```sh
./scripts/release.mjs --dry-run
./scripts/release.mjs
```

The version defaults to `packaging/npm/package.json`. Update its version before
releasing changed artifacts, or pass an explicit version:
`./scripts/release.mjs 0.2.2-beta.0`. The selected version is applied to the
native executable and the staged npm package; the script does not edit the source
package.json or make Git commits. Output is retained under `dist/releases/`.
`--dry-run` builds and packs everything without reading credentials or uploading.
The default npm tag is `latest`, including when the version contains `beta` or
`rc`. Use `--tag next` to publish to the explicit preview channel instead.

## Prerequisites

- Go satisfying go.mod, Node.js 20+, npm, Git, tar and zip.
- Access to the CLI's pinned, published Go dependencies. Release builds use
  `GOWORK=off`; a local SDK workspace is not a released dependency.
- An initialized public Gitee `tianacloud/cli-releases` repository, with a token
  that can create releases and upload attachments. The script does not create or
  change repository visibility.
- The Gitee token in `~/tmp/gitee_token.txt`, or another file specified with
  `--gitee-token-file /path/to/file`. Restrict access to this file. The token is
  read at runtime and is not included in archives or npm packages.
- An npm identity with publish access to `@tianadb/cli`: set `NPM_TOKEN` or use
  `npm login --registry=https://registry.npmjs.org/`.

## npm token authentication

Create an npm granular access token with read/write access to `@tianadb/cli`.
For unattended direct publication, enable Bypass 2FA if the package policy permits
token publication. See [npm token documentation](https://docs.npmjs.com/creating-and-viewing-access-tokens/).
As of September 2026 this supports direct publication; npm has announced a
January 2027 target for removing direct publication from bypass-2FA tokens.
See the [npm announcement](https://github.blog/changelog/2026-07-31-restricting-npm-bypass-2fa-granular-access-tokens/).

Read the token without including it in shell history (Bash):

```bash
read -rsp 'npm token: ' NPM_TOKEN; echo
export NPM_TOKEN
./scripts/release.mjs
unset NPM_TOKEN
```

When `NPM_TOKEN` is set, npm authentication checks, publication and dist-tag updates
use `scripts/npm-token.npmrc`, which contains only an environment-variable reference.
The script does not write the token or modify your user `.npmrc`. Without the variable,
npm uses its normal login configuration. Dry runs do not authenticate or publish.

## What is published

| System | Architectures | Archive |
| --- | --- | --- |
| macOS | amd64, arm64 | tar.gz |
| Linux | amd64, arm64, riscv64 | tar.gz |
| Windows | amd64, arm64 | zip |

The script builds all seven native Go executables, verifies architecture and
manifest checksums, and packages one archive per platform plus `SHA256SUMS`.
Gitee receives a `vVERSION` release. After all archives can be downloaded
anonymously with matching hashes, the script publishes the small npm package.
The npm postinstall script downloads only the user's platform archive.

The two npm commands remain `tiana` and `git-remote-tiana`. Installing requires
access to both the npm registry and Gitee. If lifecycle scripts were disabled,
run `npm rebuild -g @tianadb/cli --ignore-scripts=false` to install the binary.

## Interrupted publication

Retain the source and version, resolve the reported authentication/network error,
and rerun the command. Existing Gitee attachments are verified before missing
attachments are uploaded. Conflicting bytes stop publication; use a new version
for changed content. Already published npm content is accepted only when its
integrity matches the local tarball, and the requested dist-tag is then set.
Published attachments are not overwritten and npm versions are not unpublished.

Run `node --test packaging/*.test.mjs` for archive, installer and publication
checks. Cross-compilation proves binary availability, while functional platform
acceptance also requires running the CLI and its tests on that platform.
