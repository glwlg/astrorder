import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { PluginsPage } from './PluginsPage'

describe('PluginsPage Component', () => {
  it('renders plugin management header and list of viewers', () => {
    render(
      <MantineProvider>
        <PluginsPage />
      </MantineProvider>,
    )

    expect(screen.getByText('插件与工件查看器管理')).toBeInTheDocument()
    expect(screen.getByText('Draw.io 架构与流程图')).toBeInTheDocument()
    expect(screen.getByText('Mermaid 架构图')).toBeInTheDocument()
    expect(screen.getByText('Excalidraw 白板')).toBeInTheDocument()
    expect(screen.getByText('代码变更比对 (Diff)')).toBeInTheDocument()
    expect(screen.getByText('3D 模型/CAD 预览')).toBeInTheDocument()
    expect(screen.getByText('HTML 页面预览')).toBeInTheDocument()
    expect(screen.getByText('Agent 决策状态机')).toBeInTheDocument()
  })
})
