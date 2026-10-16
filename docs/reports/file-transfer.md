# 文件浏览器复制与下载修复

本次修复文件浏览器本地／远程复制失败和下载打开空白加载窗口的问题，已发布 App、桌面 EXE、浏览器静态资源，未提交或推送代码。

## 修复

- HTTP 文件系统 resolver 传入配置的本地暂存根，解决远程复制返回 `Local staging storage is not configured`。
- 桌面复制先请求暂存（`copy_to_clipboard:false`），再通过 Wails bridge 写文件剪贴板，避免先在未配置的后端失败。Windows 后端也接入文件剪贴板，供浏览器端调用。
- 远程每次复制创建独立暂存批次，避免重复复制同名文件返回 409，并保留先前剪贴板引用的文件。
- 远程导出解析结束后读到 EOF 并确认 helper 成功退出，避免关闭未结束 SSH 管道造成 502；失败批次删除。
- 文件树及其他使用相同下载方式的查看器共用认证下载入口。桌面使用原生另存为和流式保存，浏览器使用认证 fetch 后保存 Blob。HTTP 错误显示失败通知。目录下载暂未实现，菜单禁用目录下载。
- 原生下载只允许相对 `/api/v1/files/raw` 路径，拒绝跨源地址与重定向，临时文件写入成功后替换目标；失败保留原目标。
- Windows 桌面剪贴板路径先规范化并检查存在性，PowerShell 错误正确传播。

## 验证

- 前端 609 项通过，类型检查和 lint 通过。
- 本次涉及的 Go files、targetfs、httpapi 三包：74 项通过，2 项跳过；vet 和构建通过。此处不宣称整个混合工作区 App 全量回归通过。
- 桌面：25 项通过，2 项跳过，vet 和 release 构建通过。
- 重复复制回归：先复制内容 first，再将源改为 second 并复制；两批路径不同，文件名相同，各自内容正确。helper 退出延迟测试覆盖 EOF 等待。
- 安装前端真实浏览器点击：隔离本地及 SSH 接口，复制传递正确路由与路径，下载 4 字节回读一致，没有弹出窗口。此项使用接口／剪贴板 fixture。
- 隔离真实 Wails WebView 调用生成的 SaveDownload binding，认证 HTTP 取回并落盘 19 字节，回读一致。临时 Go overlay 自动指定目标，未自动操作原生保存对话框。
- 真实 Wails 文件剪贴板写入后用 Windows API 回读成功。
- 已安装真实文件：本地 AGENTS.md（1,066 字节）及远程 `/home/luwei/workspace/OpsCore/deploy/k8s/configmap.yaml`（12,319 字节）暂存与下载内容完全一致，Content-Disposition 为 attachment；相同远程文件重复请求成功。
- 已安装后端本地／远程复制均返回 clipboard_set:true，Windows 系统剪贴板文件列表与返回路径一致。验收后恢复捕获的剪贴板数据。
- 安装 App 和 Desktop EXE 哈希与候选一致，113 个静态文件逐个哈希一致，health 正常，独立发布 worker 已退出。
- 小内核 PID、创建时间和可执行文件哈希保持不变，114 个后代进程身份全部保留。

## 交付边界

已发布到 `P:/DevApp/Astrorder`，未重建安装包，未提交／推送代码。原生保存对话框尚无人工点击验收；认证取文件、native binding、落盘与失败保留目标已有执行证据。

验收原始 JSON 和日志位于 `C:/Users/luwei/AppData/Local/hermes/cache/scratch/file-transfer-fix/`。
