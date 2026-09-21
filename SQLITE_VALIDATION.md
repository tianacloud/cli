# SQLite CLI verification

The current commands are sqlite shell INSTANCE, shell -e SQL and shell -f FILE.
They use sdk-go-sqlite's exclusive Hrana v3 Session. The CLI retains auth/MGR
resolution, supported-grammar preflight, original source text, readline and
lossless ordered output. The SDK owns wire protocol, baton and value validation.

## Reproduce source checks

Configure authenticated access to the two GitHub SDK dependencies when required,
then use GOWORK=off so a local workspace cannot hide unavailable dependencies:

```sh
go mod verify
go test -mod=readonly -race -count=1 ./...
go vet -mod=readonly ./...
python3 scripts/check-public-source.py
```

Normal tests include synthetic TLS/H2 Gateway peers, command/auth/output tests,
cancellation, response/size validation, no SQL replay, and exit cleanup.
Control vectors are public hostname-sanitized derivatives; see
internal/supervisor/testdata/README.md for their provenance and limits.

For terminal editing/history/Ctrl-C/EOF/quit checks:

```sh
go test -c -o /path/to/test-output/sqlitecli.test ./internal/sqlitecli
python3 scripts/test-readline-pty.py /path/to/test-output/sqlitecli.test
```

## Optional actual App integration

```sh
sh scripts/test-sqlite-app.sh /path/to/app_sqlite
```

This builds the external App library read-only, with artifacts under .cache.
No Rust is linked into the native Go SQLite client. The integration reference
is app_sqlite bccd78e3040c9463e802f3538f4f9ffe0fcd9522. Tests cover triggers,
int64/blob/null values, BEGIN variants, savepoints, rollback and one-shot close.
These tests skip unless their fixture binary is supplied. A synthetic TLS/H2
relay is not a deployed Gateway/Agent or proof of object-storage durability.

## Data-safety boundaries

No reconnect, SQL replay or retry on uncertain writes. Lost responses cannot
prove rollback; server sessions and locks can remain until TTL cleanup. Exit
rolls back only a confirmed active transaction; rollback/close share a bounded
budget. Earlier autocommits can persist after script failure. No --atomic
support or full standalone SQLite grammar compatibility is claimed.
