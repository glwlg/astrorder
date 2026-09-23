"""Astrorder Blackboard Component Catalog & Schema specifications."""
from __future__ import annotations

from typing import Any

BLACKBOARD_COMPONENT_CATALOG: list[dict[str, Any]] = [
    {"id": "DataTable", "title": "结构化数据表格", "category": "table", "summary": "支持多列自定义表头、徽标与等宽高亮染色的只读数据表格"},
    {"id": "StepTimeline", "title": "任务阶段与流水线时间线", "category": "workflow", "summary": "呈现 CI/CD 流水线、任务阶段交接与步骤执行进度"},
    {"id": "MetricGrid", "title": "核心监控与 KPI 指标矩阵", "category": "metrics", "summary": "多列 Bento 呈现 QPS、耗时、内存、准确率等关键数值指标"},
    {"id": "ApiEndpointsCard", "title": "REST / RPC 接口契约清单", "category": "api", "summary": "呈现带 HTTP 方法染色、URL 路径、状态码与描述的 API 列表"},
    {"id": "ResourceUsageBar", "title": "系统资源负载与配额", "category": "system", "summary": "呈现 CPU、内存、磁盘的红黄绿阈值健康进度条"},
    {"id": "TestReport", "title": "自动化测试与压测报告", "category": "testing", "summary": "统计通过/失败/跳过用例数、耗时及大字号通过率百分比"},
    {"id": "CveSecurityReport", "title": "安全审计与漏洞合规报告", "category": "security", "summary": "按 Critical/High/Medium/Low 统计风险并罗列 CVE 漏洞清单"},
    {"id": "DiffViewer", "title": "代码补丁与配置差异对比器", "category": "code", "summary": "以等宽排版与增删染色呈现代码 diff / patch"},
    {"id": "Checklist", "title": "执行清单与交付验收表", "category": "task", "summary": "展示验收检查项，已完成项自动划线变灰并支持负责人"},
    {"id": "TerminalLog", "title": "控制台终端日志流", "category": "runtime", "summary": "深色控制台终端，带 ERROR、WARN 关键字自动染色输出"},
    {"id": "ArchitectureFlow", "title": "架构与调用链路流程图", "category": "architecture", "summary": "横向卡片箭头串联展示微服务、网关与 Agent 拓扑"},
    {"id": "GitCommitLog", "title": "Git 提交与发布变更日志", "category": "vcs", "summary": "展示代码版本提交历史、分支名、短 SHA 与时间戳"},
    {"id": "StatusCard", "title": "服务与网关状态概览卡片", "category": "status", "summary": "呈现 success、running、ready、warning、error 五态服务卡片"},
    {"id": "MultiNodeClusterSummary", "title": "多节点集群性能横向对比", "category": "cluster", "summary": "多主机 CPU/内存/磁盘/TOP 进程横向对齐对比看板"},
    {"id": "HostNodeTelemetryCard", "title": "服务器硬件与负载体检卡", "category": "host", "summary": "呈现单个主机 CPU 负载、内存可用健康条与系统版本"},
    {"id": "MissionSpecCard", "title": "作战任务契约与指挥规格卡", "category": "swarm", "summary": "呈现协同作战目标、当前推进阶段及目标机器列表"},
    {"id": "GomokuBoard", "title": "五子棋拟物对弈棋盘彩蛋", "category": "game", "summary": "15x15 原木纹理棋盘、3D 黑白立体落子与绝杀金色光环"},
]
