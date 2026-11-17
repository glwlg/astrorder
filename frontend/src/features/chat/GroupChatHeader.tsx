import type { ReactNode } from "react";
import { ActionIcon, Tooltip } from "@mantine/core";
import { IconPlayerPause, IconPlayerPlay, IconUsers, IconVolumeOff } from "@tabler/icons-react";
import type { Agent, BotGroup, BotGroupMemberState } from "../../domain/types";
import { GroupChatAvatar } from "./GroupChatAvatar";
import { groupMemberStatus } from "./groupChatStatus";
import { groupMemberTone } from "./groupChatPresentation";
import "./groupChat.css";

export function GroupChatHeader({ group, agents, memberStates, coordination, activeSpeakers, status, currentHop, maxHops, onToggleMuted, muteBusy, actions, children, motionPaused, onToggleMotion }: {
  group: BotGroup;
  agents: Record<string, Agent>;
  memberStates: Record<string, BotGroupMemberState>;
  coordination: BotGroup["coordination"];
  activeSpeakers: string[];
  status?: { label: string; color: string };
  currentHop: number | "—";
  maxHops: number;
  onToggleMuted: (ids: string[], muted: boolean) => void;
  muteBusy: boolean;
  actions: ReactNode;
  children?: ReactNode;
  motionPaused: boolean;
  onToggleMotion: () => void;
}) {
  const memberIds = group.members.map((member) => member.id || member.agent_id);
  const mutedCount = memberIds.filter((id) => memberStates[id]?.muted).length;
  const allMuted = memberIds.length > 0 && mutedCount === memberIds.length;
  const live = coordination?.status === "deciding" ||
    ((!coordination || coordination.status === "running") && group.members.some((member) =>
      memberStates[member.id || member.agent_id]?.status === "running" ||
      (!coordination && !memberStates[member.id || member.agent_id] && activeSpeakers.includes(member.id || member.agent_id)),
    ));
  return (
    <header className="group-chat-header">
      <div className="group-chat-header-main">
        <div className="group-chat-heading">
          <span className="group-chat-room-icon" aria-hidden="true"><IconUsers size={22} stroke={1.7} /></span>
          <div className="group-chat-heading-copy">
            <div className="group-chat-title-line">
              <h2>{group.name}</h2>
              {status && <span className="group-chat-round-status" data-status={coordination?.status || "running"}><i aria-hidden="true" />{status.label}</span>}
            </div>
            <div className="group-chat-room-meta">
              <span>{group.members.length} 位成员</span>
              <span className="group-chat-hop-count">接力 {currentHop}/{maxHops}</span>
              {typeof currentHop === "number" && (
                <progress className="group-chat-hop-progress" aria-label="本轮接力次数" value={Math.min(currentHop, maxHops)} max={maxHops} />
              )}
              {group.description && <span className="group-chat-description" title={group.description}>{group.description}</span>}
            </div>
          </div>
        </div>
        <div className="group-chat-header-actions">
          {live && <Tooltip label={motionPaused ? "播放群聊动效" : "暂停群聊动效"}>
            <ActionIcon className="group-chat-header-motion" variant="subtle" color="gray" onClick={onToggleMotion} aria-label={motionPaused ? "播放群聊动效" : "暂停群聊动效"}>
              {motionPaused ? <IconPlayerPlay size={15} /> : <IconPlayerPause size={15} />}
            </ActionIcon>
          </Tooltip>}
          {actions}
        </div>
      </div>
      <div className="group-chat-members" role="group" aria-label="群聊成员">
        <Tooltip label={allMuted ? "恢复所有成员" : "禁言所有成员"} events={{ hover: true, focus: true, touch: false }}>
          <button
            type="button"
            className="group-member-broadcast"
            data-muted={allMuted || undefined}
            onClick={() => { if (!muteBusy) onToggleMuted(memberIds, !allMuted); }}
            aria-label="所有成员禁言"
            aria-pressed={allMuted ? true : mutedCount > 0 ? "mixed" : false}
            aria-disabled={muteBusy}
          >
            {allMuted ? <IconVolumeOff size={17} aria-hidden="true" /> : <IconUsers size={17} aria-hidden="true" />}
            <span>所有成员 <small>{allMuted ? "全部恢复" : mutedCount > 0 ? `${mutedCount}/${memberIds.length} 已禁言` : "全部禁言"}</small></span>
          </button>
        </Tooltip>
        {group.members.map((member) => {
          const id = member.id || member.agent_id;
          const name = member.alias || member.name || member.agent_id;
          const state = memberStates[id];
          const muted = state?.muted === true;
          const memberStatus = groupMemberStatus(state, coordination);
          const active = !coordination || coordination.status === "running" || coordination.status === "deciding";
          const running = !muted && active && (state?.status === "running" || (!coordination && !state && activeSpeakers.includes(id)));
          const detail = [
            muted ? "点击恢复，禁言期间的消息不会补发" : "点击禁言",
            muted && state?.status === "running" ? "当前任务继续执行，不再接收新消息" : "",
            member.name || agents[member.agent_id]?.name,
            member.workspace,
            state?.last_error || (state?.status === "waiting" && coordination?.status === "error" ? coordination.error : ""),
          ].filter(Boolean).join("\n");
          return (
            <Tooltip key={id} label={detail || name} multiline maw={420} withArrow events={{ hover: true, focus: true, touch: false }} styles={{ tooltip: { whiteSpace: "pre-wrap", overflowWrap: "anywhere" } }}>
              <button
                type="button"
                className="group-member-chip group-member-tone"
                data-tone={groupMemberTone(member, group.members)}
                data-running={running || undefined}
                data-muted={muted || undefined}
                onClick={() => { if (!muteBusy) onToggleMuted([id], !muted); }}
                aria-label={`${name}禁言`}
                aria-pressed={muted}
                aria-disabled={muteBusy}
                aria-description={detail}
              >
                <GroupChatAvatar kind={agents[member.agent_id]?.kind} tone={groupMemberTone(member, group.members)} />
                <span className="group-member-chip-copy">
                  <span className="group-member-name">{name}</span>
                  <span className="group-member-state" data-status={muted ? "muted" : running ? "running" : state?.status || "idle"} data-color={muted ? undefined : memberStatus.color}>
                    {muted ? <IconVolumeOff size={12} aria-hidden="true" /> : <i aria-hidden="true" />}
                    {muted ? "已禁言" : memberStatus.label || (running ? "执行中" : "就绪")}
                  </span>
                </span>
              </button>
            </Tooltip>
          );
        })}
      </div>
      {children}
    </header>
  );
}
