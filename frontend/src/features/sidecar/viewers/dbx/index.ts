import { IconDatabase } from '@tabler/icons-react'
import { DATABASE_TOOL_SLOT, databaseToolRegistry } from '../../databaseTool'
import { useSidecarStore } from '../../sidecarStore'
import type { ArtifactViewer } from '../../types'
import { openSqlFileInDbx, openSqlSnippetInDbx } from './dbxStore'
import { DBX_DEFAULT_UPSTREAM_URL, DBX_PLUGIN_ID, DbxViewer } from './DbxViewer'

export { DBX_DEFAULT_UPSTREAM_URL, DBX_PLUGIN_ID, DBX_PROXY_ENTRY_URL } from './DbxViewer'
export { openSqlFileInDbx, openSqlSnippetInDbx, useDbxSidecarStore } from './dbxStore'
export { buildSqlSnippetFilename, isSqlFilePath, isSqlLanguage, extractSqlFileName, extractSqlLine, stripSqlLocationSuffix } from './sqlFormats'

const DBX_MEDIA_TYPE = 'application/x-astrorder-dbx'

databaseToolRegistry.register({
  id: DBX_PLUGIN_ID,
  implementationName: 'DBX',
  mediaType: DBX_MEDIA_TYPE,
  openWorkbench: (sessionId, agentId, connectionId) => {
    useSidecarStore
      .getState()
      .openDatabaseWorkbench(DBX_MEDIA_TYPE, DBX_PLUGIN_ID, sessionId, agentId, connectionId)
  },
  openSqlFile: openSqlFileInDbx,
  openSqlSnippet: openSqlSnippetInDbx,
})

export const dbxViewer: ArtifactViewer = {
  id: DBX_PLUGIN_ID,
  title: '数据库管理工具',
  description: '在右侧工作台嵌入数据库管理工具，支持点击 .sql 文件与独立 SQL 代码块一键载入查询，由你人工选库核对后执行',
  version: '1.0.0',
  category: 'development',
  capabilitySlot: DATABASE_TOOL_SLOT,
  capabilityLabel: '数据库工具',
  providerConfigKey: 'provider',
  badgeLabel: '[数据库]',
  tabColor: '#0f766e',
  icon: IconDatabase,
  mimeTypes: [DBX_MEDIA_TYPE],
  supports: (artifact) => (artifact.mediaType === DBX_MEDIA_TYPE ? 100 : 0),
  component: DbxViewer,
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
      description: '本机 wslc 容器（如 http://127.0.0.1:14224）或远程容器地址（如 http://192.168.1.100:4224），由星序后端统一鉴权反代',
      type: 'string',
      defaultValue: DBX_DEFAULT_UPSTREAM_URL,
      visibleWhen: { key: 'provider', value: 'dbx' },
    },
    {
      key: 'customUrl',
      label: 'Web 工具地址',
      description: '自托管数据库 Web 管理工具（如 CloudBeaver、pgAdmin、Bytebase 等）的完整 http(s) 地址',
      type: 'string',
      defaultValue: '',
      visibleWhen: { key: 'provider', value: 'custom-web' },
    },
  ],
  documentation: {
    summary: '提供星序的「数据库工具」右侧工作台与 SQL 联动能力；底层实现可在参数配置中自由切换（当前内置 DBX 与自定义 Web 工具选项）。',
    sections: [
      {
        title: 'SQL 文件与 SQL 代码块联动',
        content: '启用后，点击对话/文件树中的 .sql 文件或独立 SQL 代码块的「数据库打开」，会自动打开右侧数据库工作台并加入 SQL 清单（DBX 实现支持自动载入查询标签页）；绝不自动执行 DDL/DML，选库与执行完全由你人工确认。',
      },
      {
        title: 'DBX 容器要求',
        content: '选用 DBX 实现时，容器需配置 DBX_PUBLIC_BASE_PATH=/api/v1/plugins/dbx（本机回环可设 DBX_DISABLE_PASSWORD=true；部署到远程服务器建议设置 DBX_PASSWORD）。',
      },
    ],
  },
}
