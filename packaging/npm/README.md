# Tiana CLI

Install Node.js 20+ and run:

```sh
npm install -g @tianacloud/cli@latest --include=optional
tiana version
tiana login
```

Commands default to `https://console.tianacloud.com`, using system CA trust.
To use another deployment, set `TIANA_API_ORIGIN` to its management origin in
each CLI/Git process environment. The CLI and native Git helper inherit that
origin; separate shell calls do not retain an earlier export.

npm installs the native package for your operating system and CPU. The `tiana`
and `git-remote-tiana` launchers run that binary with your arguments, input and
exit status. Native binaries are included in npm packages.

```sh
tiana web list-template --json
tiana web init-template ledger --dir ./my-ledger --json
```

Read the generated metadata.json and README.md, fill the declared variables,
initialize your SQLite instance, then build and publish your application.

Supports macOS x64/arm64, Linux x64/arm64/riscv64 and Windows x64/arm64.
If the native dependency is missing, rerun the installation with
`--include=optional`. Installation with `--ignore-scripts` works as well.
