# Staging access and CA trust

Online uses `https://console.tianacloud.com` and public certificates validated
against the operating system trust store. No online root certificate is bundled.

To use staging, connect to the Tiana staging VPN/DNS and run:

```sh
export TIANA_API_ORIGIN=https://console.tianacloud-staging.net
tiana login
```

CLI 0.3.4 and later automatically add the bundled
[`staging-root.crt`](../internal/clientconfig/staging-root.crt) when this origin
is selected. This invocation trust also reaches native helpers and Git. An
explicit `--ca-file` or `TIANA_CA_FILE` replaces the bundled extra CA and still
adds to system trust. Keep this environment on every Agent subprocess, including
native `git push`; exporting in a previous independent shell is insufficient.

## Browser setup

The browser that opens the login URL needs its own staging CA trust. Download
`staging-root.crt` from the CLI release source you are using and verify its
SHA-256 fingerprint before installing it:

```sh
openssl x509 -in staging-root.crt -noout -subject -dates -fingerprint -sha256
```

Expected SHA-256 fingerprint:
`2B:E9:94:30:BC:E6:4B:09:DC:68:01:08:A8:FA:8B:83:F2:85:C7:C9:64:41:9C:61:C7:9B:A2:B5:7E:C8:22:9D`.
This is the public Caddy staging root certificate, valid until June 13, 2036.

- macOS: import it into Keychain Access and explicitly trust it for SSL in the
  keychain used by your browser.
- Windows: import it into the current user's Trusted Root Certification
  Authorities store using Certificate Manager.
- Debian/Ubuntu: copy it to `/usr/local/share/ca-certificates/tiana-staging.crt`
  and run `sudo update-ca-certificates`. Browsers with a separate authority store
  also require importing it through their certificate settings.

Restart the browser if needed and open `https://console.tianacloud-staging.net`.
DNS resolution, VPN reachability and certificate trust must all work. For a
login opened on another device, that device needs the same VPN and browser trust.

The CLI package contains only the public root certificate. Staging certificate
issuance and private keys stay with infrastructure operators. On a root change,
operators provide the replacement public root and verified fingerprint; update
this certificate, release the CLI, and update browser trust before using the new
chain. A valid chain never requires disabling TLS verification.
