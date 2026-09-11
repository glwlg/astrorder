import { IconTerminal2 } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { XtermViewer } from './XtermViewer'

export const xtermViewer: ArtifactViewer = {
  id: 'xterm-viewer',
  title: '交互式终端 (Xterm.js)',
  description: '支持 PTY 虚拟终端交互、ANSI 颜色代码回显及 Shell 命令行控制',
  version: '1.0.0',
  badgeLabel: '[终端]',
  tabColor: '#eab308',
  icon: IconTerminal2,
  extensions: ['.terminal'],
  mimeTypes: ['application/x-terminal'],
  component: XtermViewer,
  capabilities: {
    canEdit: true,
  },
  configOptions: [
    {
      key: 'fontSize',
      label: '终端字体大小',
      type: 'number',
      defaultValue: 13,
    },
    {
      key: 'cursorBlink',
      label: '光标呼吸闪烁',
      type: 'boolean',
      defaultValue: true,
    },
  ],
}
