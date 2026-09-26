# Tiana CLI

此预发布包包含 macOS arm64、Linux amd64 的原生 Go CLI 和 Git 启动器。安装时无需下载额外可执行文件，也不编译 Rust。使用 `npm install -g @tianadb/cli@0.2.1-beta.0` 安装；此版本使用 `next` 标签。

本地审核 tarball 也可用 `npm install -g /path/to/tiana-cli-VERSION.tgz` 安装。用 `tiana --version`、`tiana login --help`、`tiana apps --help` 核验功能。

管理端地址可由 `--config /path/to/tiana/config.json` 中的 `managementOrigin` 提供，也支持 `TIANA_MGR_ORIGIN`；私有 CA 使用 `TIANA_CA_FILE` 或 `--ca-file`。包不内置部署地址或 CA，默认验证 TLS。

对话中运行 `tiana login --start --no-open --json` 并向用户展示 `data.verification_uri`。授权后执行 `tiana login --resume --json`，成功再执行 `tiana status`。普通终端可用 `tiana login`。

数据库：`tiana sqlite create NAME --wait`、`tiana sqlite shell INSTANCE -e 'SELECT 1'`。源码：`tiana git create NAME --wait`，使用返回的远程地址进行普通 Git 推送。连接读取已有账号登录态，不保存本地实例 Token；过期时重新登录。

应用：`tiana apps serve --dir DIST`、`tiana apps create --project ID --name NAME`、`tiana apps upload --project ID --version VERSION --dir DIST --json`。遵循配套 Tiana Skill 的模板、哈希路由和清单。

构建：分别执行 `node scripts/build-platform.mjs darwin-arm64 ASSETS` 与 `node scripts/build-platform.mjs linux-amd64 ASSETS`，然后 `node scripts/package-cli.mjs ASSETS OUTPUT`。输出 tarball 和校验和，不上传或发布。
