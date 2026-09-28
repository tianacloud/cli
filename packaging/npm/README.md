# Tiana CLI

使用 `npm install -g @tianadb/cli` 安装。需要 Node.js 20 或更高版本；安装时从 [Gitee 发布仓库](https://gitee.com/tianacloud/cli-releases/releases) 下载当前平台的原生程序并验证校验和，无需 Go 或 Rust。

支持 macOS amd64/arm64、Linux amd64/arm64/riscv64、Windows amd64/arm64。Windows 安装需要系统 PowerShell；macOS/Linux 需要 tar。运行 `tiana --version` 核验安装。Git 功能需要另外安装 Git，`connect -- turso ...` 需要另外安装 Turso。

本地审核 tarball 也可用 `npm install -g /path/to/tiana-cli-VERSION.tgz` 安装，仍需访问 Gitee 下载该版本二进制。如果安装时禁用了脚本，执行 `npm rebuild -g @tianadb/cli --ignore-scripts=false` 补装。

管理端地址由启动 Agent 或 Shell 时设置的 `TIANA_API_ORIGIN` 提供，技能继承环境，不按命令注入或覆盖；私有 CA 使用 `TIANA_CA_FILE` 或 `--ca-file`。包不内置部署地址或 CA，默认验证 TLS。

对话中运行 `tiana login --start --no-open --json` 并向用户展示 `data.verification_uri`。授权后执行 `tiana login --resume --json`，成功再执行 `tiana status`。普通终端可用 `tiana login`。

数据库：`tiana sqlite create NAME --wait`、`tiana sqlite shell INSTANCE -e 'SELECT 1'`。源码：`tiana git create NAME --wait`，使用返回的远程地址进行普通 Git 推送。连接读取已有账号登录态，不保存本地实例 Token；过期时重新登录。

应用：`tiana web serve --dir DIST`、`tiana web create NAME`、`tiana web upload ID --version VERSION --dir DIST --json`。遵循配套 Tiana Skill 的模板、哈希路由和清单。

应用列表：`tiana web list [--json]`。删除：`tiana web delete ID_OR_NAME`，非交互需 `--force`，可用 `--wait --json` 等待源站文件清理；关联 SQLite、Git 保留。
