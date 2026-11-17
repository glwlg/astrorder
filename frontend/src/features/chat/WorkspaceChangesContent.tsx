import { useMemo, useState } from 'react'
import { ActionIcon, SegmentedControl, Tooltip } from '@mantine/core'
import { ArrowRightIcon, XIcon } from '../../components/workbenchIcons'
import { useGitStatus } from '../../hooks/useGitStatus'
import type { Attachment, Message, Session } from '../../domain/types'
import type { ArtifactRef } from '../../domain/artifact'
import { artifactViewerRegistry } from '../sidecar/registry'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { WorkspaceDiffPreview } from './WorkspaceDiffPreview'
import './workspaceChanges.css'

function statusLabel(status: string) {
  if (status === 'untracked' || status === '??') return '未跟踪'
  if (status === 'deleted' || status.includes('D')) return '已删除'
  if (status === 'added' || status.includes('A')) return '新增'
  return '修改'
}

export function WorkspaceChangesContent({ session, messages, onClose }: {
  session: Session
  messages: Message[]
  onClose: () => void
}) {
  const git = useGitStatus(session)
  const [section, setSection] = useState('files')
  const [selectedPath, setSelectedPath] = useState<string | null>(null)
  const files = git.isError ? [] : git.data?.files || []
  const path = files.find(file => file.path === selectedPath)?.path || files[0]?.path
  // Only explicit Agent outputs are artifacts. Paths mentioned in prose,
  // uploaded user references and opened editors are not produced artifacts.
  const outputs = useMemo(() => [...new Map(
    messages.filter(message => message.role === 'assistant')
      .flatMap(message => message.attachments)
      .filter(attachment => Boolean(attachment.url))
      .map(attachment => [attachment.id, attachment]),
  ).values()], [messages])

  function openOutput(attachment: Attachment) {
    const artifact: ArtifactRef = {
      id: `attachment:${session.agent_id}:${session.id}:${attachment.id}`,
      sessionId: session.id, agentId: session.agent_id,
      connectionId: session.connection_id || undefined,
      name: attachment.name, mediaType: attachment.media_type,
      readUrl: attachment.url, kind: 'attachment', writable: false,
    }
    const viewer = artifactViewerRegistry.findViewer(artifact)
    useSidecarStore.getState().openArtifact(artifact, viewer?.id || '')
    onClose()
  }

  return (
      <section className="workbench-overview">
        <header className="workbench-overview-heading">
          <h3>工作区变更</h3>
          {git.data && !git.isError && <span className="workbench-count">{files.length}</span>}
          <Tooltip label="刷新变更">
            <ActionIcon variant="subtle" aria-label="刷新变更" loading={git.isFetching}
              disabled={!session.workspace} onClick={() => void git.refetch()}>↻</ActionIcon>
          </Tooltip>
          <Tooltip label="收起变更">
            <ActionIcon variant="subtle" aria-label="收起变更" onClick={onClose}><XIcon size={16} /></ActionIcon>
          </Tooltip>
        </header>
        <SegmentedControl className="workbench-sections" fullWidth size="xs"
          value={section} onChange={setSection} data={[
            { value: 'files', label: `文件 ${files.length}` },
            { value: 'outputs', label: `产物 ${outputs.length}` },
          ]} />
        {section === 'files' ? (
          <div className="workbench-files">
            {!session.workspace ? <p className="workbench-empty">此会话未关联工作区</p>
              : git.isError ? <p className="workbench-empty" role="alert">无法读取工作区变更，请刷新重试。</p>
              : git.isPending ? <p className="workbench-empty" role="status">正在读取变更…</p>
              : files.length === 0 ? <p className="workbench-empty">工作区没有未提交的变更</p>
              : <>
                <div className="workbench-change-summary">
                  <span>{files.length} 个文件</span>
                  <span><b className="diff-added">+{git.data?.insertions}</b><b className="diff-deleted">−{git.data?.deletions}</b></span>
                </div>
                <div className="workbench-file-list" aria-label="变更文件">
                  {files.map(file => (
                    <button key={file.path} type="button" className="workbench-file"
                      aria-pressed={path === file.path} title={file.path}
                      onClick={() => setSelectedPath(file.path)}>
                      <span className="workbench-file-top"><strong>{file.path.split('/').pop()}</strong>
                        <small>{file.staged ? '已暂存' : statusLabel(file.status)}</small></span>
                      <span className="workbench-file-path">{file.path}</span>
                    </button>
                  ))}
                </div>
                {path && <WorkspaceDiffPreview session={session} path={path} revision={git.dataUpdatedAt} onOpen={onClose} />}
              </>}
          </div>
        ) : (
          <div className="workbench-output-list">
            {outputs.length === 0 ? <p className="workbench-empty">当前已加载的会话中暂无 Agent 输出附件</p>
              : outputs.map(output => (
                <button type="button" className="workbench-file" key={output.id} onClick={() => openOutput(output)}>
                  <span className="workbench-file-top"><strong>{output.name}</strong><ArrowRightIcon size={15} /></span>
                  <span className="workbench-file-path">{output.media_type}</span>
                </button>
              ))}
          </div>
        )}
      </section>
  )
}
