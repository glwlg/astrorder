import { IconDatabase } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { DBX_DEFAULT_UPSTREAM_URL, DBX_PLUGIN_ID, DbxViewer } from './DbxViewer'

export { DBX_DEFAULT_UPSTREAM_URL, DBX_PLUGIN_ID, DBX_PROXY_ENTRY_URL } from './DbxViewer'
export { openSqlFileInDbx, openSqlSnippetInDbx, useDbxSidecarStore } from './dbxStore'
export { buildSqlSnippetFilename, isSqlFilePath, isSqlLanguage, extractSqlFileName, extractSqlLine, stripSqlLocationSuffix } from './sqlFormats'

export const dbxViewer: ArtifactViewer = {
  id: DBX_PLUGIN_ID,
  title: 'DBX 数据库工作台',
  description: '在右侧 Sidecar 嵌入 DBX Web 数据库管理工作台，支持对话与文件树中的 .sql 文件右键一键载入查询标签页，由你人工选库核对后执行',
  version: '1.0.0',
  badgeLabel: '[DBX]',
  tabColor: '#0f766e',
  icon: IconDatabase,
  mimeTypes: ['application/x-astrorder-dbx'],
  supports: (artifact) => (artifact.mediaType === 'application/x-astrorder-dbx' ? 100 : 0),
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
    summary: '将开源 DBX（t8y2/dbx）数据库工作台以右侧 Sidecar 插件方式集成到星序，左侧保留 Agent 对话清单、右侧直接核对并手动执行 SQL。',
    sections: [
      {
        title: '右键 .sql 文件直接送入 DBX',
        content: '在对话中的 .sql 链接、工作区文件树右键菜单或 Monaco 预览工具栏点击「在右侧 DBX 中打开」，星序会自动读取 SQL 并载入右侧 DBX 新查询标签页，绝不自动执行 DDL/DML，选择目标库与点击执行完全由你人工控制。',
      },
      {
        title: '容器要求',
        content: '容器需配置 DBX_PUBLIC_BASE_PATH=/api/v1/plugins/dbx（本机回环可设 DBX_DISABLE_PASSWORD=true；部署到远程服务器建议设置 DBX_PASSWORD）。',
      },
    ],
  },
}
