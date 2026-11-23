import { useEffect, useMemo, useState } from 'react'
import { ActionIcon, SegmentedControl, Tooltip } from '@mantine/core'
import { ArrowRightIcon, XIcon } from '../../components/workbenchIcons'
import type {
  Agent,
  Attachment,
  Message,
  Session,
  SessionSchedule,
  SessionSwarm,
} from '../../domain/types'
import type { ArtifactRef } from '../../domain/artifact'
import { isAntigravityAgent } from '../../hooks/useSessionSwarm'
import { artifactViewerRegistry } from '../sidecar/registry'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { SessionSchedulesView } from './SessionSchedulesView'
import { SessionSwarmView } from './SessionSwarmView'
import './workspaceChanges.css'

type PanelSection = 'swarm' | 'schedules' | 'outputs'

export function WorkspaceChangesContent({
  session,
  agent,
  messages,
  swarm,
  swarmLoading = false,
  swarmFetching = false,
  swarmError,
  onRefreshSwarm,
  schedules = [],
  schedulesLoading = false,
  schedulesError,
  onOpenSession,
  onClose,
}: {
  session: Session
  agent?: Agent
  messages: Message[]
  swarm?: SessionSwarm
  swarmLoading?: boolean
  swarmFetching?: boolean
  swarmError?: unknown
  onRefreshSwarm?: () => void
  schedules?: SessionSchedule[]
  schedulesLoading?: boolean
  schedulesError?: unknown
  onOpenSession?: (agentId: string, sessionId: string) => void
  onClose: () => void
}) {
  const showSwarm = isAntigravityAgent(agent)
  const [userPickedSection, setUserPickedSection] = useState(false)
  const [section, setSection] = useState<PanelSection>(() => {
    if (showSwarm && swarm?.available) return 'swarm'
    if (schedules.length > 0) return 'schedules'
    return 'outputs'
  })

  useEffect(() => {
    if (!showSwarm && section === 'swarm') {
      setSection(schedules.length > 0 ? 'schedules' : 'outputs')
      return
    }
    if (!userPickedSection) {
      if (showSwarm && swarm?.available) {
        setSection('swarm')
      } else if (schedules.length > 0) {
        setSection('schedules')
      }
    }
  }, [showSwarm, swarm?.available, schedules.length, userPickedSection, section])

  // Only explicit Agent outputs are artifacts. Paths mentioned in prose,
  // uploaded user references and opened editors are not produced artifacts.
  const outputs = useMemo(
    () => [
      ...new Map(
        messages
          .filter((message) => message.role === 'assistant')
          .flatMap((message) => message.attachments)
          .filter((attachment) => Boolean(attachment.url))
          .map((attachment) => [attachment.id, attachment]),
      ).values(),
    ],
    [messages],
  )

  function openOutput(attachment: Attachment) {
    const artifact: ArtifactRef = {
      id: `attachment:${session.agent_id}:${session.id}:${attachment.id}`,
      sessionId: session.id,
      agentId: session.agent_id,
      connectionId: session.connection_id || undefined,
      name: attachment.name,
      mediaType: attachment.media_type,
      readUrl: attachment.url,
      kind: 'attachment',
      writable: false,
    }
    const viewer = artifactViewerRegistry.findViewer(artifact)
    useSidecarStore.getState().openArtifact(artifact, viewer?.id || '')
    onClose()
  }

  const swarmCount = swarm?.available ? swarm.members.length : 0
  const scheduleCount = schedules.length
  const effectiveSection: PanelSection = !showSwarm && section === 'swarm' ? 'outputs' : section
  const headingTitle =
    effectiveSection === 'swarm'
      ? 'Swarm 团队'
      : effectiveSection === 'schedules'
        ? '定时任务'
        : '产物'
  const headingCount =
    effectiveSection === 'swarm'
      ? swarmCount
      : effectiveSection === 'schedules'
        ? scheduleCount
        : outputs.length

  const tabOptions = showSwarm
    ? [
        { value: 'swarm', label: `Swarm ${swarmCount}` },
        { value: 'schedules', label: `定时 ${scheduleCount}` },
        { value: 'outputs', label: `产物 ${outputs.length}` },
      ]
    : [
        { value: 'schedules', label: `定时 ${scheduleCount}` },
        { value: 'outputs', label: `产物 ${outputs.length}` },
      ]

  return (
    <section className="workbench-overview">
      <header className="workbench-overview-heading">
        <h3>{headingTitle}</h3>
        <span className="workbench-count">{headingCount}</span>
        {showSwarm && effectiveSection === 'swarm' && onRefreshSwarm && (
          <Tooltip label="刷新 Swarm">
            <ActionIcon
              variant="subtle"
              aria-label="刷新 Swarm"
              loading={swarmFetching}
              onClick={onRefreshSwarm}
            >
              ↻
            </ActionIcon>
          </Tooltip>
        )}
        <Tooltip label="收起面板">
          <ActionIcon
            variant="subtle"
            aria-label="收起面板"
            className="workbench-close-btn"
            onClick={onClose}
          >
            <XIcon size={16} />
          </ActionIcon>
        </Tooltip>
      </header>
      <SegmentedControl
        className="workbench-sections"
        fullWidth
        size="xs"
        value={effectiveSection}
        onChange={(val) => {
          setUserPickedSection(true)
          setSection(val as PanelSection)
        }}
        data={tabOptions}
      />
      {effectiveSection === 'swarm' ? (
        <SessionSwarmView
          session={session}
          agent={agent}
          swarm={swarm}
          loading={swarmLoading}
          fetching={swarmFetching}
          error={swarmError}
          onRefresh={() => onRefreshSwarm?.()}
          onOpenSession={(aid, sid) => onOpenSession?.(aid, sid)}
        />
      ) : effectiveSection === 'schedules' ? (
        <SessionSchedulesView
          session={session}
          agent={agent}
          schedules={schedules}
          loading={schedulesLoading}
          error={schedulesError}
          onOpenSession={(aid, sid) => onOpenSession?.(aid, sid)}
        />
      ) : (
        <div className="workbench-output-list">
          {outputs.length === 0 ? (
            <p className="workbench-empty">当前已加载的会话中暂无 Agent 输出附件</p>
          ) : (
            outputs.map((output) => (
              <button
                type="button"
                className="workbench-file"
                key={output.id}
                onClick={() => openOutput(output)}
              >
                <span className="workbench-file-top">
                  <strong>{output.name}</strong>
                  <ArrowRightIcon size={15} />
                </span>
                <span className="workbench-file-path">{output.media_type}</span>
              </button>
            ))
          )}
        </div>
      )}
    </section>
  )
}
