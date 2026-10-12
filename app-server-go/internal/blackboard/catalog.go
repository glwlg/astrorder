package blackboard

// ComponentInfo describes a Generative UI component available in Blackboard.
type ComponentInfo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Summary  string `json:"summary"`
}

// Catalog contains all 17 supported Generative UI components for Blackboard.
var Catalog = []ComponentInfo{
	{ID: "DataTable", Title: "结构化数据表格", Category: "table", Summary: "支持多列自定义表头、徽标与等宽高亮染色的只读数据表格"},
	{ID: "StepTimeline", Title: "任务阶段与流水线时间线", Category: "workflow", Summary: "呈现 CI/CD 流水线、任务阶段交接与步骤执行进度"},
	{ID: "MetricGrid", Title: "核心监控与 KPI 指标矩阵", Category: "metrics", Summary: "多列 Bento 呈现 QPS、耗时、内存、准确率等关键数值指标"},
	{ID: "ApiEndpointsCard", Title: "REST / RPC 接口契约清单", Category: "api", Summary: "呈现带 HTTP 方法染色、URL 路径、状态码与描述的 API 列表"},
	{ID: "ResourceUsageBar", Title: "系统资源负载与配额", Category: "system", Summary: "呈现 CPU、内存、磁盘的红黄绿阈值健康进度条"},
	{ID: "TestReport", Title: "自动化测试与压测报告", Category: "testing", Summary: "统计通过/失败/跳过用例数、耗时及大字号通过率百分比"},
	{ID: "CveSecurityReport", Title: "安全审计与漏洞合规报告", Category: "security", Summary: "按 Critical/High/Medium/Low 统计风险并罗列 CVE 漏洞清单"},
	{ID: "DiffViewer", Title: "代码补丁与配置差异对比器", Category: "code", Summary: "以等宽排版与增删染色呈现代码 diff / patch"},
	{ID: "Checklist", Title: "执行清单与交付验收表", Category: "task", Summary: "展示验收检查项，已完成项自动划线变灰并支持负责人"},
	{ID: "TerminalLog", Title: "控制台终端日志流", Category: "runtime", Summary: "深色控制台终端，带 ERROR、WARN 关键字自动染色输出"},
	{ID: "ArchitectureFlow", Title: "架构与调用链路流程图", Category: "architecture", Summary: "横向卡片箭头串联展示微服务、网关与 Agent 拓扑"},
	{ID: "GitCommitLog", Title: "Git 提交与发布变更日志", Category: "vcs", Summary: "展示代码版本提交历史、分支名、短 SHA 与时间戳"},
	{ID: "StatusCard", Title: "服务与网关状态概览卡片", Category: "status", Summary: "呈现 success、running、ready、warning、error 五态服务卡片"},
	{ID: "MultiNodeClusterSummary", Title: "多节点集群性能横向对比", Category: "cluster", Summary: "多主机 CPU/内存/磁盘/TOP 进程横向对齐对比看板"},
	{ID: "HostNodeTelemetryCard", Title: "服务器硬件与负载体检卡", Category: "host", Summary: "呈现单个主机 CPU 负载、内存可用健康条与系统版本"},
	{ID: "MissionSpecCard", Title: "作战任务契约与指挥规格卡", Category: "swarm", Summary: "呈现协同作战目标、当前推进阶段及目标机器列表"},
	{ID: "GomokuBoard", Title: "五子棋拟物对弈棋盘彩蛋", Category: "game", Summary: "15x15 原木纹理棋盘、3D 黑白立体落子与绝杀金色光环"},
}

// ComponentSchema defines the exact schema and example for a Blackboard component.
type ComponentSchema struct {
	ComponentID string         `json:"component_id"`
	Title       string         `json:"title"`
	Schema      map[string]any `json:"schema"`
	Example     map[string]any `json:"example"`
}

// Schemas maps the 12 schema-supported components to their specifications.
var Schemas = map[string]ComponentSchema{
	"DataTable": {
		ComponentID: "DataTable",
		Title:       "结构化数据表格",
		Schema: map[string]any{
			"title": "string (可选表格总标题)",
			"columns": []any{
				map[string]any{
					"key":   "string (字段键名，必需)",
					"label": "string (表头展示名称，可选，缺省同 key)",
					"type":  "'text' | 'badge' | 'mono' (列展示样式，可选)",
				},
			},
			"rows": []any{
				"object (键值对应各列 key 的数据行对象，必需)",
			},
			"caption": "string (表格底部备注说明，可选)",
		},
		Example: map[string]any{
			"title": "全星域 Agent 会话健康度与审计状态表",
			"columns": []any{
				map[string]any{"key": "agent", "label": "Agent 标识", "type": "mono"},
				map[string]any{"key": "machine", "label": "所在服务器"},
				map[string]any{"key": "status", "label": "健康度", "type": "badge"},
				map[string]any{"key": "sessions", "label": "活跃会话数"},
			},
			"rows": []any{
				map[string]any{"agent": "local-codex", "machine": "本机", "status": "ok", "sessions": 43},
				map[string]any{"agent": "ssh-codex-debian", "machine": "Debian", "status": "ok", "sessions": 19},
				map[string]any{"agent": "ssh-hermes-wsl", "machine": "WSL", "status": "ok", "sessions": 4},
			},
			"caption": "审计耗时 1.2s，共扫描 3 台主机节点",
		},
	},
	"StepTimeline": {
		ComponentID: "StepTimeline",
		Title:       "任务阶段与流水线时间线",
		Schema: map[string]any{
			"title": "string (可选总标题)",
			"steps": []any{
				map[string]any{
					"title":       "string (阶段标题，必需)",
					"status":      "'completed' | 'running' | 'pending' | 'failed' (状态，必需)",
					"description": "string (阶段说明或产出物描述，可选)",
					"time":        "string (耗时或完成时间，可选)",
				},
			},
		},
		Example: map[string]any{
			"title": "生产构建与发布流水线",
			"steps": []any{
				map[string]any{"title": "代码依赖审计", "status": "completed", "time": "2.4s"},
				map[string]any{"title": "单元与集成测试", "status": "completed", "time": "14.2s"},
				map[string]any{"title": "Docker 镜像构建与安全扫描", "status": "running", "description": "正在执行 CVE 漏洞扫描..."},
				map[string]any{"title": "灰度放量发布", "status": "pending"},
			},
		},
	},
	"MetricGrid": {
		ComponentID: "MetricGrid",
		Title:       "核心监控与 KPI 指标矩阵",
		Schema: map[string]any{
			"title": "string (可选总标题)",
			"metrics": []any{
				map[string]any{
					"label":  "string (指标名称，必需)",
					"value":  "string | number (指标数值，必需)",
					"unit":   "string (单位，如 'ms', 'MB', '%', 可选)",
					"change": "string (同比/环比浮动，如 '+12%', '-5%', 可选)",
					"status": "'good' | 'warn' | 'bad' (健康状态，可选)",
				},
			},
		},
		Example: map[string]any{
			"title": "推理网关实时性能看板",
			"metrics": []any{
				map[string]any{"label": "QPS 吞吐", "value": 1420, "unit": "req/s", "change": "+8.4%", "status": "good"},
				map[string]any{"label": "P99 时延", "value": 42, "unit": "ms", "change": "-4ms", "status": "good"},
				map[string]any{"label": "显存占用", "value": 82.5, "unit": "%", "status": "warn"},
				map[string]any{"label": "错误率", "value": 0.01, "unit": "%", "status": "good"},
			},
		},
	},
	"ApiEndpointsCard": {
		ComponentID: "ApiEndpointsCard",
		Title:       "REST / RPC 接口契约清单",
		Schema: map[string]any{
			"title":   "string (清单标题，可选)",
			"baseUrl": "string (基础地址，如 'https://api.domain.com', 可选)",
			"endpoints": []any{
				map[string]any{
					"method": "'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH' (请求方法，必需)",
					"path":   "string (接口路径，必需)",
					"desc":   "string (接口描述或功能说明，可选)",
					"status": "number (返回状态码如 200, 201, 可选)",
				},
			},
		},
		Example: map[string]any{
			"title":   "用户认证与授权服务 API 契约",
			"baseUrl": "https://auth.internal.net",
			"endpoints": []any{
				map[string]any{"method": "POST", "path": "/api/v1/auth/login", "desc": "账密登录获取 JWT", "status": 200},
				map[string]any{"method": "GET", "path": "/api/v1/users/me", "desc": "查询当前登录用户会话与权限", "status": 200},
				map[string]any{"method": "POST", "path": "/api/v1/tokens/refresh", "desc": "静默刷新访问令牌", "status": 200},
			},
		},
	},
	"ResourceUsageBar": {
		ComponentID: "ResourceUsageBar",
		Title:       "系统资源负载与配额",
		Schema: map[string]any{
			"title": "string (标题，可选)",
			"resources": []any{
				map[string]any{
					"name":    "string (资源名称，如 'CPU 核心', '物理内存', 必需)",
					"used":    "number (已使用量，可选)",
					"total":   "number (总配额量，可选)",
					"unit":    "string (单位，如 'GB', 'Core', 可选)",
					"percent": "number (百分比 0-100，可选)",
				},
			},
		},
		Example: map[string]any{
			"title": "推理宿主机当前负载配额",
			"resources": []any{
				map[string]any{"name": "CPU 负载", "percent": 45, "display": "45% (8/16 Cores)"},
				map[string]any{"name": "物理内存", "used": 18, "total": 32, "unit": "GB", "percent": 56},
				map[string]any{"name": "NVMe 根盘", "used": 120, "total": 512, "unit": "GB", "percent": 23},
			},
		},
	},
	"TestReport": {
		ComponentID: "TestReport",
		Title:       "自动化测试与压测报告",
		Schema: map[string]any{
			"title":    "string (报告标题，可选)",
			"passed":   "number (通过用例数，必需)",
			"failed":   "number (失败用例数，必需)",
			"skipped":  "number (跳过用例数，可选)",
			"duration": "string (总耗时，如 '14.2s', 可选)",
			"suite":    "string (测试套件名，可选)",
			"cases":    []any{map[string]any{"name": "string", "status": "'passed' | 'failed'", "duration": "string"}},
		},
		Example: map[string]any{
			"title":    "核心端到端集成测试报告",
			"suite":    "pytest --e2e backend/tests",
			"passed":   42,
			"failed":   0,
			"skipped":  1,
			"duration": "8.45s",
			"cases": []any{
				map[string]any{"name": "test_mcp_gateway_dispatch", "status": "passed", "duration": "0.12s"},
				map[string]any{"name": "test_blackboard_namespace_scoping", "status": "passed", "duration": "0.08s"},
				map[string]any{"name": "test_agent_websocket_heartbeat", "status": "passed", "duration": "0.15s"},
			},
		},
	},
	"CveSecurityReport": {
		ComponentID: "CveSecurityReport",
		Title:       "安全审计与漏洞合规报告",
		Schema: map[string]any{
			"title":           "string (报告标题，可选)",
			"critical":        "number (严重漏洞数，必需)",
			"high":            "number (高危漏洞数，必需)",
			"medium":          "number (中危漏洞数，必需)",
			"low":             "number (低危漏洞数，必需)",
			"vulnerabilities": []any{map[string]any{"cve": "string", "package": "string", "severity": "'CRITICAL' | 'HIGH' | 'MEDIUM'"}},
		},
		Example: map[string]any{
			"title":    "容器基础镜像 CVE 安全审计结果",
			"critical": 0,
			"high":     1,
			"medium":   2,
			"low":      5,
			"vulnerabilities": []any{
				map[string]any{"cve": "CVE-2026-3829", "package": "openssl-3.0.12", "severity": "HIGH"},
				map[string]any{"cve": "CVE-2026-1920", "package": "curl-8.5.0", "severity": "MEDIUM"},
			},
		},
	},
	"DiffViewer": {
		ComponentID: "DiffViewer",
		Title:       "代码补丁与配置差异对比器",
		Schema: map[string]any{
			"file": "string (修改的文件路径，可选)",
			"diff": "string (标准统一 diff 文本，包含 +/-/@@ 标记，必需)",
		},
		Example: map[string]any{
			"file": "backend/src/astrorder/store.py",
			"diff": "@@ -102,3 +102,3 @@\n-  namespace = 'global'\n+  namespace = f'swarm:{root_key}'\n   db.flush()",
		},
	},
	"Checklist": {
		ComponentID: "Checklist",
		Title:       "执行清单与交付验收表",
		Schema: map[string]any{
			"title": "string (清单标题，可选)",
			"items": []any{
				map[string]any{
					"label":    "string (检查项描述，必需)",
					"done":     "boolean (是否已完成，必需)",
					"assignee": "string (负责人/执行 Agent，可选)",
				},
			},
		},
		Example: map[string]any{
			"title": "生产上线前就绪检查清单",
			"items": []any{
				map[string]any{"label": "数据库无锁热迁移", "done": true, "assignee": "Codex"},
				map[string]any{"label": "全链路接口拨测通过", "done": true, "assignee": "Hermes"},
				map[string]any{"label": "CDN 静态缓存预热与回源验证", "done": false, "assignee": "Grok"},
			},
		},
	},
	"TerminalLog": {
		ComponentID: "TerminalLog",
		Title:       "控制台终端日志流",
		Schema: map[string]any{
			"title":  "string (终端标题，可选)",
			"status": "string (状态标记，如 'ok', 'error', 可选)",
			"lines":  "string[] (日志行数组，包含 ERROR/WARN 将自动染色，必需)",
		},
		Example: map[string]any{
			"title":  "集群编译构建输出",
			"status": "ok",
			"lines": []any{
				"[INFO] Starting build for astrorder-worker:v2.4",
				"[INFO] Compiling native Rust / PTY bridge",
				"[WARN] Deprecated dependency detected in build graph",
				"[INFO] Build finished successfully in 4.2s",
			},
		},
	},
	"ArchitectureFlow": {
		ComponentID: "ArchitectureFlow",
		Title:       "架构与调用链路流程图",
		Schema: map[string]any{
			"title": "string (架构标题，可选)",
			"nodes": []any{
				map[string]any{
					"name":   "string (服务/节点名称，必需)",
					"role":   "string (职责/类型如 'Gateway', 'Core API', 'DB', 必需)",
					"status": "'ok' | 'warn' (健康状态，可选)",
					"desc":   "string (补充描述，可选)",
				},
			},
		},
		Example: map[string]any{
			"title": "多智能体任务协同链路",
			"nodes": []any{
				map[string]any{"name": "Codex (主星)", "role": "Orchestrator", "status": "ok", "desc": "指挥与门禁裁决"},
				map[string]any{"name": "Hermes (伴星)", "role": "WSL Worker", "status": "ok", "desc": "性能数据采集"},
				map[string]any{"name": "Grok (伴星)", "role": "Debian Worker", "status": "ok", "desc": "集群状态汇总"},
			},
		},
	},
	"StatusCard": {
		ComponentID: "StatusCard",
		Title:       "服务与网关状态概览卡片",
		Schema: map[string]any{
			"title":     "string (卡片标题，必需)",
			"status":    "'success' | 'running' | 'ready' | 'warning' | 'error' | 'info' (状态枚举，必需)",
			"summary":   "string (核心摘要描述，可选)",
			"badge":     "string (右上角徽标文字，可选)",
			"timestamp": "string (时间戳，可选)",
			"details":   "object (键值补充参数对象，可选)",
		},
		Example: map[string]any{
			"title":     "多节点负载探针任务状态",
			"status":    "success",
			"summary":   "已顺利完成 WSL2 与 Debian 远端主机全部 14 项基准指标采集。",
			"badge":     "COMPLETED",
			"details":   map[string]any{"wsl_latency": "12ms", "debian_latency": "34ms", "nodes": 2},
		},
	},
}
