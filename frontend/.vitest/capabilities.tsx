import React from 'react'
import { createRoot } from 'react-dom/client'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import '@mantine/core/styles.css'
import '@mantine/notifications/styles.css'
import '../src/index.css'
import { CapabilitiesSettingsCard } from '../src/features/services/CapabilitiesSettingsCard'

// Isolated visual harness. API data comes from the test runner; never invokes models.
createRoot(document.getElementById('root')!).render(
  <MantineProvider defaultColorScheme="light">
    <Notifications />
    <div style={{ height: '100vh', display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
      <CapabilitiesSettingsCard />
    </div>
  </MantineProvider>,
)
