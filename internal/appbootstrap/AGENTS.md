# Preview account authentication (2026-09-26)

User requested migrating CLI apps to access/refresh authentication. This decision
supersedes older root guidance requiring instance-scoped preview token issuance.

- Each browser login owns a separate in-memory SDK account store; do not read or
  overwrite the CLI account file. No instance-token API/cache is used.
- `_tiana/connection` keeps its compatible descriptor shape, but `tianaToken` is
  now the current account access token. Refresh credentials never leave the CLI.
  Account access has tenant scope, broader than the former endpoint grant; only
  trusted application code should run under this authorization.
- `window.tiana.auth.getAccessToken({signal})` is the dynamic provider for the new
  serverless SDK. Old snapshot consumers must switch to the provider for refresh.
  `app create/upload/status` remain account-authenticated. MGR published bootstrap
  and its issuance lifecycle are outside this local preview change.
- Broker calls serialize/share credential rotation. Browser cancellation only
  cancels its wait; independent work has a ten-second ceiling. Sync readiness must
  be complete before the access token is sent to the page. Management GETs may use
  pinned SDK's one-time 401 recovery; data requests are never retried here.
- The pinned Go auth SDK cannot distinguish an uncertain refresh from all read
  failures. Conservatively latch management-operation failures until new login.
  A timeout while waiting between successful pending-sync reads may retry the
  same stored replacement. Do not resend an old refresh token after unknown commit.
- No filesystem/storage migration. State disappears on process exit. Refresh
  rotation saves the whole in-memory credential under a mutex before release.
  Broker operations have one flight per browser and bounded resources, no idle
  refresh timers. Existing Host/Origin, HttpOnly/SameSite cookie and CSRF protections
  are retained. Browser lookup retry is safe because rotation belongs to broker.

Validation: TLS MGR fixtures for access-only response, no token issuance, denied
instance access, sync pending, concurrent rotation, cancelled waiter, unknown
refresh/no replay; race detector and browser provider/reload/logout fixtures.
Do not call fixture tests deployed Console/Gateway acceptance. Rollback requires
returning consumers to static token snapshots before removing the provider API.

## Saved CLI login reuse (2026-09-27)

User explicitly requested removing repeated preview login and adding hosted auth.
This supersedes the browser-only isolation rule above: app serve defaults to the
saved CLI account when it authorizes the manifest database. SDK handles disk and
cross-process rotation locks; never copy a refresh token into an independent store.
Pin user+tenant, stop on account switch, never replay uncertain refresh or SQL.
Fallback Console login remains independent/in-memory only when saved account is
unavailable. Browser receives only access; this grants trusted app code tenant scope.

Require a random 256-bit one-use launch capability (five-minute validity) to attach
the saved identity. Emit only a fragment URL, clear it before loading app code,
exchange in a same-origin POST header, issue a path-bound HttpOnly SameSite=Strict
cookie. Anonymous loopback visitors cannot retrieve the account or app assets.
Keep Host/Origin/Fetch-Site checks and bounded sessions. Logout removes preview
cookie only; server shutdown discards sessions. No new storage format. Failed
launch response consumes capability; restart or explicit Console login recovers.

Both Web/CLI bootstrap.js copies are identical and expose dynamic auth. Hosted
connection uses {} and server-owned account renewal; no instance token fallback.
Retry only pending-sync lookup, share parallel calls, isolate waiter cancellation,
allow later lookup after lost response, never replay data operations. Verify real
browser launch/replay/secret removal, saved account reuse/switch, SDK rotation,
source scans, full race/vet and builds. Deployment is separate from code changes.

账户文件可能被其他 CLI 进程刷新。连接查询前后必须比较 access-token 快照；发生变化时拒绝该次返回，下一次查询重新校验数据面同步状态，不能把未确认同步的替换 token 交给应用。刷新仍由原 SDK 的持久化锁串行化。绑定预览端口成功后才访问账号。

## Preview authorization appearance (2026-09-27)

Use the Web homepage's Ink & Paper palette, actual Tiana brand glyph, pill
buttons and restrained borders for the embedded login page. Bundle artwork
inline; no external fonts, scripts or remote asset dependencies before login.
Keep status live announcements, visible keyboard focus, reduced motion and
responsive layout. Presentation changes must retain the existing element IDs,
one-use capability exchange, Console popup/poll and authorization boundaries.
Verify desktop/mobile rendering and the existing browser authorization flow.
