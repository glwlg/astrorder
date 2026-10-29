import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { BrowserEngineSettingsCard } from './BrowserEngineSettingsCard'

vi.mock('@mantine/notifications', () => ({ notifications: { show: vi.fn() } }))
afterEach(() => { cleanup(); vi.restoreAllMocks() })

it('loads and explicitly saves the selected upstream browser engine', async () => {
  vi.spyOn(api, 'getBrowserEngine').mockResolvedValue({ engine: 'browser_use' })
  vi.spyOn(api, 'updateBrowserEngine').mockResolvedValue({ engine: 'jev_ultrafast' })
  render(<MantineProvider><BrowserEngineSettingsCard /></MantineProvider>)
  await waitFor(() => expect(screen.getByLabelText('Jev Ultrafast')).not.toBeDisabled())
  fireEvent.click(screen.getByLabelText('Jev Ultrafast'))
  fireEvent.click(screen.getByRole('button', { name: '保存配置' }))
  await waitFor(() => expect(api.updateBrowserEngine).toHaveBeenCalledWith('jev_ultrafast'))
})
