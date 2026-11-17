import { Paper } from "@mantine/core";
import type { Agent, BotGroup, BotGroupMemberState, GroupMessage, Session } from "../../domain/types";
import { scopeKey } from "../../domain/semantics";
import { useAstrorderStore } from "../../state/store";
import { agentKindLabel } from "../../components/AgentBrandIcon";
import { MarkdownContent } from "../../components/MarkdownContent";
import { MessageBody } from "../../components/MessageBody";
import { GroupChatAvatar } from "./GroupChatAvatar";
import { GroupChatImages } from "./GroupChatImages";
import { groupMemberTone, groupMessageTime } from "./groupChatPresentation";
import "./groupChat.css";

export function GroupChatMessage({
  message,
  members,
  memberStates,
  agents,
}: {
  message: GroupMessage;
  members: BotGroup["members"];
  memberStates: Record<string, BotGroupMemberState>;
  agents: Record<string, Agent>;
}) {
  const isUser = message.sender_type === "user";
  const member = message.sender_type === "agent"
    ? message.sender_member_id
      ? members.find((m) => (m.id || m.agent_id) === message.sender_member_id)
      : members.find((m) => m.agent_id === message.sender_id)
    : undefined;
  const agent = agents[message.sender_id];
  const sessionId = member && (memberStates[member.id || member.agent_id]?.session_id || member.fork_session_id);
  const storedSession = useAstrorderStore((state) =>
    member && sessionId ? state.sessions[scopeKey(member.agent_id, sessionId)] : undefined,
  );
  const session: Session | null = storedSession || (member && sessionId ? {
    id: sessionId,
    agent_id: member.agent_id,
    title: member.alias || member.name || member.agent_id,
    workspace: member.workspace || null,
    connection_id: agent?.connection_id || member.machine_id,
    status: "idle",
    updated_at: message.created_at,
  } : null);
  const label = isUser ? "用户 (指挥官)"
    : member?.alias ? `${member.alias} (${member.name || message.sender_id})`
      : !member && message.sender_name ? message.sender_name
        : agent?.name || member?.name || message.sender_id;
  const tone = isUser ? "violet" : groupMemberTone(member, members);
  const timestamp = groupMessageTime(message.created_at);

  return (
    <article className={`group-chat-message group-member-tone${isUser ? " group-chat-message-user" : ""}`} data-tone={tone} aria-label={`${label}的消息`}>
      {!isUser && <GroupChatAvatar kind={agent?.kind} tone={tone} />}
      <div className="group-chat-message-main">
        <div className="group-chat-message-meta">
          <span className="group-chat-message-author">{isUser ? "你" : member?.alias || member?.name || label}</span>
          {!isUser && agent && <span className="group-chat-message-agent">{agentKindLabel(agent.kind)}</span>}
          {message.hop_count > 0 && <span className="group-chat-message-hop">接力 {message.hop_count}</span>}
          {timestamp && <time dateTime={message.created_at} title={new Date(message.created_at).toLocaleString("zh-CN")}>{timestamp}</time>}
        </div>
        <Paper className="group-chat-message-bubble" radius="lg" withBorder>
          {!!message.attachments?.length && <GroupChatImages attachments={message.attachments} />}
          <MessageBody
            value={message.text}
            user={isUser}
            attachmentNames={message.attachments?.map((attachment) => attachment.name)}
            sessionId={session?.id}
            connectionId={session?.connection_id || undefined}
            renderMarkdown={(value) => <MarkdownContent value={value} session={session} />}
          />
        </Paper>
      </div>
      {isUser && <GroupChatAvatar user tone={tone} />}
    </article>
  );
}
