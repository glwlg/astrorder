import { useState, type ReactNode } from "react";
import { IconBulb, IconPlayerPause, IconPlayerPlay, IconSparkles } from "@tabler/icons-react";
import type { Agent, Approval, BotGroup, BotGroupMember, BotGroupMemberState, Message } from "../../domain/types";
import { scopeKey } from "../../domain/semantics";
import { useAstrorderStore } from "../../state/store";
import { AgentBrandIcon } from "../../components/AgentBrandIcon";
import { groupMemberTone } from "./groupChatPresentation";
import "./groupChat.css";

type Phase = "working" | "thinking" | "tool" | "approval";

function MemberActivity({
  member,
  state,
  agent,
  approval,
  tone,
}: {
  member: BotGroupMember;
  state?: BotGroupMemberState;
  agent?: Agent;
  approval: boolean;
  tone: string;
}) {
  const phase = useAstrorderStore((store): Phase => {
    if (approval) return "approval";
    // Only use activity from this delivery; old turns must not appear as live work.
    if (!state?.session_id || !state.updated_at) return "working";
    const messages = store.messages[scopeKey(member.agent_id, state.session_id)];
    const startedAt = Date.parse(state.updated_at);
    const latest = Object.values(messages || {}).reduce<Message | undefined>(
      (last, message) => Date.parse(message.created_at) >= startedAt && (!last || Date.parse(message.created_at) >= Date.parse(last.created_at)) ? message : last,
      undefined,
    );
    if (latest?.completed_at) return "working";
    if (latest?.kind === "thinking") return "thinking";
    if (latest?.kind === "tool" && latest.tool?.status === "running") return "tool";
    return "working";
  });
  const name = member.alias || member.name || member.agent_id;
  const copy = {
    working: ["正在处理中", "脑力小马达已启动，稍候带着结果回来。"],
    thinking: ["正在思考中", "灵感正在连线，稍等一下下。"],
    tool: ["正在执行任务", "袖子挽好了，正在动手处理。"],
    approval: ["等待你的授权", "需要你点个头，就能继续啦。"],
  }[phase];
  return <ActivityRow
    name={name}
    title={copy[0]}
    detail={copy[1]}
    working={phase !== "approval"}
    tone={tone}
    icon={agent ? <AgentBrandIcon kind={agent.kind} size={18} /> : <IconSparkles size={18} />}
  />;
}

function ActivityRow({ name, title, detail, icon, working = true, tone = "violet" }: {
  name: string;
  title: string;
  detail: string;
  icon: ReactNode;
  working?: boolean;
  tone?: string;
}) {
  return (
    <div className="group-chat-activity-row group-member-tone" data-tone={tone}>
      <span className="group-chat-activity-avatar" aria-hidden="true">{icon}</span>
      <div className="group-chat-activity-copy">
        <div className="group-chat-activity-title">
          <span>{name} {title}</span>
          {working && <span className="group-chat-activity-dots" aria-hidden="true"><i /><i /><i /></span>}
        </div>
        <div className="group-chat-activity-detail">{detail}</div>
      </div>
    </div>
  );
}

export function GroupChatActivity({ members, memberStates, coordination, activeSpeakers, agents, approvals, motionPaused, onToggleMotion }: {
  members: BotGroup["members"];
  memberStates: Record<string, BotGroupMemberState>;
  coordination: BotGroup["coordination"];
  activeSpeakers: string[];
  agents: Record<string, Agent>;
  approvals: Approval[];
  motionPaused?: boolean;
  onToggleMotion?: () => void;
}) {
  const [localPaused, setLocalPaused] = useState(false);
  const paused = motionPaused ?? localPaused;
  const active = !coordination || coordination.status === "running" || coordination.status === "deciding";
  const runningMembers = active ? members.filter((member) => {
    const id = member.id || member.agent_id;
    return memberStates[id]?.status === "running" || (!coordination && !memberStates[id] && activeSpeakers.includes(id));
  }) : [];
  const running = runningMembers.map((member) => {
    const state = memberStates[member.id || member.agent_id];
    const approval = Boolean(state?.session_id && approvals.some((item) =>
      item.state === "pending" && item.agent_id === member.agent_id && item.session_id === state.session_id,
    ));
    return { member, state, approval };
  });
  const deciding = coordination?.status === "deciding";
  const animating = deciding || running.some((item) => !item.approval);
  return (
    <div className="group-chat-activity" data-motion-paused={paused || undefined}>
      <div role="status" aria-label="群聊处理进度" aria-atomic="true" className="group-chat-activity-rows">
        {deciding && <ActivityRow name="System One" title="正在思考下一棒" detail="接力棒交给谁，正在安排中…" icon={<IconBulb size={18} />} />}
        {running.map(({ member, state, approval }) => {
          const id = member.id || member.agent_id;
          return <MemberActivity key={id} member={member} state={state} agent={agents[member.agent_id]}
            approval={approval}
            tone={groupMemberTone(member, members)}
          />;
        })}
      </div>
      {animating && (
        <button type="button" className="group-chat-motion-toggle" onClick={onToggleMotion || (() => setLocalPaused((value) => !value))} aria-label={paused ? "播放状态动效" : "暂停状态动效"} title={paused ? "播放状态动效" : "暂停状态动效"}>
          {paused ? <IconPlayerPlay size={14} /> : <IconPlayerPause size={14} />}
        </button>
      )}
    </div>
  );
}
