# Codex 消息图片展示修复

## 缺陷与变更

Codex 原生 userMessage.content 的 image/url 和 localImage/path 已能发送给模型，但 App 历史转换只提取文本并固定返回空 attachments；实时 started/completed 用户回显同样返回空附件。桌面 AttachmentList 还会把 data 图片链接拒绝为非同源链接。

- 新增 projection.CodexUserAttachments，共用于历史和实时事件，保留图片块和稳定附件标识。
- codex 与 codex-ssh 历史映射支持图片；绑定读取与未绑定只读分页共用映射。
- localImage 复用带 session_id 的 /api/v1/files/raw，保留原有目标连接解析与访问范围，不扩大文件读取权限。
- 桌面与移动端共用 attachmentHref：允许 PNG/JPEG/GIF/WebP base64 图片和同源文件接口；拒绝跨来源及 HTML/SVG data、javascript、file 链接。
- 原生消息和图片不另存 App SQLite。

## 验证

- 新增历史图片用例先失败（空 attachments），修复后通过；实时 started/completed 图片用例先失败，修复后通过；桌面 data 图片回归先失败，修复后通过。
- 最终图片、历史分页、文件读取与不持久化专项：Go 10 passed in 3 packages。
- 前端相关组件 33/33；全量前端 540/540（108 files），有现存 jsdom scrollTo 提示。
- TypeScript tsc -b、相关 Go 包 vet、大内核 build、指定文件 git diff --check 均通过。
- 真实 Chrome 隔离浏览器：桌面和移动端 2/2；使用标注 CODEX IMAGE FIXTURE 的 240x140 PNG，验证两张历史图片解码、点击预览、桌面多图切换、实时纯图片消息解码。截图人工查看确认缩略图可见。HTTP/WebSocket 数据为模拟，未访问生产聊天、数据库或凭据。
- 隔离 Go HTTP/gRPC 用例覆盖绑定历史和只读第二页，验证图片下载与原始字节一致、越界文件 403、无本地消息副本。
- 扩大后端三包测试未稳定全绿：初轮 178 passed/7 failed，后续两轮各 187 passed/1 failed，失败项目不同；撤去本次图片插入的 Go overlay 对照中，指定活动测试单独通过。未证明失败属于本次图片变更或既有缺陷。期间工作区相关测试亦有其他改动，不能将这些轮次当作固定基线。

## 交付范围与限制

源码及隔离构建完成，未替换安装目录、未发布、未重启前端壳或大小内核。没有逐条读取截图中的真实会话，不能声称这些真实图片已恢复。

本地路径图片仍要求文件存在且处于现有允许范围；跨来源 HTTP 图片链接保留为不可用提示，不自动代理外站图片。

测试构建：C:/Users/luwei/AppData/Local/hermes/cache/scratch/astrorder-app-images-fix.exe
前端隔离构建：C:/Users/luwei/AppData/Local/hermes/cache/scratch/codex-images-dist
浏览器验收：C:/Users/luwei/AppData/Local/hermes/cache/scratch/codex-images-browser/results.json
