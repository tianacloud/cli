# 模板素材文件

本地初始化、普通 Web 发布和 Console 模板安装都使用同一组应用相对路径。
包含素材的格式版本 2 安装包需要目标服务端已部署并启用 `template_files_v1`。
兼容版 C 会拒绝这种包；CLI 的版本号和本地打包成功不代表目标环境已经支持安装。

## 准备素材

JavaScript、CSS 和代码依赖随 Web 构建。随应用分发的静态小图片，按优化后、
base64/data URL 编码前的实际字节数计算，单张不超过 16,384 字节时内嵌到 JS。
其他需外置的图片、附件、音视频、字体和模型使用文件存储。用户上传文件不论大小
都使用文件存储，业务记录与文件关联信息使用数据库。平台必需的 `icon.png` 和
自包含市场预览沿用各自的随包规则。

在原有 `metadata.json` 中增加 `"file_seeds": "template-files"`，保留原来的名称、
数据库需求及变量声明。把可公开、安装必需的文件放在工程根部 `template-files/`：

```text
metadata.json
template-files/images/cover.webp
template-files/documents/guide.pdf
src/
showcase/
```

整个目录参与安装：1..256 个普通文件，单个和总计均不超过 16 MiB。
路径使用 UTF-8，相对路径不超过 1,024 字节；拒绝软链接、特殊文件、重复路径、
文件/目录冲突、绝对路径、`.`/`..`、反斜线、控制字符和百分号编码。
没有外置素材时省略 `file_seeds` 和目录，保持无素材流程。

| 位置 | 同一文件的路径 |
| --- | --- |
| 源码 | `template-files/images/cover.webp` |
| 安装包的独立材料 | `files/images/cover.webp` |
| 应用与数据库 | `template-assets/images/cover.webp` |
| Console 和 CLI 的租户文件目录 | `WEB_ID/template-assets/images/cover.webp` |

`WEB_ID` 替换成本次创建回执中的真实 `data.id`。数据库不保存 Web ID 前缀、作者
文件 URL、临时授权 URL 或 OSS key。程序通过当前应用的 `context.files` 读取：

```js
export async function mount(root, { files }) {
  const image = document.createElement('img');
  image.alt = '封面';
  root.append(image);
  const unbind = await files.bindImage(image, 'template-assets/images/cover.webp');
  return () => unbind();
}
```

## 从源码创建自己的应用

`tiana web init-template NAME --dir NEW_DIR` 或 `init-share-template LINK --dir NEW_DIR`
只校验下载并原样解压。读取工程说明，创建所需资源并保存真实 Web ID，完成变量
替换和数据库初始化。首次准备这个新应用时，使用现有文件命令逐项上传已审查素材：

```sh
tiana file put ./template-files/images/cover.webp WEB_ID/template-assets/images/cover.webp --json
tiana file put ./template-files/documents/guide.pdf WEB_ID/template-assets/documents/guide.pdf --json
tiana file list WEB_ID/template-assets/ --json
tiana file get WEB_ID/template-assets/images/cover.webp ./verified-cover.webp --json
```

下载目标必须不存在。逐项核对大小和 SHA-256 后再普通 `web publish`。
本地 `web serve WEB_ID` 可以在发布前读取这个 Web ID 的文件目录。
上传响应未知时保留返回的操作标识，检查原上传；不要用 `--overwrite` 重试未知写入。
后续修改或重新发布 Web 包不再次灌入素材，也不自动补回用户改动或删除的文件。

## 制作可安装模板

从同一个确定提交导出源码并在干净目录构建；素材的源字节也必须来自该提交。
使用支持素材声明的打包器，按其实际帮助核对参数。格式 2 包的 `source.zip` 保留
`template-files/` 供服务端核对，同时另有 `files/` 的实际字节。安装者选择保留源码
或直接安装，都会从独立材料安装素材；直接安装不创建 Git 源码库。
发布者选择 `--without-source` 的分享包仍限于无素材旧格式，不能跳过格式 2 的
字节核对或默默改成公开源码。每个条目的路径、大小、SHA-256 和 Content-Type
应与素材一致。
文件清单按 UTF-8 字节顺序排列，清单摘要取安装清单中 `files` 对象的原始 UTF-8
字节，不能重新序列化后计算。

检查干净构建和 `site.tweb`：外置素材不得被复制到 `dist`、改名后混入 Web 包，
或重新内嵌到 JS。小图片确实进入 JS，平台图标仍按要求保留。检查源码和独立材料
逐字节相同，合成 SQL 只保存上述应用相对路径，不携带作者文件或私人记录。
无素材包省略 `format_version`、`requires` 和 `files`，不增加空文件步骤。

上传分享包使用已有 `publish-share-template WEB_ID --file install.zip --wait --json`，
响应未知先 `status-share-template WEB_ID --json` 核对原发布，不把再次上传当作恢复。
完整服务端保留发布历史及旧版材料；临时上传包仍会清理，历史材料仍占发布者额度。

## 安装验收与恢复

在目标环境分别验证保留源码/直接安装与桌面/手机四种组合。检查安装完成回执、
真实 Web ID、文件数量、字节与摘要，使用上面的 CLI 路径读回同一份文件。
首次打开、刷新、重开和另一授权会话都能读取；不同安装的数据与文件相互独立，
其他未授权租户无法读取。另验证无素材包跳过文件步骤、无需文件预留仍能安装。

安装失败或响应未知时，从 Console 原安装记录刷新或重试，沿原根任务核对状态。
模板发布新版后，旧任务仍使用原版材料；不能新建任务来替代未知结果，或把最新
版本当作旧任务输入。用户修改/删除已安装文件、应用改名和普通 Web 发布都不会
触发素材重新灌入。这里没有新增 CLI 安装或恢复命令。
