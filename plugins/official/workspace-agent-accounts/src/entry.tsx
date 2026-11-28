import { Suspense } from 'react'
import { Route, Routes } from 'react-router-dom'
import { Center, Loader } from '@mantine/core'
import { IconId } from '@tabler/icons-react'
import { defineAstrorderPlugin, type AstrorderPluginContext } from '@astrorder/sdk'
import { workbenchIcons } from '@astrorder/workspace-bridge'
import { AgentAccountsLayout } from '../../../../frontend/src/features/agentAccounts/AgentAccountsLayout'
import { accountPlatforms } from '../../../../frontend/src/features/agentAccounts/accountPlatforms'
import { UnavailablePlatformPage } from '../../../../frontend/src/features/agentAccounts/UnavailablePlatformPage'

function AccountsSuspenseFallback() {
  return (
    <Center style={{ height: '100%', minHeight: 200 }}>
      <Loader size="sm" color="violet" />
    </Center>
  )
}

function AgentAccountsRoutedPage() {
  return (
    <Routes>
      <Route element={<AgentAccountsLayout />}>
        {accountPlatforms.map(({ kind, path, Page }) => (
          <Route
            key={kind}
            index={!path}
            path={path || undefined}
            element={
              <Suspense fallback={<AccountsSuspenseFallback />}>
                {Page ? <Page /> : <UnavailablePlatformPage kind={kind} />}
              </Suspense>
            }
          />
        ))}
      </Route>
    </Routes>
  )
}

export function activate(ctx: AstrorderPluginContext): void {
  ctx.registerPage({
    id: 'agentAccounts',
    title: 'Agent 账号管理',
    navLabel: '账号',
    icon: workbenchIcons.IdentificationCardIcon,
    mobileIcon: IconId,
    path: '/agent-accounts',
    routePaths: ['/agent-accounts', '/agent-accounts/*'],
    order: 80,
    hideInOfficeMode: true,
    hasSecondaryPanel: false,
    showInSidebar: true,
    showInMobileApps: false,
    component: AgentAccountsRoutedPage,
    command: {
      id: 'route:accounts',
      title: 'Agent 账号管理',
      subtitle: '/agent-accounts',
      to: '/agent-accounts',
      keywords: 'accounts quota 账号 额度',
      icon: IconId,
    },
  })
}

export function deactivate(): void {}

export default defineAstrorderPlugin({ activate, deactivate })
