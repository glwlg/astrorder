import path from 'node:path'
import { fileURLToPath } from 'node:url'

const sharedDir = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(sharedDir, '..', '..', '..')
const frontendSrcDir = path.join(repoRoot, 'frontend', 'src')

const VIRTUAL_PREFIX = '\0astrorder-ws-bridge:'

const BRIDGE_SHIMS = {
  'api/client': `
import { client } from '@astrorder/workspace-bridge'
export const { api, ApiError, request, authenticatedApiFetch } = client
export default client
`,
  'api/learning': `
import { learningApi } from '@astrorder/workspace-bridge'
export { learningApi }
`,
  'components/AgentBrandIcon': `
import { AgentBrandIcon, agentKindLabel } from '@astrorder/workspace-bridge'
export { AgentBrandIcon, agentKindLabel }
`,
  'components/AgentSessionFilter': `
import { AgentSessionFilter as M } from '@astrorder/workspace-bridge'
export const { connectionName, matchesAgent, AgentSessionFilter } = M
`,
  'components/BrandedSelect': `
import { BrandedSelect } from '@astrorder/workspace-bridge'
export { BrandedSelect }
`,
  'components/CreateBotGroupDialog': `
import { CreateBotGroupDialog } from '@astrorder/workspace-bridge'
export { CreateBotGroupDialog }
`,
  'components/EmptyState': `
import { EmptyState } from '@astrorder/workspace-bridge'
export { EmptyState }
`,
  'components/LazyDetails': `
import { LazyDetails } from '@astrorder/workspace-bridge'
export { LazyDetails }
`,
  'components/MarkdownContent': `
import { MarkdownContent } from '@astrorder/workspace-bridge'
export { MarkdownContent }
`,
  'components/MessageBody': `
import { MessageBody } from '@astrorder/workspace-bridge'
export { MessageBody }
`,
  'components/animations/ShinyText': `
import { ShinyText } from '@astrorder/workspace-bridge'
export { ShinyText }
`,
  'components/animations/useSpotlightSurface': `
import { useSpotlightSurface } from '@astrorder/workspace-bridge'
export { useSpotlightSurface }
`,
  'components/sessionRailModel': `
import { displaySessionTitle, isHiddenRailSession, resolveSessionDisplayTitle } from '@astrorder/workspace-bridge'
export { displaySessionTitle, isHiddenRailSession, resolveSessionDisplayTitle }
`,
  'components/workbenchIcons': `
import { workbenchIcons } from '@astrorder/workspace-bridge'
export const {
  ChartLineUpIcon,
  CompassIcon,
  HardDrivesIcon,
  IdentificationCardIcon,
  MicrophoneIcon,
  NotebookIcon,
  SquaresFourIcon,
  UsersThreeIcon,
} = workbenchIcons
export default workbenchIcons
`,
  'domain/semantics': `
import { newCommandId, scopeKey } from '@astrorder/workspace-bridge'
export { newCommandId, scopeKey }
`,
  'domain/swarmTopology': `
import { swarmTopology } from '@astrorder/workspace-bridge'
export const { buildSwarmTopology } = swarmTopology
export default swarmTopology
`,
  'hooks/useAstrorderData': `
import { useSessionResources } from '@astrorder/workspace-bridge'
export { useSessionResources }
`,
  'hooks/useUserIdle': `
import { isUserIdle, subscribeUserIdle, useUserIdle } from '@astrorder/workspace-bridge'
export { isUserIdle, subscribeUserIdle, useUserIdle }
`,
  'state/store': `
import {
  useAstrorderStore,
  selectApprovals,
  selectCommands,
  selectMessages,
  selectSessions,
} from '@astrorder/workspace-bridge'
export {
  useAstrorderStore,
  selectApprovals,
  selectCommands,
  selectMessages,
  selectSessions,
}
`,
  'features/agentAccounts/AgyQuotaRefreshProvider': `
import { AgyQuotaRefreshProvider as M } from '@astrorder/workspace-bridge'
export const {
  AgyQuotaRefreshProvider,
  sessionAccountTarget,
  agySessionEnvironment,
  useAccountPageRefreshTarget,
} = M
`,
  'features/agents/ForceAgentRestartDialog': `
import { ForceAgentRestartDialog } from '@astrorder/workspace-bridge'
export { ForceAgentRestartDialog }
`,
  'features/chat/ApprovalModeControl': `
import { ApprovalModeControl } from '@astrorder/workspace-bridge'
export { ApprovalModeControl }
`,
  'features/chat/BotGroupChatPage': `
import { BotGroupChatPage } from '@astrorder/workspace-bridge'
export { BotGroupChatPage }
`,
  'features/chat/GroupSidecarPanel': `
import { GroupSidecarPanel } from '@astrorder/workspace-bridge'
export { GroupSidecarPanel }
`,
  'features/chat/SessionModelControl': `
import { SessionModelControl } from '@astrorder/workspace-bridge'
export { SessionModelControl }
`,
  'features/chat/commandActions': `
import { commandActions } from '@astrorder/workspace-bridge'
export const { submitBrowserCommand } = commandActions
`,
  'features/chat/composerMedia': `
import { composerMedia } from '@astrorder/workspace-bridge'
export const { clipboardFiles } = composerMedia
`,
  'features/chat/draftStorage': `
import { draftStorage } from '@astrorder/workspace-bridge'
export const { draftStorage: storage, usePersistentDraft } = draftStorage
export { storage as draftStorage }
`,
  'features/chat/localVoiceStream': `
import { localVoiceStream } from '@astrorder/workspace-bridge'
export const {
  buildLocalVoiceStreamUrl,
  downsampleToPcm16,
  startLocalVoiceStream,
} = localVoiceStream
`,
  'features/chat/toolPresentation': `
import { describeTool, PackSummary, ToolLineIcon } from '@astrorder/workspace-bridge'
export { describeTool, PackSummary, ToolLineIcon }
`,
  'features/meetings/meetingSessionStore': `
import { meetingSessionStore } from '@astrorder/workspace-bridge'
export const {
  MEETING_SETTINGS_SYNC_KEY,
  AI_PROVIDERS_SYNC_KEY,
  broadcastMeetingSettingsSync,
  broadcastAiProvidersSync,
  DEFAULT_MEETING_SETTINGS,
  todayDateString,
  parseParticipants,
  segmentsToTranscript,
  segmentsToPlainTranscript,
  transcriptToSegments,
  sampleCapsuleBars,
  buildCapsuleDisplayText,
  readCrashDraftSnapshot,
  ensureMeetingCapsuleActionListener,
  useMeetingSessionStore,
} = meetingSessionStore
`,
  'features/mobile/mobileOutbox': `
import { mobileOutbox } from '@astrorder/workspace-bridge'
export default mobileOutbox
`,
  'features/mobile/mobileOutboxStorage': `
import { mobileOutboxStorage } from '@astrorder/workspace-bridge'
export const { mobileOutboxStorage: storage } = mobileOutboxStorage
export { storage as mobileOutboxStorage }
`,
  'features/mobile/serverOutbox': `
import { sharedServerOutbox } from '@astrorder/workspace-bridge'
export { sharedServerOutbox }
`,
  'features/office/appProfile': `
import { useIsOfficeProfile } from '@astrorder/workspace-bridge'
export { useIsOfficeProfile }
`,
  'features/office/export/ExportMenu': `
import { ExportMenu } from '@astrorder/workspace-bridge'
export { ExportMenu }
`,
  'features/plugins/pluginSettingsStore': `
import { usePluginSettingsStore } from '@astrorder/workspace-bridge'
export { usePluginSettingsStore }
`,
  'features/sidecar/sidecarStore': `
import { groupSidecarKey, useSidecarStore } from '@astrorder/workspace-bridge'
export { groupSidecarKey, useSidecarStore }
`,
}

function normalizeFrontendKey(absPath) {
  const rel = path.relative(frontendSrcDir, absPath).replace(/\\/g, '/')
  if (!rel || rel.startsWith('..') || path.isAbsolute(rel)) return null
  return rel.replace(/\.(?:tsx?|jsx?|mjs|cjs)$/, '')
}

/**
 * Vite plugin for official Category-3 workspace ESM plugins: redirects shared
 * host imports inside `frontend/src/` to `@astrorder/workspace-bridge` so
 * plugins reuse the host's live singletons (Zustand stores, API client, React
 * contexts, MarkdownContent, ExportMenu) without bundling host internals.
 */
export function vitePluginWorkspaceBridge(options = {}) {
  const excluded = new Set(options.excludeModules || [])
  return {
    name: 'astrorder-workspace-bridge',
    enforce: 'pre',
    resolveId(source, importer) {
      if (!importer || !source.startsWith('.')) return null
      if (importer.startsWith('\0')) return null
      const absTarget = path.resolve(path.dirname(importer), source)
      const key = normalizeFrontendKey(absTarget)
      if (!key || excluded.has(key)) return null
      if (Object.prototype.hasOwnProperty.call(BRIDGE_SHIMS, key)) {
        return `${VIRTUAL_PREFIX}${key}`
      }
      return null
    },
    load(id) {
      if (!id.startsWith(VIRTUAL_PREFIX)) return null
      const key = id.slice(VIRTUAL_PREFIX.length)
      return BRIDGE_SHIMS[key] || null
    },
  }
}
