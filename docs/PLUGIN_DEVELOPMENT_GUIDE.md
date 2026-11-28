# 星序（Astrorder）插件开发手册

> 适用 SDK 版本：`@astrorder/sdk` v1（`schema_version: 1`）

星序提供**全栈可扩展插件体系**：一个插件可以同时注册前端页面、自定义文件扩展名查看/编辑器、对话消息拦截与渲染增强、侧边栏面板、独立后端进程/HTTP 服务、Agent 可调用工具能力（CLI + `SKILL.md`）以及异常诊断信号。启用即生效，停用即卸载，且全程支持热重载与错误隔离。

---

## 目录

1. [架构总览与双轨前端模型](#1-架构总览与双轨前端模型)
2. [插件目录结构与 `plugin.json` 完整清单](#2-插件目录结构与-pluginjson-完整清单)
3. [权限模型与安全策略](#3-权限模型与安全策略)
4. [文件扩展名注册与多查看器「打开方式」](#4-文件扩展名注册与多查看器打开方式)
5. [前端 `@astrorder/sdk` v1 完整参考](#5-前端-astrordersdk-v1-完整参考)
6. [对话事件流与消息管线钩子](#6-对话事件流与消息管线钩子)
7. [跨插件通信总线、服务注册与 `depends_on`](#7-跨插件通信总线服务注册与-depends_on)
8. [沙箱 iframe Bridge SDK (`bridge.js`) 参考](#8-沙箱-iframe-bridge-sdk-bridgejs-参考)
9. [后端服务与宿主 Host API（含工作区文件沙箱）](#9-后端服务与宿主-host-api含工作区文件沙箱)
10. [Agent 能力、技能文档与诊断信号](#10-agent-能力技能文档与诊断信号)
11. [插件商店（Marketplace）与私有源协议](#11-插件商店marketplace与私有源协议)
12. [构建、打包、Ed25519 签名与热调试](#12-构建打包ed25519-签名与热调试)
13. [完整实战模板（3 套即拷即用）](#13-完整实战模板3-套即拷即用)

---

## 1. 架构总览与双轨前端模型

```text
┌─────────────────────────────────────────────────────────────────────────┐
│                         Astrorder 浏览器 / Wails 宿主                     │
│  ┌───────────────────────────────┐   ┌───────────────────────────────┐  │
│  │  ESM 同页热加载插件             │   │  iframe 沙箱插件               │  │
│  │  - Scoped Import Map 共享依赖  │   │  - sandbox="allow-scripts..." │  │
│  │  - 原生 React/Mantine 零缝集成 │   │  - bridge.js postMessage 桥接  │  │
│  │  - PluginErrorBoundary 隔离   │   │  - 无需打包，纯 HTML/JS 可运行  │  │
│  └──────────────┬────────────────┘   └──────────────┬────────────────┘  │
│                 │  /api/v1/ext/<id>/_esm/*          │ /api/v1/ext/<id>/_ui/*
└─────────────────┼───────────────────────────────────┼───────────────────┘
                  ▼                                   ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                 app-server-go (pluginhost.Manager)                      │
│  • 清单校验 / 权限门禁 / Ed25519 签名校验 / 内容摘要热刷新                 │
│  • 反向代理: /api/v1/ext/<id>/*  ──▶  插件后端进程 (process) 或 HTTP 服务  │
│  • 宿主 API: /api/v1/plugin-host/* ◀── X-Astrorder-Host-Token 鉴权       │
│  • 商店聚合: /api/v1/extensions/{registries,marketplace,install-remote}  │
└─────────────────────────────────────────────────────────────────────────┘
```

### 两种前端运行模式如何选择？

| 特性 | 同页热加载 ESM (`frontend.mode: "esm"`) | 沙箱 iframe (`frontend.mode: "iframe"`) |
| --- | --- | --- |
| **适用场景** | 深度集成宿主 UI、独立导航页面、自定义文件查看/编辑器、对话拦截器、Markdown 代码块渲染、跨插件协作 | 零构建轻量工具、第三方不信任脚本、复用独立 HTML/Web 应用 |
| **依赖复用** | 通过 Scoped Import Map 零重复加载宿主 `react`、`@mantine/core`、`react-router-dom`、`@tanstack/react-query`、`@astrorder/sdk` | 独立 DOM/JS 上下文，通过 `/api/v1/ext/_sdk/bridge.js` 与宿主通信 |
| **安全隔离** | 办公模式仅放行官方或 Ed25519 签名插件；开发者模式经授权后加载；渲染由 `PluginErrorBoundary` 隔离 | `sandbox="allow-scripts allow-forms"` 严格隔离，仅可请求自身 `/api/v1/ext/<id>/` 与当前打开文件 |
| **文件类型支持** | `ctx.registerFileHandler()` / `ctx.registerViewer()` | 在 `plugin.json` 中声明 `contributes.viewers[].ui_entry` + `bridge.js` `readFile()`/`saveFile()` |

---

## 2. 插件目录结构与 `plugin.json` 完整清单

### 典型插件包结构

```text
my-plugin/
├── plugin.json           # 必填：插件元数据、权限、前后端入口与贡献点
├── ui/
│   ├── entry.js          # ESM 模式入口（Vite 构建输出）
│   ├── entry.css         # 可选：ESM 样式表（启用自动注入，停用自动移除）
│   └── viewer.html       # iframe 模式静态页面入口（可引入 /api/v1/ext/_sdk/bridge.js）
├── backend/              # 可选：独立后端可执行程序或 Python/Node 脚本
│   └── server.py
└── skills/               # 可选：注入给会话 Agent 的技能目录
    └── my-skill/
        └── SKILL.md
```

### `plugin.json` 完整 Schema

```json
{
  "schema_version": 1,
  "id": "acme-csv-pro",
  "name": "高级表格与数据透视",
  "version": "1.2.0",
  "category": "office",
  "description": "为 .csv / .tsv / .parquet 提供可视化透视与 SQL 筛选",
  "author": "Acme Team",
  "homepage": "https://example.com/acme-csv-pro",
  "icon": "table",
  "default_enabled": false,
  "depends_on": ["base-chart-engine"],
  "permissions": [
    "ui:slots",
    "workspace:files.read",
    "workspace:files.write",
    "storage:kv"
  ],
  "backend": {
    "type": "none"
  },
  "frontend": {
    "mode": "esm",
    "entry": "ui/entry.js",
    "styles": ["ui/entry.css"],
    "sdk_version": "^1.0"
  },
  "settings": {
    "mode": "schema",
    "schema": [
      {
        "key": "pageSize",
        "label": "每页行数",
        "type": "number",
        "default_value": 100,
        "min": 10,
        "max": 1000,
        "step": 10
      },
      {
        "key": "apiKey",
        "label": "外部服务 API Key",
        "type": "secret",
        "required": false
      }
    ],
    "test_endpoint": "/test-connection"
  },
  "contributes": {
    "pages": [
      {
        "id": "dashboard",
        "title": "数据透视",
        "path": "/ext-page/acme-csv-pro/dashboard",
        "order": 150,
        "hide_in_office_mode": false
      }
    ],
    "viewers": [
      {
        "id": "acme-csv-pro-viewer",
        "title": "高级透视表格",
        "description": "支持透视表与条件高亮",
        "extensions": [".csv", ".tsv", ".parquet"],
        "mime_types": ["text/csv", "application/vnd.apache.parquet"],
        "badge_label": "[透视表]",
        "tab_color": "#0ea5e9",
        "category": "office",
        "priority": 94,
        "read_only": false,
        "default_for_extensions": true,
        "hide_in_office_mode": false
      }
    ],
    "ui_slots": [],
    "sidecar_panels": [],
    "agent_capabilities": [],
    "agent_skills": [],
    "signal_providers": []
  }
}
```

### 字段校验约束

- `id`：匹配 `^[a-z0-9][a-z0-9._-]{1,63}$`，禁止包含 `..`，且不可为保留名 `_sdk`。
- `category`：`agent` | `development` | `office` | `workspace` | `diagram` | `media` | `runtime`。
- `depends_on`：可选字符串数组，声明前置依赖插件 ID（不可依赖自身；前端按拓扑波次优先加载依赖插件，自动检测缺失依赖与循环依赖）。
- `default_enabled`：仅对 `official` / `builtin` 插件生效；**第三方 `external` 插件永远默认停用**，必须由用户在界面显式授权启用。
- 所有相对路径（`frontend.entry`、`frontend.styles[]`、`contributes.viewers[].ui_entry`、`sidecar_panels[].ui_entry`、`settings.ui_entry`）严禁出现 `..`、盘符前缀、反斜杠或隐藏目录段（`.` 开头）。

---

## 3. 权限模型与安全策略

星序定义了 **10 项显式能力权限**。插件后端访问 `/api/v1/plugin-host/*` 时由 Go 宿主逐项鉴权；前端启用外部插件时，未获批权限将弹出 `PermissionGrantModal` 请用户逐项确认。

| 权限 ID | 风险等级 | 说明 |
| --- | :---: | --- |
| `ui:slots` | 低 | 注册界面槽位、面板或同页扩展入口 |
| `storage:kv` | 低 | 读写本插件隔离命名空间的 KV 配置存储 |
| `signals:provide` | 低 | 向工作复盘与诊断系统上报异常信号 |
| `workspace:git.read` | 中 | 读取当前会话工作区的 Git 远程地址、分支与 HEAD SHA |
| `workspace:files.read` | 中 | 通过宿主安全沙箱列举、查询、搜索与读取工作区内文件（限工作区目录内） |
| `agent:capabilities` | 中 | 向会话 Agent 暴露 CLI 工具命令与 `SKILL.md` |
| `workspace:files.write` | 高 | 通过宿主安全沙箱在工作区内创建目录或原子写回文件（写入完整审计日志） |
| `storage:secrets` | 高 | 读写本插件专属的加密凭据存储（仅本插件后端可见，前端仅见脱敏掩码） |
| `network:outbound` | 高 | 允许插件后端向外部服务或局域网发起 HTTP 请求 |
| `desktop:control` | 高 | 允许访问宿主机桌面截图、UI 控件树及键鼠模拟 |

### 办公模式 vs 开发者模式信任策略

- **官方插件 (`official`) 与内置插件 (`builtin`)**：直接受信任，办公模式与开发者模式均可按需启用。
- **第三方 ESM 插件 (`external`, `frontend.mode: "esm"`)**：
  - **办公模式 (`office`)**：仅允许带有有效 **Ed25519 信任签名** 的外部 ESM 插件；未签名插件会被后端 `/_esm` 端点拒绝（`403`）。
  - **开发者模式 (`developer`)**：用户显式授予 `ui:slots` 后允许加载未签名本地开发 ESM 插件（可通过策略 `plugin-security.allow_unsigned_esm_plugins = false` 关闭）。
  - **前端 API 对称鉴权**：外部 ESM 插件调用 `ctx.api.readWorkspaceFile` / `listWorkspaceFiles` 时校验 `workspace:files.read`；调用 `ctx.api.writeWorkspaceFile` 时校验 `workspace:files.write`；`ctx.api.fetch` 禁止直接访问宿主受保护管理接口（`/api/v1/auth`、`/api/v1/extensions`、`/api/v1/credentials`、`/api/v1/settings`、`/api/v1/plugin-host`），请使用 `ctx.api.fetchExt` 或 SDK 授权方法。
- **公共契约边界**：第三方插件请统一依赖稳定版本化的 `@astrorder/sdk`（v1）；`@astrorder/workspace-bridge` 属于官方一等工作区插件使用的内部桥接层，不对外提供 SemVer 兼容承诺。

---

## 4. 文件扩展名注册与多查看器「打开方式」

星序支持**任何插件为任意自定义扩展名（包括复合扩展名如 `.drawio.xml`、`.pb.json`、`.logfmt`）注册专属打开与编辑能力**。注册生效后，以下 4 处宿主界面会自动联动，无需刷新页面：

1. **右侧工作区文件树 (`FileTreeViewer`)**：自定义文件显示插件专属图标与主题色；双击使用最高优先级查看器打开；右键菜单自动出现 **「打开方式」** 子菜单供选择任意已注册查看器。
2. **Sidecar 标签页工具栏 (`SidecarHost`)**：当同一文件匹配 2 个及以上查看器时，工具栏自动出现 **「打开方式 / 切换查看器」** 切换按钮，可一键在专用插件视图与代码编辑器之间无缝切换。
3. **对话 Markdown 消息与附件列表**：消息正文里提到的已注册扩展名文件会自动识别为可点击的文件胶囊；点击直接由对应查看器在侧栏打开。
4. **插件热卸载**：停用或卸载插件后，其注册的扩展名与已打开的对应标签页会自动清理，文件自动回退至其他可用查看器（如 Monaco 代码编辑器）。

### 方式 A（推荐）：ESM 插件 `ctx.registerFileHandler(...)`

适用于需要完整 React + Mantine 交互或复杂渲染引擎（Canvas / WebGL / 图表 / 可视化编辑）的场景。

```tsx
import { useEffect, useState } from 'react'
import { Button, Group, Paper, Stack, Text, Textarea } from '@mantine/core'
import {
  defineAstrorderPlugin,
  type AstrorderPluginContext,
  type ViewerContext,
} from '@astrorder/sdk'

function MyNoteViewer({ artifact, toolbarAction, onSave, onDirtyChange }: ViewerContext) {
  const [content, setContent] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    let active = true
    if (!artifact.readUrl) return
    void fetch(artifact.readUrl, { credentials: 'include' })
      .then((res) => res.text())
      .then((text) => {
        if (active) setContent(text)
      })
    return () => { active = false }
  }, [artifact.readUrl])

  const handleSave = async () => {
    if (!onSave) return
    setSaving(true)
    try {
      const ok = await onSave(content)
      if (ok) onDirtyChange?.(false)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Stack h="100%" gap={0}>
      <Paper p="xs" withBorder radius={0}>
        <Group justify="space-between">
          <Text size="xs" fw={600}>{artifact.name}</Text>
          <Group gap="xs">
            {artifact.writable && onSave && (
              <Button size="compact-xs" loading={saving} onClick={() => void handleSave()}>
                保存
              </Button>
            )}
            {toolbarAction}
          </Group>
        </Group>
      </Paper>
      <Textarea
        styles={{ root: { flex: 1, display: 'flex' }, wrapper: { flex: 1 }, input: { height: '100%' } }}
        value={content}
        onChange={(e) => {
          setContent(e.currentTarget.value)
          onDirtyChange?.(true)
        }}
      />
    </Stack>
  )
}

export default defineAstrorderPlugin({
  activate(ctx: AstrorderPluginContext) {
    ctx.registerFileHandler({
      id: 'my-note-viewer',
      title: '灵感笔记编辑器',
      description: '预览与编辑 .snote / .note.json 文件',
      extensions: ['.snote', '.note.json'], // 自动标准化小写并补全前导点
      mimeTypes: ['application/x-snote'],
      defaultForExtensions: true,           // 设为该扩展名默认查看器（优先级 96+）
      canEdit: true,
      badgeLabel: '[灵感笔记]',
      tabColor: '#10b981',
      component: MyNoteViewer,
    })
  },
})
```

> **提示**：请在组件的顶栏中渲染宿主传入的 `toolbarAction`（如上例），这样宿主提供的 **「打开方式」切换器**、**「引用到输入框」** 和 **「在文件树中定位」** 按钮会自动出现在查看器右上角。

### 方式 B（零构建）：声明式 iframe 文件查看器

只需一个 `plugin.json` + 一个静态 `ui/viewer.html`，无需 Node.js / Vite 打包：

1. 在 `plugin.json` 中声明 `contributes.viewers[].ui_entry`：

```json
{
  "schema_version": 1,
  "id": "logfmt-viewer",
  "name": "Logfmt 结构化日志查看器",
  "version": "1.0.0",
  "category": "development",
  "backend": { "type": "none" },
  "frontend": { "mode": "iframe" },
  "permissions": ["ui:slots"],
  "contributes": {
    "viewers": [
      {
        "id": "logfmt-viewer",
        "title": "Logfmt 日志表格",
        "extensions": [".logfmt", ".jsonl"],
        "ui_entry": "ui/viewer.html",
        "read_only": true,
        "priority": 92,
        "default_for_extensions": true,
        "badge_label": "[日志]",
        "tab_color": "#f59e0b"
      }
    ]
  }
}
```

2. 在 `ui/viewer.html` 中引入 `/api/v1/ext/_sdk/bridge.js` 并调用 `readFile()` / `saveFile()`：

```html
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <script src="/api/v1/ext/_sdk/bridge.js"></script>
</head>
<body>
  <pre id="out">正在读取文件…</pre>
  <script>
    async function main() {
      const file = await window.astrorderPlugin.readFile({ encoding: 'utf8' });
      document.getElementById('out').textContent =
        `文件名：${file.name}\n类型：${file.mediaType}\n\n${file.content}`;
    }
    main().catch((err) => {
      document.getElementById('out').textContent = '读取失败：' + err.message;
    });
  </script>
</body>
</html>
```

### 匹配优先级与用户持久化默认偏好

1. **用户持久化默认偏好最高**：在 Sidecar 工具栏点击 **「打开方式 → 始终用「XXX」打开 `.ext`」** 后，该扩展名的首选查看器将持久化保存并在跨会话/重启后始终生效（可随时一键 **「恢复 `.ext` 自动选择」**）。
2. **复合扩展名优先**：如 `.drawio.xml`、`.note.json` 自动高于单扩展名 `.xml` / `.json`。
3. **权重排序**：自定义 `supports(artifact)` 返回分数 > `defaultForExtensions: true` (96) > 普通扩展查看器 (86~88) > 内置代码编辑器 `monaco-viewer` (80) > MIME 兜底匹配 (40~60)。
4. **非破坏性共存与工具栏复用**：多个插件注册同一扩展名不会互相覆盖；声明式 iframe 文件查看器由宿主自动装配顶栏与 `toolbarAction`，用户随时可在 iframe 查看器与代码编辑器之间一键来回切换。

---

## 5. 前端 `@astrorder/sdk` v1 完整参考

### 宿主 Import Map 共享模块

宿主页面内置 Scoped Import Map（作用域 `/api/v1/ext/`），ESM 插件构建时通过 `createAstrorderPluginConfig` 自动将以下依赖标记为 external，运行时零重复加载：

- `react`、`react/jsx-runtime`、`react-dom`、`react-dom/client`
- `@mantine/core`、`@mantine/notifications`
- `react-router-dom`、`@tanstack/react-query`
- `@astrorder/sdk`

> 其余第三方库（如 `three`、`echarts`、小型工具包或少量 SVG 图标）直接打包进插件自己的 `ui/entry.js`，与宿主彻底解耦。

### `AstrorderPluginContext`（`activate(ctx)` 注入）

所有通过 `ctx.register*` 或 `ctx.api.on*` 注册的资源都会自动加入 `ctx.subscriptions`，在插件停用或热重载时**按逆序自动释放**。

| 方法 | 说明 |
| --- | --- |
| `ctx.registerPage(spec)` | 注册独立导航页面（支持桌面路由、移动端路由、侧边栏图标与命令面板快捷项） |
| `ctx.registerViewer(spec)` | 注册底层 `ArtifactViewer`（完全自定义匹配逻辑与配置项） |
| `ctx.registerFileHandler(spec)` | 面向文件扩展名的便捷注册接口（自动标准化扩展名与优先级） |
| `ctx.registerSlot(spec)` | 注入底栏状态条、Git 菜单、Sidecar 欢迎菜单、自定义设置抽屉或内置面板 |
| `ctx.registerCommand(spec)` | 向全局命令面板 (`Ctrl+K`) 注册可执行动作 |
| `ctx.registerDatabaseTool(provider)` | 注册数据库工作台提供者（接管 `.sql` 文件与 SQL 代码块执行） |
| `ctx.registerMessageInterceptor(spec)` | 注册发送前消息拦截器（改写 / 附加元数据 / 阻断） |
| `ctx.registerMarkdownBlockRenderer(spec)` | 注册自定义 Markdown 围栏代码块渲染器 |
| `ctx.registerMessageDecorator(spec)` | 注册消息气泡头部/底部自定义组件装饰器 |
| `ctx.registerService(serviceId, impl)` | 向跨插件服务注册表发布强类型服务实例 |

### `ctx.api` 宿主能力汇总

- **网络与扩展后端**：`fetch(path, init)`、`fetchExt<T>(subpath, init)`（自动拼装 `/api/v1/ext/<pluginId>/...` 并携带用户鉴权）。
- **工作区文件（前端便捷封装）**：
  - `readWorkspaceFile(filePath, { sessionId?, workspace?, connectionId?, encoding?: 'utf8' | 'base64' })`
  - `writeWorkspaceFile(filePath, content, { sessionId?, workspace?, connectionId? })`
  - `listWorkspaceFiles({ path?, depth?, sessionId?, workspace?, connectionId? })`
- **工作台交互**：`notify(opts)`、`navigate(path)`、`openArtifact(artifact, viewerId?)`、`openPluginPanel(panelId, opts)`、`openSettings(opts)`、`quoteToComposer(session, text)`、`openExternal(url)`、`downloadFile(url, filename)`。
- **配置与状态**：`getConfig<T>()`、`onConfigChange(cb)`、`usePluginConfig<T>(key, fallback)`、`getActiveSession()`、`getSessions()`、`useCurrentSession()`、`useAppProfile()`、`useThemeMode()`。
  - **自动配置同步**：若插件在 `plugin.json` 中声明了 `settings.mode: "schema"`，宿主在调用 `activate(ctx)` 前自动拉取后端 KV 并合并 `default_value` 同步至前端配置仓库；用户在设置抽屉保存后也会实时触发 `onConfigChange` 与 `usePluginConfig` 重渲染。
- **事件与跨插件总线**：`onHostEvent`、`onSessionEvent`、`emitPluginEvent`、`onPluginEvent`、`getService`、`useService`。

---

## 6. 对话事件流与消息管线钩子

### 6.1 实时订阅宿主 WebSocket 事件流

```ts
ctx.api.onHostEvent('session.*', (event) => {
  console.log('会话状态变更:', event.type, event.session_id, event.data)
})

// 仅监听指定会话的全部实时事件（含 message.delta / message.upsert / task.upsert）
ctx.api.onSessionEvent(sessionId, (event) => {
  if (event.type === 'message.upsert') {
    // 响应新消息
  }
})
```

- 支持精确匹配（如 `'command.upsert'`）、前缀通配（如 `'message.*'`、`'learning.*'`）或全局通配 `'*'`。
- 每个监听器均运行在独立的 `try/catch` 中，插件抛错不会影响主事件流。

### 6.2 发送前消息拦截器 (`registerMessageInterceptor`)

```ts
ctx.registerMessageInterceptor({
  id: 'secret-guard',
  order: 50,
  async beforeSend({ text }) {
    if (/AKIA[0-9A-Z]{16}/.test(text)) {
      return {
        cancel: true,
        reason: '检测到疑似 AWS Access Key，已拦截发送，请先脱敏。',
      }
    }
    return { text: text.replace(/#today\b/g, new Date().toISOString().slice(0, 10)) }
  },
})
```

- 按 `order` 升序串联执行；单个拦截器默认 3 秒超时保护，超时或抛错自动跳过不阻塞发送。
- 返回 `{ cancel: true, reason }` 时中止发送并在输入框下方展示提示；返回 `{ text }` 时替换为改写后的文本。

### 6.3 自定义 Markdown 代码块渲染器 (`registerMarkdownBlockRenderer`)

```tsx
ctx.registerMarkdownBlockRenderer({
  id: 'vega-chart',
  title: 'Vega-Lite 图表',
  languages: ['vega', 'vega-lite'],
  priority: 120,
  component: ({ code, language, session }) => (
    <VegaChartCanvas specText={code} lang={language} session={session} />
  ),
})
```

- 自动包裹 `PluginErrorBoundary`，并在渲染块右上角提供 **「查看源码 / 切回视图」** 切换按钮。

### 6.4 消息气泡装饰器 (`registerMessageDecorator`)

```tsx
ctx.registerMessageDecorator({
  id: 'word-count-footer',
  position: 'footer', // 'header' | 'footer'
  order: 100,
  match: (message) => message.role === 'assistant' && message.text.length > 200,
  component: ({ message }) => (
    <span style={{ fontSize: 11, opacity: 0.65 }}>
      字数统计：{message.text.length} 字
    </span>
  ),
})
```

---

## 7. 跨插件通信总线、服务注册与 `depends_on`

### 发布 / 订阅跨插件事件

```ts
// 插件 A 发送事件（fromPluginId 自动填充为插件 A 的 ID）
ctx.api.emitPluginEvent('diagram.node.selected', { nodeId: 'svc-auth', file: 'auth.go' })

// 插件 B 订阅事件（支持精确主题或通配符 'diagram.*'）
ctx.api.onPluginEvent<{ nodeId: string; file: string }>('diagram.*', (payload, meta) => {
  console.log(`收到来自 ${meta.fromPluginId} 的事件:`, meta.topic, payload)
})
```

### 服务注册表与声明式依赖排序

1. **提供方插件 (`graph-core`)**：

```ts
export interface GraphService {
  focusNode(id: string): void
}

ctx.registerService<GraphService>('graph-core.api', {
  focusNode: (id) => console.log('focus', id),
})
```

2. **消费方插件 (`graph-insights`)** 在 `plugin.json` 中声明依赖：

```json
{
  "id": "graph-insights",
  "depends_on": ["graph-core"]
}
```

星序的 `EsmPluginLoader` 会使用拓扑排序按波次加载，确保 `graph-core` 完成 `activate(ctx)` 后再启动 `graph-insights`。在 React 组件中可使用响应式 Hook：

```tsx
import { useService } from '@astrorder/sdk'

function JumpButton() {
  const graph = useService<GraphService>('graph-core.api')
  return <button disabled={!graph} onClick={() => graph?.focusNode('node-1')}>定位节点</button>
}
```

---

## 8. 沙箱 iframe Bridge SDK (`bridge.js`) 参考

在 iframe 页面中引入 `<script src="/api/v1/ext/_sdk/bridge.js"></script>` 后，宿主自动挂载 `window.astrorderPlugin`：

| API | 返回值 | 说明 |
| --- | --- | --- |
| `getContext()` | `Promise<Context>` | 获取 `pluginId`、`theme`（`'light' \| 'dark'`）、`sessionId`、`agentId`、`workspace`、`connectionId`、`artifact`、`params` |
| `onContextChange(cb)` | `() => void` | 订阅主题、会话或参数变化 |
| `fetch(path, init?)` | `Promise<{ ok, status, text, json }>` | 代理请求本插件后端 `/api/v1/ext/<pluginId>/<path>`（自动携带宿主鉴权，禁止越权访问其他插件路径） |
| `readFile(opts?)` | `Promise<{ name, path, mediaType, writable, encoding, content }>` | 读取当前 iframe 文件查看器所打开的工件内容（支持 `{ encoding: 'utf8' \| 'base64' }`） |
| `saveFile(content)` | `Promise<{ ok: boolean }>` | 将编辑结果写回当前打开的可写文件（仅当工件 `writable: true` 且 `read_only !== true` 时允许） |
| `toast(message, tone?)` | `Promise<{ ok: boolean }>` | 弹出宿主通知（`tone`: `'info' \| 'success' \| 'warning' \| 'error'`） |
| `openPanel(panelId, params?)` | `Promise<{ ok: boolean }>` | 打开本插件声明的其他 Sidecar 面板 |
| `insertComposer(text)` | `Promise<{ ok: boolean }>` | 将文本引用插入当前会话输入框 |
| `resize(height)` | `Promise<{ ok: boolean }>` | 在自适应高度槽位中调整 iframe 高度 |

---

## 9. 后端服务与宿主 Host API（含工作区文件沙箱）

### 后端类型

- `none`：纯前端插件，不启动任何后端进程。
- `process`：由星序进程监管器托管（支持自动分配端口、健康检查、崩溃退避重启与随插件停用优雅退出）。
  - 注入环境变量：
    - `ASTRORDER_PLUGIN_ID`：插件 ID
    - `ASTRORDER_PLUGIN_PORT`：监听端口（绑定 `127.0.0.1`）
    - `ASTRORDER_PLUGIN_DATA_DIR`：持久化数据目录
    - `ASTRORDER_HOST_API_URL`：宿主 Host API 基址（如 `http://127.0.0.1:30001/api/v1/plugin-host`）
    - `ASTRORDER_HOST_TOKEN`：每次启动动态签发的随机 Bearer Token
- `http`：指向已运行的外部 HTTP 服务 URL（`backend.url`）。

### Host API 端点一览

调用方式：在请求头携带 `X-Astrorder-Host-Token: <ASTRORDER_HOST_TOKEN>`（或 `Authorization: Bearer <token>`）。

| 方法与路径 | 所需权限 | 说明 |
| --- | --- | --- |
| `GET /workspace/git-info?workspace=...&session_id=...&connection_id=...` | `workspace:git.read` | 获取工作区 Git 远端地址、分支、HEAD SHA |
| `GET /workspace/files/list?workspace=...&path=...&depth=1..4` | `workspace:files.read` | 列出工作区内目录项（支持本机 Windows / WSL / SSH 远程工作区） |
| `GET /workspace/files/stat?workspace=...&path=...` | `workspace:files.read` | 读取工作区内文件/目录元信息 |
| `GET /workspace/files/read?workspace=...&path=...&encoding=utf8\|base64\|raw` | `workspace:files.read` | 读取文件内容（单次上限 10 MiB，自动识别文本/二进制） |
| `GET /workspace/files/search?workspace=...&query=...&limit=50` | `workspace:files.read` | 在工作区内按文件名搜索（上限 200 条） |
| `PUT\|POST /workspace/files/write` | `workspace:files.write` | JSON `{ workspace, session_id, connection_id, path, content, encoding }` 原子写回文件（上限 10 MiB，自动记录审计日志） |
| `POST /workspace/files/mkdir` | `workspace:files.write` | JSON `{ workspace, session_id, connection_id, parent, name }` 在工作区内创建子目录（自动记录审计日志） |
| `GET\|PUT /storage/kv` | `storage:kv` | 读写本插件隔离 KV 存储 |
| `GET\|PUT /storage/secrets` | `storage:secrets` | 读写本插件隔离加密凭据 |
| `GET /sessions/context?session_id=...&agent_id=...` | `workspace:git.read` 或 `agent:capabilities` | 获取当前会话元数据上下文 |

> **安全边界**：所有 `/workspace/files/*` 路径均经过严格的 POSIX/Windows 工作区目录约束检查，任何 `../` 越界或指向工作区外部的绝对路径均直接返回 `403 Forbidden`。

---

## 10. Agent 能力、技能文档与诊断信号

1. **Agent 可调用能力 (`contributes.agent_capabilities`)**：
   - 声明 `id`、`cli_alias`、`summary`、`endpoint`、`input_schema`，会话中的 Agent 可通过 `astrorder-agent invoke <pluginId>.<capId>` 调用插件后端接口。
2. **技能文档 (`contributes.agent_skills`)**：
   - 将 `skills/<skill-name>/SKILL.md` 随插件打包并在清单中声明路径；启用插件后，星序自动将该技能同步至 Agent 技能目录，停用时自动撤回。
3. **异常诊断信号 (`contributes.signal_providers`)**：
   - 声明 `kind` 与 `endpoint`，供「工作复盘」与「成长雷达」拉取该插件检测到的失败任务或告警线索。

---

## 11. 插件商店（Marketplace）与私有源协议

进入 **「插件管理 → 插件商店」** 即可浏览官方与自定义仓库源中的扩展并一键安装/更新；点击 **「管理插件源」** 可增删企业内网或社区插件源。

### 插件源索引 JSON 格式

任意静态 HTTP/HTTPS 托管服务或本地 `file://` 文件均可作为插件源：

```json
{
  "version": 1,
  "name": "团队私有插件商店",
  "plugins": [
    {
      "id": "acme-log-viewer",
      "name": "结构化日志分析器",
      "version": "1.1.0",
      "category": "development",
      "description": "高亮展示 .logfmt 与 .ndjson 日志并支持字段过滤",
      "author": "Platform Team",
      "homepage": "https://git.internal.example/plugins/acme-log-viewer",
      "download_url": "https://artifacts.internal.example/plugins/acme-log-viewer-1.1.0.zip",
      "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
      "frontend_mode": "esm",
      "extensions": [".logfmt", ".ndjson"],
      "permissions": ["ui:slots", "workspace:files.read"]
    }
  ]
}
```

- 安装流程内置 **云元数据 SSRF 拦截**、**64 MiB 包体积上限** 与 **SHA256 完整性校验**，校验通过后以临时目录原子替换安装，并自动交由权限审批弹框确认。

---

## 12. 构建、打包、Ed25519 签名与热调试

### Vite 构建配置（ESM 插件）

复用仓库内的标准预设 `packages/astrorder-plugin-sdk/vite-preset.mjs`：

```ts
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createAstrorderPluginConfig } from '@astrorder/plugin-sdk/vite-preset.mjs'

const root = path.dirname(fileURLToPath(import.meta.url))

export default createAstrorderPluginConfig({
  root,
  entry: 'src/entry.tsx',
  outDir: 'dist',
  entryFileName: 'ui/entry.js',
  cssFileName: 'ui/entry.css',
  manifestFile: 'plugin.json',
})
```

### 官方开发命令行工具 (`astrorderctl plugin`)

星序内置 `astrorderctl plugin` CLI，覆盖从脚手架初始化、本地监听自动热重载、打包到 Ed25519 签名与验签的全生命周期。其中 `esm-viewer` 与 `chat-hook` 模板会自动生成自包含的 `package.json`、`tsconfig.json`、`src/astrorder-sdk.d.ts` 与独立 `vite.config.ts`，**无需依赖星序源码仓库即可在任意目录直接 `npm install && npm run build`**：

```bash
# 1. 初始化自包含插件工程（支持 esm-viewer / iframe-viewer / chat-hook 三种模板）
astrorderctl plugin init ./my-toml-plugin --id my-toml-plugin --template esm-viewer --ext .toml
cd ./my-toml-plugin && npm install && npm run build

# 2. 本地开发监听（自动注册并监听目录变更，文件保存即触发宿主热重载）
astrorderctl plugin dev ./my-toml-plugin/dist --server http://127.0.0.1:30001 --token <YOUR_TOKEN>

# 3. 生成 Ed25519 密钥对、对插件目录签名并验签（面向办公模式分发）
astrorderctl plugin keygen --out-dir ./keys --key-id team-release-k1
astrorderctl plugin sign ./my-toml-plugin/dist --key ./keys/team-release-k1.private.key --key-id team-release-k1
astrorderctl plugin verify ./my-toml-plugin/dist --pub-key ./keys/team-release-k1.public.key

# 4. 打包可分发 ZIP 并输出用于商店索引的 SHA-256 摘要
astrorderctl plugin pack ./my-toml-plugin/dist --out ./my-toml-plugin-1.0.0.zip
```

### 手动安装与热重载工作流

1. 构建插件产出 `dist/`（包含 `plugin.json`、`ui/entry.js` 与可选 `ui/entry.css`）。
2. 在星序中打开 **「插件管理 → 安装外部插件」**，选择 **本地目录安装**（填入 `dist` 路径）或上传 `.zip` 压缩包。
3. 授权并启用插件后即刻生效；后续每次修改并重新构建后，使用上方的 `astrorderctl plugin dev` 自动热重载，或点击插件卡片的 **「热重载」** 按钮（`POST /api/v1/extensions/reload`）：
   - 宿主会重新计算内容摘要（`content_digest`），先调用旧实例 `deactivate()` 并清理全部 `subscriptions` 与样式表，再加载新版 `ui/entry.js?v=<newDigest>`，**全程无需重启星序或刷新整个页面**。
4. 签名插件在运行期任意文件被篡改都会被后端 `/_esm` 端点立即拦截（`403`）。

---

## 13. 完整实战模板（3 套即拷即用）

### 模板 1：ESM 自定义文件格式查看/编辑器（`.toml` 可视化面板）

**`plugin.json`**
```json
{
  "schema_version": 1,
  "id": "toml-studio",
  "name": "TOML 可视化配置台",
  "version": "1.0.0",
  "category": "development",
  "description": "将 .toml 文件解析为分组键值卡片，并支持切回文本编辑",
  "author": "Your Name",
  "backend": { "type": "none" },
  "permissions": ["ui:slots"],
  "frontend": {
    "mode": "esm",
    "entry": "ui/entry.js",
    "sdk_version": "^1.0"
  },
  "contributes": {
    "viewers": [
      {
        "id": "toml-studio-viewer",
        "title": "TOML 可视化配置台",
        "extensions": [".toml"],
        "badge_label": "[TOML]",
        "tab_color": "#0ea5e9",
        "default_for_extensions": true
      }
    ]
  }
}
```

**`src/entry.tsx`**
```tsx
import { useEffect, useState } from 'react'
import { Badge, Group, Paper, ScrollArea, Stack, Text } from '@mantine/core'
import { defineAstrorderPlugin, type ViewerContext } from '@astrorder/sdk'

function parseSimpleToml(raw: string): Array<{ section: string; key: string; value: string }> {
  let section = 'root'
  const rows: Array<{ section: string; key: string; value: string }> = []
  for (const line of raw.split(/\r?\n/)) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) continue
    const sec = trimmed.match(/^\[(.+)]$/)
    if (sec) {
      section = sec[1]
      continue
    }
    const eq = trimmed.indexOf('=')
    if (eq > 0) {
      rows.push({
        section,
        key: trimmed.slice(0, eq).trim(),
        value: trimmed.slice(eq + 1).trim(),
      })
    }
  }
  return rows
}

function TomlViewer({ artifact, toolbarAction }: ViewerContext) {
  const [rows, setRows] = useState<Array<{ section: string; key: string; value: string }>>([])

  useEffect(() => {
    let active = true
    if (!artifact.readUrl) return
    void fetch(artifact.readUrl, { credentials: 'include' })
      .then((r) => r.text())
      .then((text) => {
        if (active) setRows(parseSimpleToml(text))
      })
    return () => { active = false }
  }, [artifact.readUrl])

  return (
    <Stack h="100%" gap={0}>
      <Paper p="xs" withBorder radius={0}>
        <Group justify="space-between">
          <Group gap="xs">
            <Text size="xs" fw={600}>{artifact.name}</Text>
            <Badge size="xs" variant="light">{rows.length} 项配置</Badge>
          </Group>
          {toolbarAction}
        </Group>
      </Paper>
      <ScrollArea style={{ flex: 1 }} p="sm">
        <Stack gap="xs">
          {rows.map((row, idx) => (
            <Paper key={idx} withBorder p="xs" radius="sm">
              <Group justify="space-between">
                <Text size="xs" fw={600}>[{row.section}] {row.key}</Text>
                <Text size="xs" ff="monospace" c="dimmed">{row.value}</Text>
              </Group>
            </Paper>
          ))}
        </Stack>
      </ScrollArea>
    </Stack>
  )
}

export default defineAstrorderPlugin({
  activate(ctx) {
    ctx.registerFileHandler({
      id: 'toml-studio-viewer',
      title: 'TOML 可视化配置台',
      extensions: ['.toml'],
      defaultForExtensions: true,
      tabColor: '#0ea5e9',
      badgeLabel: '[TOML]',
      component: TomlViewer,
    })
  },
})
```

---

### 模板 2：零构建 iframe 文件查看器（`.ndjson` 日志流）

**`plugin.json`**
```json
{
  "schema_version": 1,
  "id": "ndjson-inspector",
  "name": "NDJSON 日志检视器",
  "version": "1.0.0",
  "category": "development",
  "backend": { "type": "none" },
  "frontend": { "mode": "iframe" },
  "permissions": ["ui:slots"],
  "contributes": {
    "viewers": [
      {
        "id": "ndjson-inspector-viewer",
        "title": "NDJSON 表格视图",
        "extensions": [".ndjson", ".jsonl"],
        "ui_entry": "ui/index.html",
        "read_only": true,
        "default_for_extensions": true
      }
    ]
  }
}
```

**`ui/index.html`**
```html
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <style>
    body { margin: 0; font: 12px/1.5 ui-monospace, monospace; padding: 12px; }
    .row { border-bottom: 1px solid #e5e7eb; padding: 6px 0; white-space: pre-wrap; }
  </style>
  <script src="/api/v1/ext/_sdk/bridge.js"></script>
</head>
<body>
  <div id="app">加载中…</div>
  <script>
    (async () => {
      const file = await window.astrorderPlugin.readFile({ encoding: 'utf8' });
      const lines = file.content.split(/\r?\n/).filter(Boolean);
      const app = document.getElementById('app');
      app.innerHTML = '';
      for (const line of lines.slice(0, 500)) {
        const div = document.createElement('div');
        div.className = 'row';
        try {
          div.textContent = JSON.stringify(JSON.parse(line), null, 2);
        } catch {
          div.textContent = line;
        }
        app.appendChild(div);
      }
    })();
  </script>
</body>
</html>
```

---

### 模板 3：对话增强 + 跨插件服务插件

```tsx
import { Badge, Paper, Text } from '@mantine/core'
import { defineAstrorderPlugin, type AstrorderPluginContext } from '@astrorder/sdk'

export default defineAstrorderPlugin({
  activate(ctx: AstrorderPluginContext) {
    // 1. 发送前敏感词检查与快捷宏展开
    ctx.registerMessageInterceptor({
      id: 'macro-and-guard',
      order: 20,
      beforeSend({ text }) {
        if (text.includes('BEGIN PRIVATE KEY')) {
          return { cancel: true, reason: '检测到私钥片段，已自动阻止发送。' }
        }
        return { text: text.replace(/\/now\b/g, new Date().toLocaleString('zh-CN')) }
      },
    })

    // 2. 自定义 Markdown 代码块（```kpi ... ```）
    ctx.registerMarkdownBlockRenderer({
      id: 'kpi-card',
      title: 'KPI 指标卡',
      languages: ['kpi'],
      component: ({ code }) => (
        <Paper withBorder p="sm" radius="md">
          <Badge size="xs" color="teal" mb={6}>KPI 摘要</Badge>
          <Text size="sm" ff="monospace" style={{ whiteSpace: 'pre-wrap' }}>{code}</Text>
        </Paper>
      ),
    })

    // 3. 监听实时任务完成事件并向其他插件广播
    ctx.api.onHostEvent('command.upsert', (ev) => {
      if (ev.data?.state === 'completed') {
        ctx.api.emitPluginEvent('kpi.command.completed', { sessionId: ev.session_id })
      }
    })

    // 4. 暴露跨插件共享服务
    ctx.registerService('kpi.formatter', {
      formatPct: (value: number) => `${(value * 100).toFixed(1)}%`,
    })
  },
})
```
