import type { Agent, BotGroupMember, GroupMessage } from "../../domain/types";

const memberTones = ["violet", "teal", "amber", "blue", "rose", "olive"] as const;

export function groupMemberTone(member: BotGroupMember | undefined, members: BotGroupMember[]): string {
  return member ? memberTones[Math.max(0, members.indexOf(member)) % memberTones.length] : "neutral";
}

export function groupMessageTime(createdAt: string): string | undefined {
  const timestamp = Date.parse(createdAt);
  if (!Number.isFinite(timestamp)) return undefined;
  return new Date(timestamp).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false });
}

export function blackboardNotice(
  message: GroupMessage,
  members: BotGroupMember[],
  agents: Record<string, Agent>,
): { actor: string; actorId: string; key: string } | undefined {
  if (message.sender_type !== "system" || message.sender_id !== "astrorder:blackboard") return undefined;
  const match = /^📋\s*【黑板更新】(.+?) 更新了黑板条目「(.+)」$/s.exec(message.text.trim());
  if (!match) return undefined;
  const [, actorId, key] = match;
  const matchingMembers = message.sender_member_id
    ? members.filter((member) => (member.id || member.agent_id) === message.sender_member_id)
    : members.filter((member) => member.id === actorId || member.agent_id === actorId);
  // Old notices carry an Agent ID only, which cannot distinguish duplicate members.
  const member = matchingMembers.length === 1 ? matchingMembers[0] : undefined;
  return { actor: member?.alias || member?.name || agents[actorId]?.name || "群成员", actorId, key };
}
