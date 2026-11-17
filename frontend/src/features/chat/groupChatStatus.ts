import type { BotGroup, BotGroupMemberState } from "../../domain/types";

type Status = { label: string; color: string };

export function groupChatStatus(
  coordination: BotGroup["coordination"],
  legacyActive: boolean,
): Status | undefined {
  if (!coordination) {
    return legacyActive ? { label: "接力流转中", color: "teal" } : undefined;
  }
  switch (coordination.status) {
    case "error": return { label: "已暂停", color: "red" };
    case "deciding": return { label: "判断中", color: "blue" };
    case "running": return { label: "执行中", color: "teal" };
    case "waiting": return { label: "暂缓发送", color: "yellow" };
    case "complete": return { label: "本轮结束", color: "teal" };
    case "stopped": return { label: "已中断", color: "gray" };
    default: return undefined;
  }
}

export function groupMemberStatus(
  state: BotGroupMemberState | undefined,
  coordination: BotGroup["coordination"],
): Status {
  switch (state?.status) {
    case "failed": return { label: "失败", color: "red" };
    case "success": return { label: "完成", color: "teal" };
    case "running": return { label: "执行中", color: "blue" };
    case "unknown": return { label: "待确认", color: "yellow" };
    case "cancelled": return { label: "已取消", color: "gray" };
    case "waiting":
      if (coordination?.status === "error") return { label: "未发送", color: "red" };
      if (coordination?.status === "deciding") return { label: "待判断", color: "blue" };
      if (coordination?.status === "waiting" || coordination?.status === "running") {
        return { label: "暂缓发送", color: "yellow" };
      }
      if (coordination?.status === "stopped") return { label: "已取消", color: "gray" };
      if (coordination?.status === "complete" || coordination?.status === "idle") {
        return { label: "", color: "gray" };
      }
      return { label: "等待中", color: "yellow" };
    default: return { label: "", color: "gray" };
  }
}
