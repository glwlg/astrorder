import { ActionIcon, Button, Tooltip } from "@mantine/core";
import { IconChalkboard, IconFolderSearch } from "@tabler/icons-react";
import { notifications } from "@mantine/notifications";
import { api } from "../../api/client";
import type { BotGroup, GroupFileReference, Session } from "../../domain/types";
import type { ArtifactRef } from "../../domain/artifact";
import { scopeKey } from "../../domain/semantics";
import { useAstrorderStore } from "../../state/store";
import { artifactViewerRegistry } from "../sidecar/registry";
import { SidecarHeader } from "../sidecar/SidecarHeader";
import { groupSidecarKey, useSidecarStore } from "../sidecar/sidecarStore";
import { SidecarBlackboardPanel } from "./SidecarBlackboardPanel";
import { GroupFileSourceScope } from "./GroupFileSourceScope";
import { groupFileSession } from "../../components/GroupFileLink";
import { FileSourceContext } from "../sidecar/FileSourceContext";

export function GroupSidecarPanel({ group }: { group: BotGroup }) {
  const tabs = useSidecarStore((state) => state.tabs);
  const mountedTabs = useSidecarStore((state) => state.mountedTabs);
  const activeTabId = useSidecarStore((state) => state.activeTabId);
  const dirtyTabs = useSidecarStore((state) => state.dirtyTabs);
  const isOpen = useSidecarStore((state) => state.isOpen);
  const sessions = useAstrorderStore((state) => state.sessions);
  const ownerKey = groupSidecarKey(group.id);
  const store = useSidecarStore.getState();
  const activeTab = tabs.find((tab) => tab.id === activeTabId);
  const retainedTabs = [
    ...tabs.map((tab) => [ownerKey, tab] as const),
    ...Object.entries(mountedTabs).flatMap(([key, items]) =>
      key === ownerKey || !key.startsWith("group:") ? [] : items.filter((tab) => dirtyTabs[tab.id]).map((tab) => [key, tab] as const)),
  ];

  const sourceSession = (artifact: ArtifactRef): Session | null => {
    const reference = artifact.metadata?.groupFileReference as GroupFileReference | undefined;
    if (reference?.group_id === group.id && reference.agent_id === artifact.agentId && reference.session_id === artifact.sessionId) {
      return groupFileSession(reference);
    }
    const stored = sessions[scopeKey(artifact.agentId, artifact.sessionId)];
    if (stored) return stored;
    const member = group.members.find((candidate) => candidate.agent_id === artifact.agentId &&
      (group.member_states?.[candidate.id || candidate.agent_id]?.session_id === artifact.sessionId || candidate.fork_session_id === artifact.sessionId));
    return member ? {
      id: artifact.sessionId, agent_id: artifact.agentId, connection_id: artifact.connectionId,
      workspace: member.workspace || null, title: member.alias || member.name || member.agent_id,
      status: "idle", updated_at: group.updated_at,
    } : null;
  };
  const save = async (artifact: ArtifactRef, content: string) => {
    if (!artifact.path) return false;
    try {
      await api.writeFile({ path: artifact.path, content, session_id: artifact.sessionId, connection_id: artifact.connectionId });
      return true;
    } catch (error) {
      notifications.show({ color: "red", message: error instanceof Error ? error.message : "保存文件失败" });
      return false;
    }
  };

  return <GroupFileSourceScope groupId={group.id} revision={group.updated_at}>
  <aside className="group-sidecar" aria-label="群聊工作台">
    <SidecarHeader tabs={tabs} activeId={activeTabId} dirtyTabs={dirtyTabs}
      onSelect={store.setActiveTabId} onCloseTab={store.closeTab}
      onClose={() => store.setIsOpen(false)} closeLabel="收起群聊工作台" />
    <div className="group-sidecar-body">
      {!activeTab && <div className="group-sidecar-empty">
        <Button variant="light" leftSection={<IconChalkboard size={16} />} onClick={() => store.openGroupBlackboard(group.id, group.name)}>打开群聊黑板</Button>
      </div>}
      {retainedTabs.flatMap(([key, tab]) => {
        if (!tab.artifact) return [];
        const artifact = tab.artifact;
        const isActive = isOpen && key === ownerKey && tab.id === activeTabId;
        if (tab.id === `blackboard:${ownerKey}`) return <div key={`${key}:${tab.id}`} hidden={!isActive} style={{ height: "100%" }}>
          <SidecarBlackboardPanel
            namespace={ownerKey}
            title={`群聊黑板 · ${group.name}`}
            onClose={() => store.setIsOpen(false)}
            focusKey={typeof artifact.metadata?.focusKey === "string" ? artifact.metadata.focusKey : undefined}
            focusRequest={Number(artifact.metadata?.focusRequest || 0)}
          />
        </div>;
        const viewer = artifactViewerRegistry.findViewer(artifact);
        if (!viewer) return <div key={`${key}:${tab.id}`} hidden={!isActive}>暂无支持的查看器</div>;
        const Viewer = viewer.component;
        const session = sourceSession(artifact);
        const toolbarAction = artifact.path && artifact.mediaType !== "application/x-directory" && session?.workspace ? (
          <Tooltip label="在文件树中定位">
            <ActionIcon variant="subtle" size="sm" color="gray" aria-label="在文件树中定位" onClick={() => store.revealFileInTree(
              artifact.sessionId, artifact.agentId, artifact.path!, session.workspace!, session.title, artifact.connectionId,
            )}><IconFolderSearch size={16} /></ActionIcon>
          </Tooltip>
        ) : undefined;
        return <div key={`${key}:${tab.id}`} hidden={!isActive} style={{ height: "100%", minHeight: 0 }}>
          <FileSourceContext value={null}><Viewer artifact={artifact} session={session} isActive={isActive} toolbarAction={toolbarAction}
            onDirtyChange={(dirty) => store.setTabDirty(tab.id, dirty)}
            onSave={(content) => save(artifact, content)} onClose={() => store.closeTab(tab.id)} /></FileSourceContext>
        </div>;
      })}
    </div>
  </aside>
  </GroupFileSourceScope>;
}
