# 统一出站代理开发与验收记录

状态：SDK v1.0.1 已发布，CLI 远程 pin 已更新；本地检查、七平台构建、本地 npm 新鲜安装和隔离网络访问真实服务验收已通过。CLI PR #9 的实现提交已通过 Linux/macOS/Windows 原生 CI 及 Go 1.25/stable race/vet；CLI/npm 尚未发布。

## 源码、版本与交付边界

- CLI：[PR #9](https://github.com/tianacloud/cli/pull/9)，分支 `feat/unified-outbound-proxy`，基于 `42e5c4be368f63fa3957ae293e2d41d4ab7a8d84`；完整实现已提交并推送。
- sdk-go：[PR #1](https://github.com/tianacloud/sdk-go/pull/1) 已 rebase/merge；不可变 [v1.0.1](https://github.com/tianacloud/sdk-go/tree/v1.0.1) 指向 `240e10f1f2a76ad77a76a5d38a4c4a23f7f1127e`，annotated tag 对象为 `bd0ea48f8621e209535f4a86e08b5524a77ac9c3`。
- CLI 的 go.mod/go.sum 使用远程 sdk-go v1.0.1 和 sdk-go-sqlite v1.0.1。核心 SDK 模块校验和为 `h1:H6CTqTAvCOa7zE3Ds6DgeSVsYNdkFIIKsQ9KbqqByqU=`。Go 生成并整理校验和；产品没有 replace、vendor 或 workspace 依赖。
- 采用空模块缓存、公开 Go proxy 与 sumdb 的独立消费者实际下载两个 SDK，构造 Gateway/fetch/SQLite/auth 客户端；build info 的版本均为 v1.0.1、Replace 为 nil，module verify 通过。开发阶段的仓库外临时 modfile 已不用于正式验收。
- sdk-rust 仅核查，工作区未修改；当前 CLI 使用 Go 内置 helper，contract 3 未改。
- 首版包含 HTTP/HTTPS CONNECT、SOCKS5/socks5h、ALL_PROXY 和 NO_PROXY，不包含 PAC、WPAD 或系统 GUI 代理发现。
- SDK 已按新授权发布 tag；CLI 提交、推送、合并、npm 发布及部署分别遵循当前任务授权，不由 SDK 发布推导。

## 已取得的证据

| 范围 | 结果 | 证据边界 |
| --- | --- | --- |
| SDK 选择与握手 | 专用代理/ALL_PROXY 优先级、NO_PROXY/CGI、严格解析、脱敏、URL 副本；HTTP/HTTPS/SOCKS5、远端 DNS、IP wire 类型、凭证字节边界均通过 | 合成代理；不读取环境的 SDK 公共 API |
| 故障、取消与资源边界 | 代理 TLS、慢 header、SOCKS 方法/认证/CONNECT 分阶段取消与 deadline、畸形回复、EOF；一次建立、不 fallback/retry；物理覆盖及交接后 deadline 清除通过 | fixture 关闭并 join socket/处理 goroutine |
| Gateway/fetch/resource | TLS/H2、独立代理状态、Bearer/Basic 隔离、双向/半关闭；真实 framing PUT/current/receipt、native GET/HEAD 与逻辑 Port 透传通过 | 合成 Gateway/App，和下方真实服务结果分列 |
| CLI 管理及对象流量 | login start/resume/exchange、401 刷新、分页 JSON、目录/ZIP、分享 ZIP 下载/上传/complete、file GET/PUT、真实 serve launch/cookie/文件转发、凭证 canary 通过 | 每项都有 HTTP/SOCKS5 且仅 ALL_PROXY 的本地 fixture |
| helper/worker | 独立 CLI helper、非 443 NO_PROXY、原生环境过滤、旧 Process 值/指针及 Embedded helper 拒绝代理；实际独立更新 worker 代理继承、选定 CA、静默、无提醒预约通过 | contract 3、进程/协议 fixture；真实 Turso 结果单列 |
| SQL 脚本与终端 | CLI -e 经代理；脚本、Shell、错误后停止、重连和 readline PTY 分别通过 HTTP/SOCKS5；编辑、历史、Ctrl-C、事务丢失、未知结果、.read 与 parse error 回归通过 | 真实 SDK、合成 Hrana peer；真实 App/线上 SQL 结果单列 |
| 原生 Git | direct/HTTP/SOCKS5 的 clone/fetch/push/force/lease/shallow、1 MiB 对象、拒绝/断链不重放、取消、TLS、pre-200 通过 | 实际 Git upload-pack/receive-pack，经合成 Gateway；线上结果单列 |
| 真实 Turso | 官方 v1.0.32 的 HTTP/WebSocket positional locator 抓取；实际 CLI/helper、显式 token/已保存账号 × direct/HTTP/SOCKS5 六组通过 | 独立冻结官方制品摘要；保留旧 fixture 原摘要断言 |
| 真实 App SQLite | 类型、trigger/savepoint、退出回滚、TTL；direct/HTTP/SOCKS5 中 SQL_PARSE_ERROR 后保留 TEMP 表/事务，显式 ROLLBACK/COMMIT 和独立读取确认通过 | 原始 App library 测试 carrier，参考源码只读；非 FUSE/S3 存储发布资格 |
| 浏览器与通知终端 | Playwright Chromium 的登录、受限资源、模块/hash route、刷新恢复、HttpOnly、退出；CLI update-check PTY 通过 | bootstrap/browser fixture；浏览器自身网络不由 CLI 代理接管 |
| 工程检查 | 远程 pin 下完整 race/vet、module verify、格式、公共源码与 PEM/hex 正反回归、安全卸载；SDK source-tools/consumer smoke 通过 | GOWORK=off、GOFLAGS 空、-mod=readonly |
| 构建与包 | 七平台构建、24 个 Node packaging/launcher tests、八个 npm tarball、完整性、新鲜本地安装、Linux bundle 通过；七个二进制 build info 均确认两个 SDK v1.0.1 且无替换 | 临时版本 1.0.5-proxy.1，不是 npm 发行；交叉编译与原生运行区分 |
| CLI 原生 CI | 实现提交 `80ae24990acbc2a1aafdb7c151b5cda2e841d5d3` 的 Linux/macOS/Windows 原生测试、vet、launcher/installation，以及 Go 1.25/stable race 检查全部通过 | push 与 PR 两组 workflow 均成功；真实服务验收仍仅在 Linux namespace 执行 |

## 新建隔离环境与真实服务验收

新增 [scripts/proxy-sandbox.py](../scripts/proxy-sandbox.py)，在 Linux 无特权 user/network namespace 中执行 CLI。该 namespace 只有 loopback、IPv4 路由表为空，不能建立外部 TCP；代理服务运行在宿主网络，通过 owner-only Unix socket 和 namespace 内的 loopback bridge 到达。

```text
CLI（namespace：只有 lo、无公网路由）
  → loopback TCP → 私有 AF_UNIX bridge
  → 宿主 HTTP CONNECT 或 SOCKS5 代理 → 真实服务
```

目标 DNS 在代理端解析；目标 TLS 在 CLI/SDK 与真实服务间端到端验证，代理不解密或记录凭据。只在测试子进程设置 ALL_PROXY/NO_PROXY，不修改系统代理或路由。代理限制 64 个活动连接、64 KiB copy buffer，并在退出时关闭连接、移除自己的 Unix socket。

已实际验证的控制和服务路径：

- 关闭 ALL_PROXY 的对照组访问失败；只设置 HTTP 或 SOCKS5 ALL_PROXY 时，真实账号状态及 SQLite/Git/Web 全页列表成功。
- 本次独立创建并记录 SQLite、Git、Web 各一个临时实例，写入只作用于这些已绑定的不可变 ID。
- 两种代理均通过真实 SQL 脚本事务/savepoint；语法错误退出后独立查询确认待提交写入已回滚；真实 Turso 经独立 Go helper 返回 SQL 结果。
- 真实 Git 1 MiB 随机对象通过 HTTP push、SOCKS5 clone/push、HTTP fetch，字节和提交引用逐一核对。
- 两种代理均通过 tenant file 的实际 OSS 上传、下载、字节比较及本次文件删除；当前目录中选取的模板也通过真实签名 ZIP 下载及原样解压。
- 两种代理各执行一次真实 Web PUT，实际完成 App/OSS 上传后，在 fetch 适配器边界丢弃客户端回执以模拟响应丢失。随后使用原 publish_id 查询真实回执，验证 archive 保留、无第二次 PUT，确认 running、remote_sha256/serving_sha256 匹配，再通过原生 channel 0 GET/HEAD 校验实际资源。
- 上述响应丢失是明确的客户端适配器故障模型，没有 MITM，也没有宣称向已部署网络注入了物理断链。原有网络断链/未知结果 fixture 另行覆盖。
- 本地 npm 新鲜安装后的 launcher 再次通过真实 MGR、SQLite 和 Web 状态查询。所有本次临时文件及三个云端实例已按原 ID 清理，实例用量恢复到验收前的 36；不触碰用户原有资源。
- 代理服务已关闭并清理自己的 socket；最终计数为 HTTP 56、SOCKS5 44 次连接，代理端失败为 0。无代理失败对照不会进入代理计数。

环境重现示例（两个终端，已有账号由 CLI/SDK 使用默认凭据存储）：

```sh
# 终端一：创建私有目录，启动仅 AF_UNIX 可达的代理。
proxy_dir=$(mktemp -d)
python3 scripts/proxy-sandbox.py host "$proxy_dir"

# 终端二：使用同一 proxy_dir，分别运行两种代理。
unshare --user --map-root-user --net \
  python3 scripts/proxy-sandbox.py run "$proxy_dir" http -- /path/to/tiana status --json
unshare --user --map-root-user --net \
  python3 scripts/proxy-sandbox.py run "$proxy_dir" socks5h -- /path/to/tiana status --json
```

真实发布回归是显式 opt-in 的 `TestLiveProxyPublicationRecovery`；配置必须指定本次任务自有 Web ID、精确名称、构建目录和私有 receipt 目录，并在 sandbox 中执行。默认测试跳过，不读取真实账号或访问部署。不会将运行时地址、签名 URL、账号资料写入公共源码或此文档。

## 可复现工程命令

CLI 仓库：

```sh
GOWORK=off GOFLAGS= go test -mod=readonly -race -timeout 3m ./...
GOWORK=off GOFLAGS= go vet -mod=readonly ./...
GOWORK=off GOFLAGS= go mod verify
python3 scripts/test_public_source.py
python3 scripts/check-public-source.py
python3 scripts/test_uninstall_safety.py
node --test packaging/*.test.mjs
```

脚本/PTY 代理回归分别设置 TIANA_TEST_SQLITE_PROXY=http 和 socks5h；真实 App、Turso、独立 helper/worker 测试分别使用 TIANA_SQLITE_APP_PEER_BINARY、TIANA_TEST_NATIVE_TURSO、TIANA_TEST_BUILTIN_CLI。浏览器由 TestBootstrapBrowser harness 配合 TIANA_PLAYWRIGHT_MODULE 执行。

本次最终验收目录为 `/tmp/tiana-proxy-final-31rey8ux`：remote-consumer 和独立模块缓存；targets、module-build-proof.json、npm-packages、installed、linux-bundle.tar.gz，以及 private 下的现场证据。private 为 owner-only，不作为公开发布内容。遇到临时磁盘配额后仅清理本任务可重建 Rust cache，App peer 二进制和引用源码保留，受影响构建已重新完成。

## 制品及平台证据边界

- SDK v1.0.1 tag 的 [Go CI](https://github.com/tianacloud/sdk-go/actions/runs/40281576963) 和 [macOS/Windows CI](https://github.com/tianacloud/sdk-go/actions/runs/40281576905) 全部成功。
- CLI 实现提交 `80ae249` 的 PR [Public CLI](https://github.com/tianacloud/cli/actions/runs/40289854607)、[Native CLI platforms](https://github.com/tianacloud/cli/actions/runs/40289854833)，以及 push [Public CLI](https://github.com/tianacloud/cli/actions/runs/40289847538)、[Native CLI platforms](https://github.com/tianacloud/cli/actions/runs/40289847433) 全部成功。后续仅更新验收文档时，最新检查以 PR #9 当前 HEAD 为准。
- 首次 Windows CI 发现新增回归测试误用 Unix `0600` 位；修正后三平台均检查保留普通文件，Unix 另检查 `0600`。HTTP/SOCKS5 的未知发布身份、archive 保留及原 ID 回执查询场景继续运行，未跳过。
- Turso 官方 v1.0.32 Linux x86_64 archive SHA-256：`c35acbcad8e2e7a32580fe380adc4658d3032ddc56d25396b9b123aa4a704107`；解出 binary：`ee56beef9b888ecfb9537ce19badb6bccc33e13ea78763ef2f51ef8d4454684f`。新增官方制品抓取测试已通过；旧 fixture 的 `257ef10bf753477f0fc6cd0754e346aade78bd3614fa8e71fb7eb8b3df4ef31f` 仍独立保留，不放宽摘要断言。
- App 参考提交 `392e21fce0ebba0ab818acad3465f42be712960d` 未修改，SQLite 3.53.4 native build ID 为 `aabf712ce09cce89357592ae7b478508084c28b19ba4b0ac5de28d62aea51fad`。
- CLI 已在 Linux/macOS/Windows 原生 CI 运行代理 fixture；真实公网服务验收在 Linux 隔离网络完成。七平台交叉构建不等于另外四个目标的原生运行或云端验收。
- npm 本地新鲜安装已通过；公开 npm 发布、系统安装及生产部署不在本次已执行范围。

## 回滚

恢复旧 CLI 与旧 SDK 的配对版本会失去新增代理通道及 ALL_PROXY 能力；不迁移账号、pending、回执或数据库。保留未知写入/发布的原始身份，不删除记录或自动重放。v1.0.1 tag 不回写或移动；旧外部 helper 不静默忽略已选代理。
