import {
  defineAstrorderPlugin,
  type AstrorderPluginContext,
  type ViewerContext,
} from '@astrorder/sdk'
import {
  DBX_ALT_MEDIA_TYPE,
  DBX_DEFAULT_UPSTREAM_URL,
  DBX_MEDIA_TYPE,
  DBX_PLUGIN_ID,
  dbxSidecarStore,
  openDbxWorkbench,
  openSqlFileInDbx,
  openSqlSnippetInDbx,
} from './dbxStore'
import { DbxViewer } from './DbxViewer'
import { DatabaseIcon } from './icons'
import './dbxViewer.css'

const DATABASE_TOOL_SLOT = 'database-tool'

export function activate(ctx: AstrorderPluginContext): void {
  const BoundDbxViewer = (props: ViewerContext) => <DbxViewer {...props} ctx={ctx} />

  ctx.registerViewer({
    id: DBX_PLUGIN_ID,
    title: '数据库管理工具',
    description:
      '在右侧工作台嵌入数据库管理工具，支持点击 .sql 文件与独立 SQL 代码块一键载入查询，由你人工选库核对后执行',
    version: ctx.version,
    category: 'development',
    capabilitySlot: DATABASE_TOOL_SLOT,
    capabilityLabel: '数据库工具',
    providerConfigKey: 'provider',
    badgeLabel: '[数据库]',
    tabColor: '#0f766e',
    icon: DatabaseIcon,
    mimeTypes: [DBX_MEDIA_TYPE, DBX_ALT_MEDIA_TYPE],
    supports: (artifact) =>
      artifact.mediaType === DBX_MEDIA_TYPE ||
      artifact.mediaType === DBX_ALT_MEDIA_TYPE ||
      artifact.id === 'database:workbench'
        ? 100
        : 0,
    component: BoundDbxViewer,
    capabilities: {
      canEdit: false,
    },
    configOptions: [
      {
        key: 'provider',
        label: '工具实现',
        description: '选择右侧数据库工作台使用的底层实现，可随时切换而不影响星序交互',
        type: 'select',
        options: [
          { label: 'DBX', value: 'dbx' },
          { label: '自定义 Web 工具', value: 'custom-web' },
        ],
        defaultValue: 'dbx',
      },
      {
        key: 'upstreamUrl',
        label: '服务地址',
        description:
          '本机 wslc 容器（如 http://127.0.0.1:14224）或远程容器地址（如 http://192.168.1.100:4224），由星序后端统一鉴权反代',
        type: 'string',
        defaultValue: DBX_DEFAULT_UPSTREAM_URL,
        visibleWhen: { key: 'provider', value: 'dbx' },
      },
      {
        key: 'customUrl',
        label: 'Web 工具地址',
        description:
          '自托管数据库 Web 管理工具（如 CloudBeaver、pgAdmin、Bytebase 等）的完整 http(s) 地址',
        type: 'string',
        defaultValue: '',
        visibleWhen: { key: 'provider', value: 'custom-web' },
      },
    ],
    documentation: {
      summary:
        '提供星序的「数据库工具」右侧工作台、项目数据库关联与对话内 SQL 原地执行能力；底层实现可在参数配置中自由切换（当前内置 DBX 与自定义 Web 工具选项）。',
      sections: [
        {
          title: '项目关联数据库与对话内原地执行（DBX）',
          content:
            '在侧栏项目菜单选择「关联数据库」即可为一个项目绑定多个 DBX 目标库并设定默认库（同仓多 Agent 与 Worktree 自动共享）。对话中的 SQL 代码块与 .sql 文件引用均可免开右侧 DBX 直接原地执行并查看表格结果、一键发回当前会话；纯只读查询一键直出，涉及事务控制、写入或 DDL 语句前后端双重强制弹窗确认（PROD 目标重点标红警示）。',
        },
        {
          title: 'SQL 文件与 SQL 代码块联动',
          content:
            '需要浏览表结构或编辑复杂 SQL 时，仍可点击「数据库打开」载入右侧数据库工作台与 SQL 清单（不自动执行）。',
        },
        {
          title: 'DBX 容器要求',
          content:
            '选用 DBX 实现时，容器需配置 DBX_PUBLIC_BASE_PATH=/api/v1/plugins/dbx（本机回环可设 DBX_DISABLE_PASSWORD=true；部署到远程服务器建议设置 DBX_PASSWORD）。',
        },
      ],
    },
  })

  ctx.registerDatabaseTool({
    id: DBX_PLUGIN_ID,
    implementationName: 'DBX',
    mediaType: DBX_MEDIA_TYPE,
    openWorkbench: (sessionId, agentId, connectionId) => {
      openDbxWorkbench(ctx, sessionId, agentId, connectionId)
    },
    openSqlFile: (params) => openSqlFileInDbx(ctx, params),
    openSqlSnippet: (params) => openSqlSnippetInDbx(ctx, params),
  })
}

export function deactivate(): void {
  // All viewer and databaseTool registrations are automatically released via ctx.subscriptions.
  dbxSidecarStore.reset()
}

export default defineAstrorderPlugin({
  activate,
  deactivate,
})
