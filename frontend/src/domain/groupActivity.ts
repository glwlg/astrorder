import type { BotGroup } from "./types";

export function groupHasActiveTask(group: BotGroup): boolean {
  // Pending delivery alone is a paused handoff, not an executing task.
  return group.coordination?.status === "deciding" ||
    group.coordination?.status === "running" ||
    Object.values(group.member_states || {}).some((member) => member.status === "running");
}
