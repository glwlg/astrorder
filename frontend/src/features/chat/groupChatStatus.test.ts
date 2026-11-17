import { describe, expect, it } from "vitest";
import type { BotGroup, BotGroupMemberState } from "../../domain/types";
import { groupChatStatus, groupMemberStatus } from "./groupChatStatus";

describe("group chat delivery status", () => {
  it.each([
    ["error", "已暂停", "未发送"],
    ["deciding", "判断中", "待判断"],
    ["waiting", "暂缓发送", "暂缓发送"],
    ["running", "执行中", "暂缓发送"],
    ["complete", "本轮结束", ""],
    ["stopped", "已中断", "已取消"],
  ] as const)("keeps group and pending member status consistent for %s", (status, label, memberLabel) => {
    const coordination: BotGroup["coordination"] = { status };
    expect(groupChatStatus(coordination, true)?.label).toBe(label);
    expect(groupMemberStatus({ status: "waiting" }, coordination).label).toBe(memberLabel);
  });

  it.each(["running", "unknown", "failed", "success"] as const)("preserves %s member state when another delivery is paused", (status) => {
    const state: BotGroupMemberState = { status };
    expect(groupMemberStatus(state, { status: "error" })).toEqual(groupMemberStatus(state, { status: "running" }));
  });
});
