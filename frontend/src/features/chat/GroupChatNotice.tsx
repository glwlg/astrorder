import { IconChalkboard, IconCircleDot } from "@tabler/icons-react";
import type { Agent, BotGroupMember, GroupMessage } from "../../domain/types";
import { MarkdownContent } from "../../components/MarkdownContent";
import { blackboardNotice, groupMessageTime } from "./groupChatPresentation";

export function GroupChatNotice({ message, members, agents, onOpenBlackboard }: {
  message: GroupMessage;
  members: BotGroupMember[];
  agents: Record<string, Agent>;
  onOpenBlackboard?: (key: string) => void;
}) {
  const notice = blackboardNotice(message, members, agents);
  const timestamp = groupMessageTime(message.created_at);
  return (
    <article className="group-chat-notice" aria-label={notice ? "黑板更新" : "系统通知"}>
      <span className="group-chat-notice-icon" aria-hidden="true">{notice ? <IconChalkboard size={16} /> : <IconCircleDot size={16} />}</span>
      <div className="group-chat-notice-copy">
        {notice ? <>
          <span><strong title={notice.actorId}>{notice.actor}</strong> 更新了黑板</span>
          {onOpenBlackboard ? (
            <button type="button" className="group-chat-notice-link" onClick={() => onOpenBlackboard(notice.key)} aria-label={`查看黑板条目 ${notice.key}`}>{notice.key}</button>
          ) : <code className="group-chat-notice-key">{notice.key}</code>}
        </> : <MarkdownContent value={message.text} session={null} />}
      </div>
      {timestamp && <time dateTime={message.created_at} title={new Date(message.created_at).toLocaleString("zh-CN")}>{timestamp}</time>}
    </article>
  );
}
