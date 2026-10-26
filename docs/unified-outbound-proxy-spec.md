# CLI 与 Go SDK 统一出站代理修改 spec

状态：sdk-go v1.0.1 已发布，CLI 已更新远程 pin，并完成本地、独立远程消费者及隔离网络访问真实服务的验收，详见[验收记录](unified-outbound-proxy-validation.md)。首版包含 HTTP/HTTPS 代理、SOCKS5 和 ALL_PROXY，不包含 PAC。CLI 跨平台原生 CI、提交及发布边界在验收记录中单列。

目标是在不能直接访问公网、但能连接公网代理的机器上，使用 CLI 完成登录、资源管理、Web 发布、SQLite 与 Git 操作。所有出站使用一致的代理选择规则；不配置代理时保留已有直连行为。代理只改变连接路径，不改变目标身份、认证、业务协议、写入身份或重试语义。

## 1. 代码基线与已确认的问题

初始阅读基线（通道表描述修改前的问题；当前交付状态见验收记录）：

| 仓库 | 基线 | 用途 |
| --- | --- | --- |
| CLI | `main`，`42e5c4be368f63fa3957ae293e2d41d4ab7a8d84` | 本地实现分支为 `feat/unified-outbound-proxy`；初始文档分支为 `docs/unified-outbound-proxy-spec` |
| sdk-go | 新克隆的 `main`，`c4cbcde2758cba233f072521907168f74af521c2` | 位于 CLI 同级目录 `sdk-go`，PR #1 已合并；v1.0.1 指向 `240e10f1f2a76ad77a76a5d38a4c4a23f7f1127e` |
| sdk-rust | 新克隆的 `main`，`10f25420260aedb0ca43b4fb88ae4e2028313084` | 位于 CLI 同级目录 `sdk-rust`；当前是通用 transport 库，不包含 helper |
| CLI 当前固定 SDK | `sdk-go v1.0.1` | 已发布远程 tag，对应 `240e10f1f2a76ad77a76a5d38a4c4a23f7f1127e`；初始 pin 为 v1.0.0 |
| CLI 固定 SQLite SDK | `sdk-go-sqlite v1.0.1` | 补充检查已下载模块中的配置传递；本次不克隆第三个仓库 |

初始 sdk-go main（c4cbcde）与当时 CLI 固定的 v1.0.0 在代理能力上相同。现已发布的 v1.0.1 也包含该基线的冷启动容量错误处理变更；它相对 v1.0.0 的版本差异不只有代理功能。

| 出站通道 | 现状及代码位置 | 需要的改动 |
| --- | --- | --- |
| MGR 登录、刷新、资源及地址查询 | [CLI 客户端创建](../cmd/tiana/main.go)、[SDK auth](../../sdk-go/auth/client.go) 默认使用 Go HTTP 客户端；`auth.Config.HTTPClient` 可注入 | CLI 注入统一策略；SDK auth 无需新增代理接口 |
| 独立授权码交换 | [login_exchange.go](../internal/authclient/login_exchange.go) 单独构建 HTTP 客户端，可继承 CLI auth 的 HTTPClient | 确保使用相同配置，保留单次交换与禁重定向 |
| Web 发布、current、receipt 查询 | [serverless.go](../internal/apppublish/serverless.go) → [fetch/client.go](../../sdk-go/fetch/client.go)，自建 Transport 没有 Proxy，也无注入入口 | SDK fetch 新增显式网络配置，CLI 传入 |
| CLI SQLite shell | [sqlitecli/client.go](../internal/sqlitecli/client.go) → SQLite Session → sdk-go Gateway；[SDK client.go](../../sdk-go/client.go) 直接拨 TCP | Gateway SDK 新增显式网络配置，SQLite 沿现有 Gateway 字段透传 |
| Git remote-helper | [connect.go](../internal/gitremote/connect.go) 先拨 TCP，再 TLS/H2；仅持有一条预建连接 | 仅替换建立目标字节流的步骤，保留 Git 独有约束 |
| connect Go helper | [builtin_helper.go](../internal/supervisor/builtin_helper.go) → Gateway SDK；[adapter.go](../internal/supervisor/adapter.go) 当前过滤代理变量 | 给 helper 显式传递代理环境快照；原生子进程继续过滤 |
| SDK 原生 Web resource | [fetch/resource.go](../../sdk-go/fetch/resource.go) 将 fetch.Config 转为 Gateway Config | 新网络字段必须一并透传，避免 channel 0 遗漏 |
| 文件、模板与分享包的对象存储读写 | [files.go](../cmd/tiana/files.go)、[file_request.go](../internal/authclient/file_request.go)、[webtemplate](../internal/webtemplate) 使用默认 HTTP 或其克隆 | 改为统一构造入口，继续与管理凭证隔离 |
| 更新检查与后台 worker | [updatecheck/check.go](../internal/updatecheck/check.go)、[version.go](../cmd/tiana/version.go)、[main.go](../cmd/tiana/main.go) | 显式与后台路径都接入，保留两秒预算及失败不阻塞产品操作 |

普通 Go 构建和当前 npm `scripts/build-platform.mjs` 使用 Go 内置 helper。`build-release.sh`、`build-single-binary.sh`、Linux bundle 仍保留引用旧外部 helper 的代码，但不能据此认为当前 Rust 仓库还提供该二进制。新克隆的 sdk-rust 明确设置 `autobins = false`、没有 bin target，其 AGENTS 要求不恢复 helper／私有控制协议；旧构建路径需按第 5.2 节收敛。

## 2. 范围和首版配置规则

本方案使用环境代理，不新增 CLI flag、配置文件、代理持久化或凭证存储。操作系统 GUI 代理、PAC/WPAD 和浏览器网络不在首版范围；CLI 打开的浏览器仍由浏览器自行联网。不新增 PAC 环境变量、脚本获取／执行、JS runtime 或额外脚本传递管道。

首版支持代理 URL 的 `http://`、`https://`、`socks5://` 与 `socks5h://`。HTTP/HTTPS 代理支持 URL userinfo 的 Basic 认证；SOCKS5 支持无认证及 username/password 认证。两种 SOCKS scheme 均与 Go 标准 Transport 一致，把未解析的目标 hostname 交给代理，不采用 curl 的 socks5 本地 DNS 语义。SOCKS4、SOCKS UDP、NTLM、Kerberos 和交互式代理认证不在首版范围。

环境变量按目标 URL 的 scheme 选配置，代理 URL 的 scheme 决定实际传输。例如 HTTPS_PROXY 可以指向 HTTP CONNECT 代理或 SOCKS5 代理；其变量名不表示代理服务器必须使用 HTTPS。

| 输入 | 规则 |
| --- | --- |
| `HTTPS_PROXY` / `https_proxy` | 所有 HTTPS 出站，以及 Gateway/Git 的 TLS 通道优先使用此设置 |
| `HTTP_PROXY` / `http_proxy` | HTTP URL 优先使用此设置；不作为 HTTPS_PROXY 的回退 |
| `ALL_PROXY` / `all_proxy` | 对应 scheme 的专用代理未配置时使用；同样支持四种代理 URL scheme |
| `NO_PROXY` / `no_proxy` | 复用固定版本 `httpproxy.Config.ProxyFunc` 的域名、端口、IP、CIDR、`*` 匹配语义 |
| 大小写同时设置 | 取第一个非空的大写值，否则小写；与 Go 当前规则一致 |
| 空值 | 按 Go 环境代理规则表示未配置；不是 token/origin 的“显式空值失败”语义 |
| 仅 `ALL_PROXY` | HTTP、HTTPS 和全部原生 TLS 通道都使用它，除非命中绕过规则 |
| 代理地址无 scheme | 接受 `host` 或 `host:port` 并规范为 HTTP 代理；拒绝其他歧义形式 |
| 错误配置 | 大小写选择后，三个代理变量的每个非空值都在构建策略时严格校验，即使其中某项会被更具体配置覆盖；不吞成直连或换用其他代理 |
| loopback | `localhost`、loopback IP 的目标始终直连；不通过 DNS 查询把其他主机判成 loopback |
| CGI 环境 | 快照保留 `REQUEST_METHOD` 是否非空；对由 HTTP_PROXY/http_proxy 提供的 HTTP 路由保留 CGI 防护，不改用 ALL_PROXY 绕过拒绝 |

代理选择顺序固定为：逻辑目标 loopback／显式物理 loopback 覆盖／NO_PROXY → 对应 scheme 的专用代理 → ALL_PROXY → direct。命中绕过表示配置选择直连；代理建立失败后的直连回退始终禁止。纯本地帮助和版本不初始化网络；需要网络时先校验整份配置，再按目标匹配绕过。

代理 URL 拒绝不支持的 scheme、缺失 hostname、非法端口、非根路径、query、fragment、控制字符和不合法 userinfo；根路径规范为空路径。省略端口分别使用 HTTP 80、HTTPS 443、SOCKS5 1080；IPv6 地址必须使用方括号。首版 proxy URL 字符串每项最多 8 KiB、NO_PROXY 最多 16 KiB；检查错误只标识变量名，不回显值。NO_PROXY 的条目解析沿用 Go 的 best-effort 规则；不自行添加 DNS 解析。

SOCKS userinfo 若存在，URL 解码后的 username/password 各为 1..255 字节，提前拒绝空值、超长或控制字符，避免不同拨号实现延迟失败或处理不一致。代理凭证保持原值，不推断 token 类型；Basic 与 SOCKS 认证字段只发给代理。

统一策略从 HTTPProxy、HTTPSProxy、AllProxy 生成有效的 HTTP/HTTPS 候选，再将候选与 NoProxy 交给固定版本的 httpproxy.Config.ProxyFunc。保留原始配置来源：CGI 防护只针对由 HTTPProxy 提供的 HTTP 路由；HTTPProxy 缺失而显式配置 AllProxy 时不误标为 HTTPProxy。loopback 绕过先执行，不靠 DNS 改写决策。

示例均使用无凭证的本地代理：

```sh
# HTTP CONNECT 代理，用于全部 HTTPS/原生 TLS 通道。
export HTTPS_PROXY=http://127.0.0.1:8080

# 或只配置统一 SOCKS5 代理，让所有出站使用同一个回退。
unset HTTP_PROXY http_proxy HTTPS_PROXY https_proxy ALL_PROXY all_proxy
export ALL_PROXY=socks5h://127.0.0.1:1080
export NO_PROXY=localhost,127.0.0.1,[::1]
```

代理本身位于 loopback 不会触发目标绕过；需要匹配的是目标地址，避免本机代理入口被误判为“目标应直连”。

下表中的配置均已通过格式校验，HTTP_PROXY 记为 H、HTTPS_PROXY 记为 S、ALL_PROXY 记为 A：

| 配置／目标 | 结果 |
| --- | --- |
| 无 H/S/A，公网 HTTP 或 HTTPS | direct |
| 只有 H，公网 HTTPS／原生 TLS | direct；HTTP_PROXY 不跨 scheme 回退 |
| 只有 A，公网 HTTP／HTTPS／原生 TLS | A |
| H + A，公网 HTTP | H |
| S + A，公网 HTTPS／原生 TLS | S |
| H + A，但没有 S，公网 HTTPS／原生 TLS | A |
| 任意有效代理配置，命中 NO_PROXY／目标 loopback／显式物理 loopback 覆盖 | direct |
| 已选代理建立失败 | 失败；不换用 A、不重拨、不尝试 direct |

仅配置不适用于 HTTPS 的 HTTP_PROXY 时仍按 Go 习惯直连；CLI 不提供“必须经过代理”的独立开关。禁公网机器应配置 HTTPS_PROXY 或 ALL_PROXY，网络侧可继续禁止直接出站。

CLI 在 invocation 开始时捕获环境快照，首次需要网络时构造并校验不可变策略，在同一 invocation 的所有出站间共享。help、version、纯本地参数错误不因代理格式而失败。管理写操作在保存新 intent／发送请求之前完成代理配置校验；已有 pending 记录照常保留。代理配置不进入 pending argv、请求 key、发布回执或账号文件。

更新检查作为辅助功能，代理错误仍返回 unavailable，不改变主命令退出结果。

## 3. 分层与 SDK 接口

新增 SDK 公共包 `github.com/tianacloud/sdk-go/outbound`。它只负责显式网络策略和到目标的连接，不读取环境、账号或部署默认值，不导入 CLI，不执行认证刷新，不理解 SQL、Git 或发布内容。

拟议接口如下；实现阶段可调整私有结构，公开职责和 nil 行为需保持：

```go
package outbound

type Config struct {
    HTTPProxy    string
    HTTPSProxy   string
    AllProxy     string
    NoProxy      string
    CGI          bool
    ProxyRootCAs *x509.CertPool
}

type Target struct {
    // 仅路由需要的 scheme、逻辑 hostname 和端口；不包含业务 URL 的路径或签名。
    URL         *url.URL
    // 可选物理 host:port；不改变 TLS SNI、证书身份或业务 authority。
    DialAddress string
}

type Policy struct { /* 不可变配置，不持有业务连接池 */ }

func New(Config) (*Policy, error)
func (p *Policy) ProxyForURL(*url.URL) (*url.URL, error)
func (p *Policy) DialTunnel(context.Context, Target) (net.Conn, error)
```

`New` 校验并拷贝配置，克隆根证书池；不做网络、文件或环境 I/O。`Policy` 可并发使用。`ProxyForURL` 不暴露内部可变 URL：每次返回独立副本。Config、Policy、代理错误的普通 fmt 格式必须脱敏。

`DialTunnel` 返回通向物理目标的字节流：直连已建立，或已完成外层 HTTP CONNECT／SOCKS5 握手。它不建立目标 TLS、不发送 Tiana token。目标 TLS 仍由 Gateway/fetch/Git 原有代码负责。Policy 自身不拥有调用方业务连接，返回成功后由调用方关闭；失败则关闭本次创建的全部 socket。

SDK 的 Gateway 和 fetch 添加可选 Network 字段；Gateway 另加 Port，区分逻辑网络端口与物理覆盖：

```go
// tiana.Config
Network *outbound.Policy
// Port 为逻辑网络端口，默认 443；用于默认 TCP 目标和 NO_PROXY 匹配。
// Gateway CONNECT authority 仍为 Endpoint:443。
Port string

// fetch.Config
Network *outbound.Policy
```

nil 保持各 SDK 当前行为：Gateway 和 fetch 直连，不新增隐式环境读取；auth 的 nil HTTPClient 仍保留当前默认客户端行为。显式 direct 策略与 nil 在数据路径上等价。CLI 总是传入其统一策略。

选择一个显式网络对象，而不同时新增 ProxyURL、ProxyFunc、DialContext、HTTPClient 等多套相互覆盖的参数，以免再次形成优先级分歧。首版不需要开放任意 HTTPClient 给 fetch；fetch 继续掌握禁重定向、禁解压、响应上限及连接池生命周期。自定义连接注入若未来确有需求，应另行定义兼容接口。

### 3.1 普通 HTTP 的适配

CLI 新增 `internal/netconfig`，从环境快照构造 Policy，并集中创建 HTTP Transport：

- 使用新的／克隆的 Transport，设置 `Proxy` 为 Policy 的选择函数。
- 底层 DialContext 只直连 Transport 选择的下一跳，不再次走 DialTunnel，避免双重代理。
- 管理、对象存储、更新检查使用不同客户端和连接池，共享策略；凭证、Jar、headers、redirect policy 不混用。
- 不修改全局 `http.DefaultTransport` 或 `http.DefaultClient`。
- 保持各通道现有请求超时、TLS 最低版本、输出上限与重定向策略；新增策略本身不增加通用重试。

复用 Go 标准 Transport 完成普通 HTTP 的 HTTP/HTTPS/SOCKS5 代理协议；原生 TCP/TLS 路径按第 3.4 节选择共享 CONNECT 适配或官方 SOCKS5 Dialer。HTTP、Web fetch、Git 与 Gateway 必须从同一个 Policy 得到相同路由，不各自读取默认代理函数。

### 3.2 Gateway、fetch 与 Git 的适配

Gateway：将 `client.go` 的直接 DialContext 改为显式 Policy.DialTunnel，nil 时保持原拨号。随后沿用原 TLS 1.3、ALPN h2、CONNECT 200 校验、流控及会话生命周期。

fetch：显式 Network 时，通过私有 DialTLSContext 完成 DialTunnel → 目标 TLS，并设置 `Proxy=nil`，避免 Transport 再做代理。保持 ForceAttemptHTTP2、固定 ServerName、现有 TLS 策略与请求预算；TLS 握手由回调显式按剩余预算限制，不能假设自定义 DialTLSContext 自动受到 Transport.TLSHandshakeTimeout 约束。nil Network 保留旧路径。所有新连接都使用同一策略，连接池仍属于 fetch.Client。

fetch.ResourceClient：构造 tiana.Config 时透传 Network 及逻辑 Port。channel 0 与 channel 1 的选择、请求 framing、编码字节均不变。

Git：只把预建 TLS 连接之前的 raw TCP 建立改为 DialTunnel；沿用原有持有单条 TLS 连接的 Transport，不能改用可自动重连的通用 HTTP 客户端。整个 invocation 只尝试一次代理／目标连接，不引入 HTTP fallback 或推送重放。

### 3.3 逻辑目标与物理地址覆盖

明确保留三个身份：

| 身份 | 作用 |
| --- | --- |
| 逻辑 URL 的 hostname 与网络端口 | 对应 scheme 的代理、ALL_PROXY 和 NO_PROXY 的选择依据 |
| 物理 DialAddress（若提供） | 直连 TCP 目的地，或 HTTP CONNECT／SOCKS5 目的地 |
| 现有 TLS SNI 与 Tiana authority | 继续由现有协议规定；原生 CONNECT 的逻辑端口仍为 443 |

对显式物理 loopback 覆盖，本方案规定直连，以保留本地 Gateway fixture／调试路径；不先解析主机名再作该判断。其他物理覆盖不隐式绕过代理。NO_PROXY 匹配逻辑目标，不额外匹配物理覆盖；示例和测试必须说明这一点。

普通 HTTP 无 DialAddress，按请求 URL 选择代理。fetch 的自定义 TLS 拨号专门承接上述覆盖语义，避免把“拨代理地址”的请求错误地改拨到 TIANA_GATEWAY_ADDRESS。

代理模式下只在本机解析代理 hostname；将未解析的物理目标 hostname 交给代理。目标证书继续按逻辑 Endpoint 校验，不按代理或覆盖地址校验。

### 3.4 SOCKS5 原生拨号

原生字节流复用已固定的 `golang.org/x/net/proxy`，由同一 Policy 选定并校验代理 URL，再使用 FromURL／SOCKS5 创建 Dialer。传入支持 ContextDialer 的 net.Dialer，通过 ContextDialer.DialContext 建立连接；不得通过不可取消的 Dial 加每次一个 goroutine 的包装实现。

不调用 `proxy.FromEnvironment`／FromEnvironmentUsing：它们的 ALL_PROXY、NO_PROXY、环境缓存及错误回退与 httpproxy 不同，不能让某些通道重新独立决定路由。也不注册全局 dialer scheme 或修改全局 HTTP Transport。

SOCKS5 握手负责无认证／username-password 协商和 CONNECT 目标地址，保留域名原样供代理解析；字面 IP 使用相应地址类型。仅支持 TCP CONNECT，不实现 BIND 或 UDP ASSOCIATE。认证失败、不支持的方法、非成功 reply、断流、超时均关闭本次连接并返回固定错误，不换认证来源、不再拨号、不回退直连。

返回成功后与 HTTP CONNECT 一样，目标 TLS/H2、Tiana 授权、业务数据流、取消及关闭继续由原有调用方负责。SOCKS5 的认证与目标连接 reply 都不代表 Gateway 接受。

## 4. 代理握手、安全边界与错误语义

HTTP/HTTPS 代理链路：

```text
CLI/helper
  → TCP 到代理
  → [HTTPS 代理时：验证代理证书并建立代理 TLS]
  → HTTP/1.1 CONNECT 物理目标
  → 验证目标 TLS，原生通道协商 h2
  → 原有 Gateway CONNECT / fetch HTTP
  → SQL、Git 或 Web 业务数据
```

SOCKS5 代理链路：

```text
CLI/helper
  → TCP 到 SOCKS5 代理
  → SOCKS5 方法／可选认证协商
  → SOCKS5 CONNECT 物理目标（hostname 交给代理解析）
  → 验证目标 TLS，原生通道协商 h2
  → 原有 Gateway CONNECT / fetch HTTP
  → SQL、Git 或 Web 业务数据
```

HTTP CONNECT 代理只需要透明转发字节，不要求理解隧道内 H2。HTTPS 代理的外层 TLS 使用代理 hostname 验证，最低 TLS 1.2；目标 TLS 继续维持现有要求。代理根信任默认系统根，CLI 将已有 invocation trust.Roots 显式提供给 ProxyRootCAs；标准 HTTP Transport 同样使用该次调用的可信根。此方案复用现有 `--ca-file` / TIANA_CA_FILE 能力，不新增独立代理 CA flag。SDK 不凭 Endpoint 推断部署信任。

手工 CONNECT 的约束：

- 请求只包含 CONNECT authority、Host 和必要代理认证；不附带 Tiana token、SQL、Git 数据、签名下载路径或发布身份。
- Basic 代理认证只从 proxy URL userinfo 构建，只发给所选代理。HTTP 代理上的 Basic 认证未加密；需要保密代理凭证的环境应使用 HTTPS 代理。
- 只接受一个 HTTP 200 建隧道回复；407、其他状态、重定向、畸形响应均失败。没有认证协商、重试或直连回退。
- 响应 header 区域上限 16 KiB、64 个字段；约束在分配和解析过程中生效。错误正文不读取展示，不无界排空。
- 成功后保留读取器已预读的隧道字节，不能丢弃或把它们作为 HTTP body 消费。
- 到代理 TCP、代理 TLS、CONNECT 和目标 TLS 共用调用方原有 setup deadline；不为各步骤重置一份完整超时。不得给长时间数据传输遗留 setup deadline。
- 取消能中断代理握手及 CONNECT 读取；成功交接时移除建立阶段的取消钩子，防止与返回竞争而误关连接。连接建立后的取消仍交给原有会话逻辑。
- 不增加每个数据包的代理处理，额外 header 缓冲有界；不增加无上限 goroutine 或队列。

SOCKS5 的握手和认证由官方库完成，输入字段在构造策略时有界校验。方法协商、认证、CONNECT、目标 TLS 共用原有 setup deadline，取消需能终止每个阶段；长连接传输不继承 setup deadline。该路径不建立到 SOCKS 代理的 TLS，SOCKS user/password 本身不受目标 TLS 保护；目标会话仍维持原有 TLS 校验。

Gateway 使用内层 `Proxy-Authorization: Bearer ...` 是已有协议；它与 HTTP 代理的同名 Basic header 必须在不同连接层构建。代理握手完成前不能发送内层 Gateway headers。SOCKS 认证使用独立二进制字段，不发送 Tiana token。普通 HTTP 标准 Transport 的代理认证与 origin Authorization 同样需要验证隔离。

outbound 提供固定错误分类，例如 configuration、proxy_connect、proxy_tls、proxy_auth、proxy_protocol、canceled、timeout。错误不携带原始 URL、userinfo、响应 status text、peer body 或任意底层错误文本。仅保留安全分类、协议类型、数字代理 HTTP status 与 context sentinel；不把代理 407 或 SOCKS 认证失败伪装成 Gateway 拒绝或账号过期。若官方 SOCKS 库只给出不稳定／未导出的错误类型，统一映射安全的代理建立失败，不通过匹配任意错误文本发明认证分类或泄露 net.OpError 内含的地址。

SDK Gateway 可增加本地 `Proxy` ErrorKind，并以独立 ProxyStatus 字段承载代理状态；原有 Error.Status/Code 仍只描述 Gateway。代理握手失败时 Committed=false。fetch 保持 ErrTransport 的 errors.Is 兼容性并可包装安全的 outbound 分类。CLI 可补充固定诊断说明，但不能让纯代理失败触发登录、换 token 或发布重试。

HTTP 代理 200 或 SOCKS 成功 reply 只表示外层隧道建立，不表示 Gateway 接受，更不表示 SQL/Git/Web 已成功。任何已发送业务请求之后的未知结果，继续按现有 pending、回执和 unknown 规则处理。

## 5. CLI 接入与跨进程传递

`internal/netconfig` 为命令 context 提供不可变网络设置及 HTTP 工厂。常规命令、Git remote-helper、内置 helper、更新 worker 的入口都需覆盖，不能只依赖普通命令的 root.Before。

接入点：

1. newAuthClient 注入 HTTPClient；独立登录 exchange 继承该配置，保留 25 秒等原有预算。
2. apppublish 构造 fetch.Config 时传 Network，保留 credential precedence、archive、publish_id 及状态输出。
3. SQLite SDKConfig 填充 Gateway.Network；不改变 SQL Session、事务或 parse-error 恢复。
4. Git configuration 与 connect 使用 context 中的策略；launcher 原样继承环境，stdout 继续只承载 Git helper 协议。
5. 文件上传下载、preview 的服务端文件代理、模板 init/share 下载和分享上传，统一使用独立的对象存储 HTTP 客户端。
6. 显式 version check 和 update worker 注入 HTTPClient；缓存、不阻塞锁、每日提醒额度及后台静默保持。

浏览器端从 bootstrap 取得地址后自行建立的数据库／文件连接不由 CLI 代理控制。preview 的管理和服务端文件出站属于本次范围；不能把这项改动宣称为浏览器已有网络限制的解决方案。

### 5.1 Go 内置 helper

这里的“原生子进程”特指 `tiana connect -- turso db shell ...` 启动的 Turso 进程，不是 CLI SQLite shell，也不是 Git remote-helper：

```text
Turso 原生子进程 → 本机 loopback listener → Go helper → 企业代理 → Tiana Gateway
                                      ↑
                         CLI 用控制管道交付授权与目标配置
```

adapter 将 Turso 的远程 URL 改为 helper 实际绑定的本机 URL。Turso 只负责本地 HTTP/WebSocket，helper 才负责公网 TLS/H2 CONNECT 和 Tiana 授权。因此 helper 需要代理设置；Turso 不应再按继承的 HTTPS_PROXY、ALL_PROXY 等把本地请求送给外部代理，也不应拿到代理认证信息。该过滤是两类子进程分工的一部分，不是在禁止原生 SQL 使用代理。

CLI 的 `sqlite shell` 不启动这个原生子进程，直接在 CLI 进程里使用 Go SDK；Git launcher／remote-helper 也直接继承调用环境后自行连接代理，不能套用 Turso 子进程的环境过滤规则。

首版通过受控环境传递快照，保持冻结的 contract 3 消息编码：

- 将 helperEnvironment 与 native child environment 的策略分开；不能直接在公共 allowlistedChildEnvironment 中放行代理。
- 主进程从已选中的快照生成规范化 HTTP_PROXY、HTTPS_PROXY、ALL_PROXY、NO_PROXY，以及必要的 CGI 标记，只传给 Go helper。保留原配置来源，不把 ALL_PROXY 的回退复制伪装成 HTTP_PROXY；剔除大小写重复项，不重新读取可能变化的 os.Environ 作为代理来源。
- 原生 URL 的逻辑端口经 supervisor context 和仅给 Go helper 的私有 TIANA_INTERNAL_HELPER_NETWORK_PORT 环境字段传递；不扩充 contract 3 CONFIG，原生子进程仍过滤全部 TIANA_ 变量。该字段只在 helper 入口使用，由 SDK 校验端口；SDK 自身不读环境。非 443 目标按该端口匹配 NO_PROXY，Gateway authority 保持 443。
- helper 自己构造同一显式 Policy，注入 tiana.Config；不能尝试从账号或 token 推导代理。
- native child 只连接本地 helper，继续不接收代理变量、代理凭证、Tiana credential 或控制管道；本地通信不走代理。
- 代理 URL 可能包含凭证，继承到 helper 环境是本方案的明确取舍：只在已有同用户父子进程边界内传递，不写 argv、文件或日志。若必须消除这类环境传递，需要另行版本化私有控制协议，不在本次暗加字段。
- 保留 HELLO 到 SERVING 顺序、owner 退出、DRAIN、pidfd/kqueue、会话上限和凭证清理；更新环境断言测试。

helper 与主进程使用同一份静态配置，无需新增控制帧、匿名管道或持久化文件。原生子进程继续过滤四组代理变量及已有的 SOCKS_PROXY、TURSO_PROXY、LIBSQL_PROXY 等路由变量；仅连接本机 helper。当前首版不读取 SOCKS_PROXY 等非标准变量作为自己的代理来源。

### 5.2 Rust 仓库核查与旧打包入口

[sdk-rust/AGENTS.md](../../sdk-rust/AGENTS.md) 明确记录已移除 companion helper，要求 CLI 使用 Go BuiltinHelperLauncher，并禁止恢复 Rust binary、private helper framing、local sockets、classifier 与 adapters。[Cargo.toml](../../sdk-rust/Cargo.toml) 和 [README](../../sdk-rust/README.md) 与此一致。当前克隆包含通用 Rust SDK，没有可直接修改的 Rust helper 源码。本次没有获取旧 helper 的原始源码版本，不能声称对旧二进制做了源码审核。

据此，本方案不新增第二套 Rust helper，不在 Rust 中重复实现 CLI 的环境代理策略。CLI 代理功能没有 Rust 代码依赖；当前 npm 构建已采用正确的 Go helper 路径。

拟将旧 `build-release.sh` 和 `build-linux-bundle.sh` 的受支持构建入口统一到 Go 内置 helper，移除它们对不存在的 `../sdk`／`cargo --bin tiana-helper` 的依赖，调整对应 fixtures、manifest 和安装说明。涉及删除旧 bundle 布局时需保留已安装产物的安全卸载与路径验证，不自动卸载用户旧版本。是否退役旧嵌入模式本身作为独立审核项，不把它藏在拨号重构里。

在旧嵌入模式正式退役前，旧 artifact 及用户指定的外部 helper 不能被宣称支持新代理能力：若所选目标需要代理，启动原生客户端前明确报不支持；不通过默默改换 helper 绕过原有 hash/trust 选择。新的受支持构建必须显式选择 Go helper，并更新产物身份与完整性验证。

Rust SDK 本期不涉及修改。其 [client.rs](../../sdk-rust/src/client.rs) 当前直接 `TcpStream::connect` 的行为保持，不在本次增加代理配置、connector、依赖或发布要求。

## 6. 仓库与版本交付

| 仓库 | 计划修改 |
| --- | --- |
| sdk-go | 新 outbound 包及测试；Gateway Config/拨号与错误映射；fetch.Config、TLS 拨号、ResourceClient 透传；README、网络配置文档、AGENTS 决策 |
| CLI | internal/netconfig；上述命令和后台入口的注入；helper 环境分离、旧打包入口收敛与兼容拒绝；集成 fixture；README、Git 说明、AGENTS 决策；最终更新 SDK pin/checksums |
| sdk-rust | 本期不涉及修改。已核查当前没有 CLI helper，CLI 使用 Go 内置 helper |
| sdk-go-sqlite | 预计不改实现、不改变 v1.0.1 的 parse-error 修复；用 CLI 选定的新 sdk-go 进行真实 Session 集成回归。只有发现不透传或版本约束问题时再扩展修改范围 |

新增 Config 字段保持现有具名字段构造代码兼容；Go 外部非具名 struct literal 本身对新增字段敏感，发布说明需披露。nil 行为不变，不新增协议、存储 schema、持久化字段或 token 类型要求。

两仓库 AGENTS 已记录接口、资源边界、回滚与验证决定，并 supersede CLI 之前的 Git “no proxy”约束。用户已授权并完成 SDK PR 和 v1.0.1 tag 发布；CLI 提交、推送、合并、npm 发布及部署仍按独立授权边界执行。

开发验证可用仓库外临时 workspace／消费者模块组合源码，不修改产品 go.mod 为本地 replace，不修改模块缓存。可交付 CLI 必须在 SDK 新版本已发布后，以 GOWORK=off、远程固定版本和 Go 生成的 go.sum 验证。版本号在发布任务中决定；不预填未发布 tag，不改写 v1.0.0。SDK 的 v1.0.1 tag 已按用户新授权发布；CLI/npm 发布及部署仍需独立授权。

建议实施顺序：共享策略与 fixture → Gateway/fetch → CLI HTTP/Git/SQLite → helper/worker/文件与模板 → 全部验证 → 单独授权 SDK 发布 → 更新 CLI pin 并验证远程消费者 → 单独授权 CLI 发布。

## 7. 验收条件

### 7.1 统一网络 fixture

创建本地 HTTP CONNECT、HTTPS CONNECT 和 SOCKS5 代理，以及合成 TLS/H2 Gateway、HTTP MGR、对象存储和 registry。SOCKS fixture 同时提供无认证和 username/password 模式，按收到的域名请求映射到目标。目标使用 example.test 域名，客户端无法直接解析该目标。所有凭证及证书为合成数据。

每个通道都必须验证“只通过代理可达”，而不是仅断言选择函数返回一个 URL。记录 HTTP/SOCKS 的握手次数和目标侧请求次数，fixture 保证直连无法到达目标。另以隔离网络环境验证真正禁止直连的条件，区分代码 fixture 和操作系统网络验收。

| 维度 | 必须验证 |
| --- | --- |
| 选择 | 无代理、大／小写、冲突、空值、仅 HTTP_PROXY、仅 ALL_PROXY、专用代理高于 ALL_PROXY、被覆盖的非法配置、CGI、快照不变；四种代理 URL scheme |
| 绕过 | NO_PROXY 域名／子域／端口／IP／CIDR／`*`、localhost、IPv4/IPv6 loopback、物理 loopback override、非 loopback override |
| DNS 与身份 | 本地不解析目标；HTTP/SOCKS 代理可解析；socks5 与 socks5h 都发送 hostname；字面 IP 类型；SNI 与 authority 正确；物理覆盖不改变身份；本机代理入口不导致目标绕过 |
| TLS | HTTP/HTTPS 代理；代理证书错误；目标证书错误；自定义 CA；TLS 1.3/H2 保留；HTTPS 代理外层与目标内层信任独立校验 |
| 认证 | HTTP 无认证、Basic、407；SOCKS 无认证、username/password、错误凭证、不支持方法；解码后空值／255／256 字节边界；代理仅见代理凭证，目标仅见 Tiana 凭证，对象存储不带管理凭证 |
| 边界与取消 | 超大／畸形 HTTP header、过多字段、非 200、重定向、慢 header；SOCKS 畸形方法／版本／地址／reply，协商／认证／CONNECT 各阶段停顿；Ctrl-C、成功交接竞争、EOF、deadline 清理、预读字节保留 |
| 故障 | 代理拒绝／失联、连接中断、响应丢失：不回退直连、不增加重试；检查 socket/goroutine 泄漏 |

以仅配置 ALL_PROXY=socks5h://... 的 fixture 覆盖所有产品通道；另用 HTTP/HTTPS 专用代理与 ALL_PROXY 冲突的组合验证真实路径。SOCKS5 与 HTTP CONNECT 均需覆盖 Gateway/fetch/Git/helper 建连，不能仅靠普通 HTTP 客户端成功宣称原生通道已覆盖。

### 7.2 产品通道回归

- 登录 start/resume/exchange、账号刷新、管理分页与 JSON；exchange 不因代理故障重复执行。
- Web 实际 PUT 与 current/receipt 查询，publication unknown 后保留 publish_id/archive；上传不等于激活。
- SQLite `-e`、脚本、交互 session；BEGIN → 合法写入 → SQL_PARSE_ERROR → 后续查询／ROLLBACK 连续性；中途断链不得重放；保留 BATON_INVALID 的现有例外。
- Git clone/fetch/push、较大对象跨流控窗口、半关闭、取消、推送未知结果、一次 invocation 只建立一次外层连接。
- connect 使用真实原生客户端与 Go helper 子进程；保持 contract 3、loopback、owner/drain；确认 helper 收到 ALL_PROXY/NO_PROXY 而 Turso 无代理变量，只有 helper 发起 SOCKS/HTTP 代理握手；旧外部 helper 的代理拒绝须有 fixture。
- 文件 GET/PUT、模板 ZIP、分享包下载和上传、preview 服务端文件转发的代理路径及凭证隔离。
- 更新检查两个包共用两秒预算、后台 worker 代理继承、无额外 stdout、辅助失败不影响主命令。
- fmt、JSON、stderr 和 fixture 错误的凭证 canary；纯本地配置错误不伪造 request ID，已有业务 request ID 不丢失。

### 7.3 工程检查与远程验收

两仓库运行 GOWORK=off 的 `go test -mod=readonly -race ./...`、`go vet -mod=readonly ./...`、`go mod verify` 及格式检查；CLI 运行公共源码扫描和 PEM/hex 正反回归，SDK 运行自身 source-tools／public-source 检查与独立 consumer smoke。

CLI 另外执行 Node packaging/launcher fixtures、helper 协议和进程 fixtures、PTY/浏览器相关现有回归；构建 darwin amd64/arm64、linux amd64/arm64/riscv64、windows amd64/arm64。平台运行能力与仅交叉编译结果分别记录，不把 cross-build 当作该平台代理验收。

实现后的单独线上验收应使用一台禁公网直连的机器，分别通过可达公网的 HTTP CONNECT 及 SOCKS5 代理，覆盖真实 MGR/Gateway/App/SQLite/Git/Web/object storage；真实写入和发布需要其任务明确授权。至少有一次只设置 ALL_PROXY 的验收，另验证专用代理覆盖和 NO_PROXY。npm 新鲜安装及远程 pinned SDK 解析属于独立交付门槛，不能由本地 fixture 替代。

## 8. 兼容性、回滚与审核重点

未配置代理：保持既有目标、信任、凭证、请求身份、直连及会话行为。已配置 HTTPS_PROXY 或 ALL_PROXY：原先忽略代理的通道将开始走代理；仅 ALL_PROXY 也将影响原先只读取 HTTP(S)_PROXY 的管理、下载和更新通道，这是本次明确新增的行为。NO_PROXY 和本地绕过规则必须一致。

代理故障不会触发新增直连或业务重放；原有 SDK 401 刷新、SQLite 明确未执行的恢复、用户显式回执恢复仍按原合同运行。恢复时允许用户修正网络配置，不因代理地址变化创建新的业务身份。

回滚选择旧 CLI 和 SDK 固定版本，不回退 schema 或迁移账号文件。回滚会失去新增通道的代理能力，禁公网机器可能再次无法使用这些通道；不可通过删除 pending 或重发未知写入“修复”。

本次实现的范围和审核重点：

1. 已确认首版包含 HTTP/HTTPS 代理、SOCKS5、ALL_PROXY；不加入 PAC。SOCKS4／UDP、GUI 代理、WPAD、系统认证不在范围内。
2. 代理选择、校验、认证边界、DNS、超时及错误映射；SDK 使用显式 Network Policy，nil 保持旧行为，CLI 才读取环境。
3. NO_PROXY 使用逻辑目标、显式物理 loopback 覆盖始终直连。
4. Go helper 用受控环境快照传递四组代理设置，保持 contract 3；原生 Turso 子进程不继承。
5. 统一采用 Go 内置 helper、收敛遗留 Rust 打包入口；旧嵌入产物不能静默忽略代理，Rust SDK 不恢复 helper。
6. SDK 先发布新不可变版本，CLI 后更新远程依赖；SQLite SDK 先验证透传，不预先改版本或实现。提交、发布及部署仍需独立授权。

## 9. 依赖选型与实现边界

本次采用标准库和项目已固定的官方 x/net 依赖，不引入综合代理框架、自动系统配置检测或 JS 运行时。代理协议与业务策略分别由以下组件负责：

| 能力 | 选型 | 自有代码职责 |
| --- | --- | --- |
| 普通 HTTP/HTTPS/SOCKS5 请求 | Go 标准 [net/http.Transport](https://pkg.go.dev/net/http#Transport) | 同一代理选择函数、trust、timeout、凭证／redirect policy |
| NO_PROXY 匹配 | 已固定的 [x/net/http/httpproxy](https://pkg.go.dev/golang.org/x/net/http/httpproxy) | 专用代理与 ALL_PROXY 优先级、配置严格校验、快照和 CGI 来源；不复制域名/CIDR 匹配 |
| 原生 SOCKS5 字节流 | 已固定的 [x/net/proxy](https://pkg.go.dev/golang.org/x/net/proxy) | FromURL／SOCKS5 与 ContextDialer 适配、DNS 和错误脱敏；不手写 SOCKS 协议 |
| 原生 HTTP CONNECT 字节流 | SDK outbound 中的小范围共享适配 | 标准库没有直接返回 CONNECT net.Conn 的公共 Dialer；有界握手、单次拨号、凭证隔离、预读字节 |
| CLI 各业务通道 | internal/netconfig 与现有业务模块 | 显式依赖注入、helper 环境分离、连接池所有权及不重放约束 |

普通 HTTP 与原生连接共享 Policy 的选择结果，分别使用适合其接口的协议实现；标准 HTTP Transport 下不再调用会自行代理的底层 Dialer，避免重复代理。Config、Target、返回 URL 及证书池的拷贝、并发安全和连接关闭规则按第 3 节执行。

`proxy.FromEnvironment`、`http.ProxyFromEnvironment` 及默认客户端只允许留在 SDK 的既有 nil 兼容路径；CLI 注入路径统一使用显式策略，避免独立的环境缓存和错误回退。SDK auth 直接使用者不传 HTTPClient 时的既有行为保持不变。

实现阶段不新增第三方依赖来完成 SOCKS5 或 ALL_PROXY；两仓库的现有 x/net pin 可覆盖此设计。若实际实现需要升级其版本，应单独列明原因和完整依赖差异，不能为取得代理功能随意改动 SQLite SDK pin。
