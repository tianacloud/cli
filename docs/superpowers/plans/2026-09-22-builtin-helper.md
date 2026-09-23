# Built-in connect helper implementation plan

Goal: ordinary Go CLI builds can run connect without a separate Rust helper.
Architecture: re-execute the current CLI in a private helper mode, retaining
contract 3 framed stdin/stdout and the existing ProcessHelper supervisor.
Use the published sdk-go for TLS 1.3/H2 CONNECT. Embedded release assets retain
their existing path; the Go-only path reports mode=builtin. No data/Token in argv.

## Constraints and review focus

Keep pre-200 bytes local, preserve their exact order, never replay SQL. Classify
only bounded HTTP/1.1 headers (16 KiB, 64 fields, 1 second), retain no more than
64 KiB before CONNECT; 32 active local sessions maximum. Support existing v2
pipeline/WebSocket and App's v3 pipeline/cursor paths. Strict profile override.
Use only verified TLS; SDK has no insecure mode, so reject an insecure helper
configuration explicitly. Preserve private pipe and native environment filtering.
Accept only after CHILD_STARTED; verify/monitor owner on Linux/macOS and stop on
control EOF/drain. Close all connections on shutdown and join session workers.
Report only bounded stable errors. No protocol/storage/dependency migration.
Do not commit the new helper work without further instruction; prior endpoint
commit 75cdbfd is already authorized and complete.

## Task 1: Control lifecycle and bounded classifier

Files: internal/supervisor/builtin_helper.go, builtin_classifier.go and tests.
- [x] Add failing lifecycle/classification fixtures using real private pipes.
- [x] Decode HELLO/config/credential/child with existing frame bounds; emit
      HELLO_ACK/BOUND/READY/SERVING/SESSION_ERROR/STOPPED, reject invalid order.
- [x] Implement prefix classifier with fixed limits and request-byte redaction.
- [x] Verify invalid/malformed/oversized headers and explicit profile mismatches
      cause zero Gateway traffic; control EOF and pre-serving drain stop cleanly.

## Task 2: Native SDK relay and owner lifecycle

Files: builtin_relay.go, builtin_owner_linux.go, builtin_owner_darwin.go,
builtin_owner_other.go and tests.
- [x] Test synthetic TLS/H2 Gateway with held CONNECT response and exact bytes.
- [x] Create sdk-go client from configured CA, destination, Endpoint and Token.
- [x] One local connection gets one CONNECT; prefix is written only after 200,
      then bounded bidirectional io.CopyBuffer and half-close; never replay.
- [x] Test HTTP and WebSocket, Token placement, refusal/TLS/post-200 failures,
      native half-close, drain/cancel, owner mismatch/death and worker bounds.

## Task 3: CLI integration and delivery

Files: cmd/tiana/main.go, commands.go, integration tests, README.md, AGENTS.md.
- [x] Add BuiltinHelperLauncher using os.Executable (no PATH helper lookup),
      private entrypoint before normal flag parsing, and helper handshake coverage.
- [x] Build actual CLI in tests, launch a synthetic native client and exercise
      command startup/exit, sanitized environment and private helper lifetime.
- [x] Document mode=builtin, verified TLS, existing native adapter and limits.
- [x] Run full race/vet/scans, macOS/Linux builds, reviewer check; mirror only
      baseline-matching files into original cli and replace delivered binary.

Verification logs and rollback binary live in the session cli-native-helper
artifact directory. Read-only reference code is never edited.

## Execution record

- Endpoint work committed separately as 75cdbfd, no push.
- Protocol/classifier and missing-helper CLI tests reproduced failures before
  implementation, then passed. Full race/vet passed before final review.
- Ruling: Go's immutable SDK Token cannot promise Rust-style allocator zeroization;
  keep it only inside the private helper, clear mutable handoff/copy buffers and
  terminate the helper after drain. No child environment/argv receives the Token.
- Synthetic Gateway initially emitted an extra Date header; SDK correctly rejected
  its strict success-header set. Fixed the test peer, keeping SDK validation intact.
- Committed CONNECT failures now retain unknown-outcome classification; regression
  with malformed 200 headers failed before the error-mapping correction.
- Independent review found owner-exit/control-DRAIN race. Reproduced it with OS
  pipes (write-control failure), fixed by immediately stopping data-plane resources
  while retaining private control until DRAIN/EOF. Owner-exit regression now passes.
- Added capacity/sibling-failure tests and actual compiled-helper opt-in fixtures.
  Review's two stale comments were updated as documentation; no deferred minors.
