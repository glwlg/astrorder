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
  title: '数据库管理工具 (DBX)',
  description: '通过 DBX Web 提供右侧数据库工作台与 SQL 一键载入能力；作为可替换的数据库工具实现，可在插件管理中独立启停或更换',
  version: '1.0.0',
  category: 'development',
  capabilitySlot: DATABASE_TOOL_SLOT,
  capabilityLabel: '数据库工具',
  implementationName: 'DBX',
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
      key: 'upstreamUrl',
      label: 'DBX 服务地址',
      description: '本机 wslc 容器（如 http://127.0.0.1:14224）或远程容器地址（如 http://192.168.1.100:4224），由星序后端统一鉴权反代',
      type: 'string',
      defaultValue: DBX_DEFAULT_UPSTREAM_URL,
    },
  ],
  documentation: {
    summary: '为星序的「数据库工具」能力槽位提供开源 DBX（t8y2/dbx）实现。星序主界面仅面向抽象的数据库工具能力，不与 DBX 强耦合，后续可平滑替换为其他数据库工具插件。',
    sections: [
      {
        title: 'SQL 文件与 SQL 代码块联动',
        content: '启用后，点击对话/文件树中的 .sql 文件或独立 SQL 代码块的「数据库打开」，会自动载入右侧数据库工具的新查询标签页；绝不自动执行 DDL/DML，选库与执行完全由你人工确认。',
      },
      {
        title: '容器要求',
        content: '容器需配置 DBX_PUBLIC_BASE_PATH=/api/v1/plugins/dbx（本机回环可设 DBX_DISABLE_PASSWORD=true；部署到远程服务器建议设置 DBX_PASSWORD）。',
      },
    ],
  },
}
