package agentapi

// Capability describes an Astrorder agent capability.
type Capability struct {
	ID      string         `json:"id"`
	Summary string         `json:"summary"`
	Input   map[string]any `json:"input"`
}

// Capabilities is the authoritative static list of all 40 Astrorder agent capabilities.
var Capabilities = []Capability{
	{
		ID:      "catalog.list",
		Summary: "List Astrorder agent APIs on this instance.",
		Input:   map[string]any{},
	},
	{
		ID:      "sessions.list",
		Summary: "List sessions visible to this Astrorder instance.",
		Input: map[string]any{
			"agent_id":  "optional agent id filter",
			"limit":     "optional maximum returned count (default 30, max 200)",
			"offset":    "optional integer offset for pagination (default 0)",
			"ephemeral": "optional boolean filter to include only temporary or persistent sessions",
		},
	},
	{
		ID:      "sessions.search",
		Summary: "Search sessions by title, workspace, project, or id.",
		Input: map[string]any{
			"q":        "query",
			"agent_id": "optional",
			"limit":    "optional maximum returned count (default 20, max 50)",
		},
	},
	{
		ID:      "sessions.read",
		Summary: "Read a session and its messages. Same-agent native ids work; cross-agent and cross-machine use the Astrorder key.",
		Input: map[string]any{
			"key":        "agent_id::session_id",
			"agent_id":   "optional",
			"session_id": "optional",
			"before":     "optional cursor for older messages",
			"limit":      "optional maximum messages count (default 20, max 50)",
		},
	},
	{
		ID:      "agents.list",
		Summary: "List agents on this instance, including remote SSH agents.",
		Input:   map[string]any{},
	},
	{
		ID:      "machines.list",
		Summary: "List local and SSH machines this Astrorder instance can reach.",
		Input:   map[string]any{},
	},
	{
		ID:      "projects.list",
		Summary: "List projects grouped by this Astrorder instance.",
		Input:   map[string]any{},
	},
	{
		ID:      "sessions.create",
		Summary: "Create a new session under a specified agent.",
		Input: map[string]any{
			"agent_id":   "target agent id",
			"title":      "optional session title",
			"workspace":  "optional workspace path",
			"ephemeral":  "optional boolean to mark as temporary/probe/review session",
			"parent_key": "optional parent session key (agent_id::session_id) for DAG tracing",
		},
	},
	{
		ID:      "sessions.delete",
		Summary: "Delete one or more sessions and clean up their runtime resources.",
		Input: map[string]any{
			"key":        "session key to delete (agent_id::session_id)",
			"keys":       "optional list of session keys for batch deletion",
			"agent_id":   "optional agent id if key is omitted",
			"session_id": "optional session id if key is omitted",
		},
	},
	{
		ID:      "sessions.tree",
		Summary: "Query session hierarchy and satellite DAG topology starting from a root session key, or list all mission swarm trees.",
		Input: map[string]any{
			"key": "optional root session key (agent_id::session_id)",
		},
	},
	{
		ID:      "sessions.send",
		Summary: "Send a prompt instruction to a session.",
		Input: map[string]any{
			"key":        "agent_id::session_id",
			"text":       "instruction text to send",
			"agent_id":   "optional",
			"session_id": "optional",
		},
	},
	{
		ID:      "sessions.stop",
		Summary: "Stop or interrupt a running session turn.",
		Input: map[string]any{
			"key":        "agent_id::session_id",
			"agent_id":   "optional",
			"session_id": "optional",
		},
	},
	{
		ID:      "plugins.list",
		Summary: "List Astrorder plugins and artifact viewers with their status and capabilities.",
		Input:   map[string]any{},
	},
	{
		ID:      "plugins.configure",
		Summary: "Configure or toggle an Astrorder plugin.",
		Input: map[string]any{
			"plugin_id": "target plugin ID",
			"enabled":   "optional boolean",
			"config":    "optional key-value settings",
		},
	},
	{
		ID:      "plugins.open",
		Summary: "Instruct Astrorder UI to open a specific plugin view or file artifact in the workspace sidecar panel.",
		Input: map[string]any{
			"plugin_id":   "plugin to open: terminal, browser, gitdiff, filetree, sidechat, agentgraph, monaco, drawio, mermaid, excalidraw, diff, three, html",
			"session_key": "optional session key (agent_id::session_id)",
			"path":        "optional file or directory path for filetree/monaco/diff/drawio/three/html",
			"url":         "optional url for browser or html",
			"title":       "optional custom tab title",
		},
	},
	{
		ID:      "plugins.close",
		Summary: "Instruct Astrorder UI to close a plugin tab or collapse the sidecar panel.",
		Input: map[string]any{
			"plugin_id": "optional plugin ID to close",
			"tab_id":    "optional specific tab ID to close",
			"collapse":  "optional boolean to collapse the entire sidecar panel",
		},
	},
	{
		ID:      "browser.run",
		Summary: "Run a bounded browser task in this Astrorder session's visible Edge tab using Jev. Later calls continue that tab and login state unless reset is true.",
		Input: map[string]any{
			"url":                "initial http/https page URL",
			"goal":               "complete browser goal with a visible completion condition",
			"inputs":             "optional object mapping visible field labels to exact values to type",
			"max_steps":          "optional action limit (default 30, maximum 60)",
			"reset":              "optional boolean; replace this session's browser tab without affecting other sessions",
			"include_screenshot": "optional boolean; return the final browser screenshot to the agent",
			"session_key":        "calling Astrorder session key (agent_id::session_id); required when more than one session is active",
		},
	},
	{
		ID:      "browser.screenshot",
		Summary: "Capture this Astrorder session's browser tab and return it as an image.",
		Input: map[string]any{
			"session_key": "calling Astrorder session key (agent_id::session_id)",
		},
	},
	{
		ID:      "browser.cdp",
		Summary: "Call a Chrome DevTools Protocol method on this Astrorder session's own browser tab.",
		Input: map[string]any{
			"session_key": "calling Astrorder session key (agent_id::session_id)",
			"method":      "CDP method in Runtime, DOM, CSS, Console, Log, Network, Performance, or Page",
			"params":      "optional CDP parameters object",
		},
	},
	{
		ID:      "machines.dispatch",
		Summary: "Dispatch a task or delegate session creation onto a specific target machine (local or SSH remote host).",
		Input: map[string]any{
			"machine_id": "target machine ID (from machines_list: 'local' or 'ssh-...')",
			"agent_kind": "optional target agent kind on that machine ('codex', 'hermes', 'grok')",
			"title":      "optional session title",
			"workspace":  "optional workspace directory on that machine",
			"prompt":     "optional initial prompt text to send immediately after creation",
		},
	},
	{
		ID:      "monitor.sessions.add",
		Summary: "Add one or more sessions into the Astrorder Monitor room dashboard for live visual tracking.",
		Input: map[string]any{
			"key":        "session key (agent_id::session_id)",
			"keys":       "optional list of session keys",
			"agent_id":   "optional",
			"session_id": "optional",
		},
	},
	{
		ID:      "monitor.sessions.remove",
		Summary: "Remove a session from the Astrorder Monitor room dashboard.",
		Input: map[string]any{
			"key":        "session key (agent_id::session_id)",
			"agent_id":   "optional",
			"session_id": "optional",
		},
	},
	{
		ID:      "monitor.layout.set",
		Summary: "Configure Astrorder Monitor room grid layout columns (1, 2, 3, or 4).",
		Input: map[string]any{
			"columns": "integer 1, 2, 3, or 4",
		},
	},
	{
		ID:      "blackboard.get",
		Summary: "【星序作战黑板-读取】读取共享任务状态、规格事实、共享记忆或工件（Blackboard）。当用户提到“查看黑板”、“读取黑板”、“黑板里有什么”时，直接调用本工具。严禁使用 Python 脚本读写源码或数据库。Read shared mission state, specifications or facts from the Astrorder Blackboard.",
		Input: map[string]any{
			"session_key": "专属会话作用域标识（传入上下文提示中的会话 ID，如单聊 'agent_id::session_id'、群聊 'group::group_id' 或星图 'swarm::root_key'）",
			"key":         "specific blackboard key, or omit to list all keys",
			"namespace":   "optional custom namespace scope override (default: resolved from session_key)",
		},
	},
	{
		ID:      "blackboard.set",
		Summary: "【星序作战黑板-写入/发布】将任务状态、结构化事实、备忘录、分析结论或工件写入星序作战黑板（Blackboard）。当用户指示“写到黑板”、“记入黑板”、“更新黑板”、“同步到黑板”时，必须直接调用本工具。强烈建议在 value 中显式声明 component 属性以呈现精美 Generative UI（如 'StepTimeline' 流水线、'Checklist' 验收清单、'DataTable' 表格、'MetricGrid' 指标卡、'GomokuBoard' 棋盘），未声明时系统将通过 Jev 决策模型自动推断选择最优组件补全。",
		Input: map[string]any{
			"session_key": "专属会话作用域标识（传入上下文提示中的会话 ID，如单聊 'agent_id::session_id'、群聊 'group::group_id' 或星图 'swarm::root_key'）",
			"key":         "blackboard key",
			"value":       "data payload (object, array, string). Recommend specifying {'component': 'StepTimeline'|'Checklist'|'DataTable'|'MetricGrid'|..., ...} for rich UI presentation",
			"namespace":   "optional custom namespace scope override (default: resolved from session_key)",
		},
	},
	{
		ID:      "blackboard.delete",
		Summary: "【星序作战黑板-删除】从作战黑板中删除指定的 key 或清空当前命名空间。Delete a key from the Astrorder Blackboard.",
		Input: map[string]any{
			"session_key": "专属会话作用域标识（传入上下文提示中的会话 ID，如单聊 'agent_id::session_id'、群聊 'group::group_id' 或星图 'swarm::root_key'）",
			"key":         "blackboard key to delete, or '*' / clean_namespace: true to wipe space",
			"namespace":   "optional custom namespace scope override (default: resolved from session_key)",
		},
	},
	{
		ID:      "blackboard.components.list",
		Summary: "List all supported Generative UI components for Blackboard with their IDs, categories and summaries.",
		Input:   map[string]any{},
	},
	{
		ID:      "blackboard.auto_render",
		Summary: "Analyze unformatted text or summary and use Jev decision model to automatically determine the best json-render UI component, formatting it directly to Blackboard.",
		Input: map[string]any{
			"key":       "blackboard key to publish to",
			"content":   "raw mission status, log, test result, or facts to render",
			"title":     "optional card title",
			"namespace": "optional namespace scope (default automatically resolved)",
		},
	},
	{
		ID:      "blackboard.component.schema",
		Summary: "Query exact JSON schema, required fields and copyable example payload for a specific Blackboard Generative UI component.",
		Input: map[string]any{
			"component_id": "component name, e.g. 'StepTimeline', 'MetricGrid', 'DataTable', 'ApiEndpointsCard', 'ResourceUsageBar', 'TestReport', 'CveSecurityReport', 'DiffViewer', 'Checklist', 'TerminalLog', 'ArchitectureFlow', 'StatusCard', 'MultiNodeClusterSummary', 'HostNodeTelemetryCard', 'MissionSpecCard', 'GomokuBoard'",
		},
	},
	{
		ID:      "swarm.milestone.declare",
		Summary: "Declare an expected mission milestone or dependency gate, with optional automatic wake-up instructions once resolved.",
		Input: map[string]any{
			"milestone_id":     "unique milestone identifier, e.g. 'backend_api_ready'",
			"title":            "milestone description or goal",
			"wake_session_key": "optional session key to automatically wake up once resolved",
			"wake_prompt":      "optional prompt instruction to send to wake_session_key once resolved",
			"metadata":         "optional payload/contract object",
		},
	},
	{
		ID:      "swarm.milestone.resolve",
		Summary: "Mark a mission milestone as resolved/completed, automatically waking any blocked sessions and updating the DAG.",
		Input: map[string]any{
			"milestone_id": "unique milestone identifier",
			"result":       "optional result or contract data to publish to the blackboard",
		},
	},
	{
		ID:      "swarm.milestone.list",
		Summary: "List all active, pending, and resolved mission milestones.",
		Input:   map[string]any{},
	},
	{
		ID:      "swarm.telemetry.report",
		Summary: "Submit a structured telemetry or progress report for the current session/worker.",
		Input: map[string]any{
			"progress":  "integer 0 to 100 percentage",
			"phase":     "current phase string, e.g. 'writing_code', 'running_tests', 'deploying'",
			"status":    "health status: 'ok', 'warning', 'blocked', or 'completed'",
			"summary":   "brief one-line status summary",
			"artifacts": "optional list of produced file paths or URLs",
			"key":       "optional session key, defaults to current session context",
		},
	},
	{
		ID:      "swarm.telemetry.get",
		Summary: "Read structured telemetry reports of sessions without reading entire chat histories.",
		Input: map[string]any{
			"key": "specific session key (agent_id::session_id), or omit to get all worker telemetry reports",
		},
	},
	{
		ID:      "swarm.sos.escalate",
		Summary: "Escalate a blocker, conflict, or failure to the queen/parent orchestrator session and broadcast emergency SOS signal.",
		Input: map[string]any{
			"reason":             "concrete description of the blocker, e.g. 'Dependency conflict or port 3000 collision'",
			"context":            "optional diagnostic details, logs, or error snippet",
			"key":                "optional session key of the blocked worker, defaults to current context",
			"target_session_key": "optional queen/parent session key to notify directly",
		},
	},
	{
		ID:      "swarm.sos.list",
		Summary: "List active and unresolved SOS escalation alerts across the swarm.",
		Input:   map[string]any{},
	},
	{
		ID:      "swarm.sos.resolve",
		Summary: "Resolve an SOS escalation after providing rescue assistance or unblocking the worker.",
		Input: map[string]any{
			"sos_id":     "escalation alert ID to resolve",
			"resolution": "optional resolution notes",
		},
	},
	{
		ID:      "swarm.lock.acquire",
		Summary: "Acquire an exclusive lock on a shared resource (e.g. 'git:repo', 'port:3000', 'db:migration') to prevent multi-agent collision.",
		Input: map[string]any{
			"resource":    "resource identifier to lock, e.g. 'git:main' or 'db:schema'",
			"ttl_seconds": "optional lock expiration in seconds (default 300, max 3600)",
			"owner_key":   "optional owner session key, defaults to current session context",
		},
	},
	{
		ID:      "swarm.lock.release",
		Summary: "Release a previously acquired exclusive resource lock.",
		Input: map[string]any{
			"resource":  "resource identifier to release",
			"owner_key": "optional owner session key",
		},
	},
	{
		ID:      "swarm.lock.list",
		Summary: "List all active resource locks across the swarm.",
		Input:   map[string]any{},
	},
}

// KnownPlugins lists the 13 built-in plugins for plugins.list and plugins.configure.
var KnownPlugins = []map[string]any{
	{"id": "blackboard", "title": "黑板", "description": "多 Agent 任务协同共享记忆、规格参数与产出物实时看板", "category": "collaboration", "extensions": []any{".json", ".yaml", ".yml", ".md"}},
	{"id": "drawio", "title": "Draw.io 架构图", "description": "交互式查看、编辑和绘制 .drawio 架构设计与流程图", "category": "design", "extensions": []any{".drawio", ".drawio.xml"}},
	{"id": "mermaid", "title": "Mermaid 图表", "description": "实时渲染 Mermaid 流程图、时序图、类图与状态机", "category": "diagram", "extensions": []any{".mmd", ".mermaid"}},
	{"id": "excalidraw", "title": "Excalidraw 手绘白板", "description": "手绘草图、原型交互画板查看与编辑", "category": "whiteboard", "extensions": []any{".excalidraw"}},
	{"id": "diff", "title": "文本与补丁对比", "description": "精准的 Side-by-Side 差异与补丁审查", "category": "development", "extensions": []any{".diff", ".patch"}},
	{"id": "three", "title": "3D 模型预览", "description": "实时渲染与三维交互预览 3D 网格模型", "category": "media", "extensions": []any{".gltf", ".glb", ".obj", ".stl"}},
	{"id": "html", "title": "Web 页面沙箱预览", "description": "安全的隔离式 HTML/SVG/Web 页面实时渲染与交互", "category": "web", "extensions": []any{".html", ".htm", ".svg"}},
	{"id": "terminal", "title": "交互式终端", "description": "xterm 原生 PTY 命令输出与交互", "category": "system", "extensions": []any{".log", ".term"}},
	{"id": "monaco", "title": "Monaco 代码编辑器", "description": "VSCode 同款代码高亮、大文件查看与在线修改", "category": "development", "extensions": []any{".ts", ".tsx", ".js", ".jsx", ".py", ".json", ".yaml", ".yml", ".md", ".rs", ".go", ".sql", ".sh", ".bash", ".ps1"}},
	{"id": "filetree", "title": "目录结构浏览器", "description": "文件树全景概览与快速导航", "category": "navigation", "extensions": []any{}},
	{"id": "gitdiff", "title": "Git 变更集审查", "description": "工作区未暂存与已暂存变更的可视化审查面板", "category": "vcs", "extensions": []any{}},
	{"id": "sidechat", "title": "副对话窗口", "description": "针对当前工件或长文本的局部深入多轮探讨", "category": "chat", "extensions": []any{}},
	{"id": "agentgraph", "title": "Agent 协作拓扑", "description": "多 Agent 任务协同拓扑与调用链可视化", "category": "orchestration", "extensions": []any{}},
}
