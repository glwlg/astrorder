import { IconCode } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { MonacoViewer } from './MonacoViewer'

export const monacoViewer: ArtifactViewer = {
  id: 'monaco-viewer',
  title: 'Monaco 代码与文本编辑器',
  description: '全功能 VSCode 内核代码编辑器，支持全语法高亮、缩略图导航、行号与修改写回',
  version: '1.0.0',
  badgeLabel: '[代码]',
  tabColor: 'var(--astr-indigo)',
  icon: IconCode,
  extensions: [
    '.ts', '.tsx', '.js', '.jsx', '.py', '.json', '.rs', '.go', '.c', '.cpp',
    '.h', '.css', '.scss', '.sql', '.yaml', '.yml', '.txt', '.log', '.toml',
    '.ini', '.env', '.dockerfile', '.sh', '.bash', '.cmd', '.bat', '.ps1',
  ],
  mimeTypes: ['text/plain', 'application/json', 'text/javascript', 'text/x-python'],
  component: MonacoViewer,
  capabilities: {
    canEdit: true,
  },
  configOptions: [
    {
      key: 'minimap',
      label: '启用代码缩略图 (Minimap)',
      type: 'boolean',
      defaultValue: true,
    },
    {
      key: 'fontSize',
      label: '代码字体大小',
      type: 'number',
      defaultValue: 13,
    },
  ],
}
