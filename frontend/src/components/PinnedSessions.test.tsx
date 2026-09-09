import { MantineProvider } from '@mantine/core'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { SessionRail } from './SessionRail'
import { scopeKey } from '../domain/semantics'
import type { Session } from '../domain/types'

it('places pinned sessions above every project without duplication and keeps the 24-hour tab', () => {
 const pinned: Session = { id: 'pinned', agent_id: 'a', title: 'Pinned session', workspace: '/repo', project_name: 'Project', status: 'idle', updated_at: new Date().toISOString() }
 const old = { ...pinned, id: 'old', title: 'Old session', updated_at: new Date(Date.now() - 25 * 3600000).toISOString() }
 const values = new Map([['astrorder_pinned_sessions', JSON.stringify({ [scopeKey('a', 'pinned')]: true })]])
 vi.stubGlobal('localStorage', { getItem: (key: string) => values.get(key) || null, setItem: (key: string, value: string) => values.set(key, value) })
 const view = render(<MantineProvider><SessionRail sessions={[old, pinned]} agents={{}} onSelect={() => {}} /></MantineProvider>)
 const region = screen.getByRole('region', { name: '置顶会话' })
 expect(within(region).getByText('Pinned session')).toBeVisible()
 const project = view.container.querySelector('.session-project-group')!
 expect(region.compareDocumentPosition(project) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
 expect(project.textContent).not.toContain('Pinned session')
 fireEvent.click(screen.getByRole('tab', { name: '24小时' }))
 expect(screen.queryByText('Old session')).not.toBeInTheDocument()
 expect(within(region).getByText('Pinned session')).toBeVisible()
})
