import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { DigestPage } from './DigestPage'

describe('DigestPage', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('collects work materials, generates report, and saves to history', async () => {
    vi.spyOn(api, 'listDigestTemplates').mockResolvedValue({
      items: [
        {
          id: 'weekly_exec',
          name: '周报 · 汇报版',
          description: '管理层周报模板',
          audience: '管理者',
          built_in: true,
          system_prompt: '生成周报',
        },
      ],
    })
    vi.spyOn(api, 'listDigestHistory').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'collectDigest').mockResolvedValue({
      from: '2026-11-23T00:00:00Z',
      to: '2026-11-25T23:59:59Z',
      generated_at: '2026-11-25T14:00:00Z',
      llm_configured: true,
      llm_model: 'deepseek-chat',
      available_projects: [{ project_key: 'astrorder', project_name: 'Astrorder' }],
      projects: [
        {
          project_key: 'astrorder',
          project_name: 'Astrorder',
          workspace: '/workspace/astrorder',
          session_count: 1,
          commit_count: 1,
          task_count: 0,
          sql_audit_count: 0,
          files_changed: 8,
          insertions: 320,
          deletions: 10,
          items: [
            {
              item_id: 'item-1',
              kind: 'session',
              project_key: 'astrorder',
              project_name: 'Astrorder',
              title: '实现星序办公套件',
              summary: '完成 Word/Excel 导出和周报工作台。',
              timestamp: '2026-11-25T12:00:00Z',
              selected: true,
            },
          ],
        },
      ],
      stats: {
        project_count: 1,
        session_count: 1,
        commit_count: 1,
        task_count: 0,
        checkpoint_count: 1,
        sql_audit_count: 0,
        files_changed: 8,
        insertions: 320,
        deletions: 10,
      },
    })
    const genSpy = vi.spyOn(api, 'generateDigest').mockResolvedValue({
      ok: true,
      mode: 'llm',
      llm_configured: true,
      model: 'deepseek-chat',
      template_id: 'weekly_exec',
      template_name: '周报 · 汇报版',
      title: '第 48 周工作周报',
      markdown: '## 本周核心成果\n\n- 上线了星序办公套件与周报工作台。',
      assembled_prompt: 'prompt',
    })
    const saveSpy = vi.spyOn(api, 'saveDigestHistory').mockResolvedValue({
      id: 'hist-1',
      title: '第 48 周工作周报',
      template_id: 'weekly_exec',
      template_name: '周报 · 汇报版',
      from: '2026-11-23',
      to: '2026-11-25',
      markdown: '## 本周核心成果\n\n- 上线了星序办公套件与周报工作台。',
      item_count: 1,
      created_at: '2026-11-25T14:00:00Z',
      updated_at: '2026-11-25T14:00:00Z',
    })

    render(
      <MemoryRouter>
        <MantineProvider>
          <DigestPage />
        </MantineProvider>
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(screen.getByText('实现星序办公套件')).toBeTruthy()
    })

    fireEvent.click(screen.getByRole('button', { name: '一键生成报告' }))
    await waitFor(() => {
      expect(genSpy).toHaveBeenCalledTimes(1)
      expect(screen.getByText('本周核心成果')).toBeTruthy()
    })

    fireEvent.click(screen.getByRole('button', { name: '保存归档' }))
    await waitFor(() => {
      expect(saveSpy).toHaveBeenCalledTimes(1)
    })
  })
})
