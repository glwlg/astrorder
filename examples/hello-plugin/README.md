# Hello Extension — Astrorder 第三方插件示例

本目录是一个**零依赖、可独立运行**的 Astrorder 第三方插件参考实现，演示如何在**不修改 Astrorder 任何核心源码**的情况下接入全部扩展能力：

- **对话底栏状态胶囊**（`chat.composer.status_bar` + `declarative_pill`）
- **Git 分支菜单项与侧边栏欢迎入口**（`chat.git_menu.items` / `sidecar.welcome_menu`）
- **右侧沙箱面板**（`sidecar_panels` + `ui/panel.html` + `/api/v1/ext/_sdk/bridge.js`）
- **独立插件配置抽屉**（`settings.mode = "custom_ui"` + `ui/settings.html`）
- **受权限保护的 Host API**（读取工作区 Git 信息 `workspace:git.read`、隔离 KV `storage:kv`、加密凭据 `storage:secrets`）
- **Agent 扩展能力与 CLI 调用**（`astrorder-agent ext list` / `astrorder-agent ext call hello-plugin echo`）
- **学习雷达信号与 Skill 自动发现**（`workspace.failure_signals` + `skills/hello-ext/SKILL.md`）

## 目录结构

```text
examples/hello-plugin/
├── plugin.json               # 插件声明清单（权限、后端、设置模式与 UI/Agent 贡献点）
├── server.mjs                # 零依赖 Node.js HTTP 子进程后端
├── ui/
│   ├── panel.html            # 侧边栏沙箱面板页面
│   └── settings.html         # 插件配置抽屉页面
└── skills/
    └── hello-ext/
        └── SKILL.md          # 插件自带 Agent Skill（启用即自动发现）
```

## 安装与体验

1. 打开星序 **「设置 → 功能与插件」**，点击右上角 **「安装外部插件」**。
2. 选择 **「本地目录安装」** 并填入本目录路径（例如 `<repo>/examples/hello-plugin`），或将本目录打包为 `.zip` 上传安装。
3. 新安装的第三方插件默认处于停用状态；点击开启开关，在权限确认弹窗中审阅并授权所需权限后即刻启动。
4. 点击卡片上的 **「配置」** 即可在侧边抽屉中打开 `ui/settings.html`；返回会话页面可在底栏看到状态胶囊，点击即可在右侧 Sidecar 打开 `ui/panel.html`。

## 切换为免写前端的 Schema 配置模式

如果插件只需要表单项而不需要自定义设置页，只需将 `plugin.json` 中的 `settings` 修改为 `mode: "schema"` 并声明 `schema` 字段数组，Astrorder 的 `PluginConfigDrawer` 会自动渲染标准配置表单并加密保存 `secret` 字段。
