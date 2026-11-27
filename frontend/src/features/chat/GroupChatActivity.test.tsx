import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { Approval, BotGroupMember, BotGroupMemberState, Message } from "../../domain/types";
import { scopeKey } from "../../domain/semantics";
import { useAstrorderStore } from "../../state/store";
import { GroupChatActivity } from "./GroupChatActivity";

const members: BotGroupMember[] = [
  { id: "design", agent_id: "codex", machine_id: "local", alias: "方案设计", workspace: "/design" },
  { id: "dev", agent_id: "codex", machine_id: "local", alias: "开发", workspace: "/dev" },
];
const memberStates: Record<string, BotGroupMemberState> = {
  design: { status: "success", session_id: "design-thread" },
  dev: { status: "running", session_id: "dev-thread", updated_at: "2026-11-12T10:00:00Z" },
};
const props = { members, memberStates, activeSpeakers: ["dev"], agents: {}, approvals: [] };
const thinking: Message = {
  id: "thinking", agent_id: "codex", session_id: "dev-thread", kind: "thinking", role: "assistant",
  text: "检查授权", created_at: "2026-11-12T10:00:00.100Z", command_id: null, attachments: [], tool: null,
};

afterEach(() => useAstrorderStore.setState({ messages: {} }));

describe("group chat activity", () => {
  it("shows independent same-Agent members and never presents deferred recipients as working", () => {
    render(<GroupChatActivity {...props} coordination={{ status: "running" }} />);
    const status = screen.getByRole("status");
    expect(status).toHaveTextContent("开发 正在处理中");
    expect(status).not.toHaveTextContent("方案设计");
    expect(status.querySelectorAll(".group-chat-activity-row")).toHaveLength(1);
    expect(status.querySelector(".group-chat-activity-dots")).toHaveAttribute("aria-hidden", "true");
    fireEvent.click(screen.getByRole("button", { name: "暂停状态动效" }));
    expect(status.parentElement).toHaveAttribute("data-motion-paused", "true");
    fireEvent.click(screen.getByRole("button", { name: "播放状态动效" }));
    expect(status.parentElement).not.toHaveAttribute("data-motion-paused");
  });

  it("tracks actual thinking and tool events from the current delivery only", () => {
    const key = scopeKey("codex", "dev-thread");
    const otherKey = scopeKey("codex", "design-thread");
    useAstrorderStore.setState({ messages: {
      [key]: { old: { ...thinking, id: "old", created_at: "2026-11-12T09:00:00Z" } },
      [otherKey]: { thinking },
    } });
    render(<GroupChatActivity {...props} coordination={{ status: "running" }} />);
    expect(screen.getByRole("status")).toHaveTextContent("开发 正在处理中");
    act(() => useAstrorderStore.setState({ messages: { [key]: { thinking } } }));
    expect(screen.getByRole("status")).toHaveTextContent("开发 正在思考中");
    const tool: Message = { ...thinking, id: "tool", kind: "tool", created_at: "2026-11-12T10:00:01Z", tool: { status: "running" } };
    act(() => useAstrorderStore.setState({ messages: { [key]: { thinking, tool } } }));
    expect(screen.getByRole("status")).toHaveTextContent("开发 正在执行任务");
    act(() => useAstrorderStore.setState({ messages: { [key]: { tool: { ...tool, completed_at: "2026-11-12T10:00:02Z" } } } }));
    expect(screen.getByRole("status")).toHaveTextContent("开发 正在处理中");
  });

  it("shows decision model deciding alongside members who are still processing", () => {
    const { rerender } = render(<GroupChatActivity {...props} coordination={{ status: "deciding" }} />);
    expect(screen.getByRole("status")).toHaveTextContent("决策模型 正在思考下一棒");
    expect(screen.getByRole("status")).toHaveTextContent("开发 正在处理中");
    rerender(<GroupChatActivity {...props} coordination={{ status: "running" }} />);
    expect(screen.getByRole("status")).not.toHaveTextContent("决策模型");
  });

  it.each(["complete", "stopped", "error", "waiting", "idle"] as const)("clears processing hints after %s even with stale speaker fields", (status) => {
    const { rerender } = render(<GroupChatActivity {...props} coordination={{ status: "running" }} />);
    expect(screen.getByRole("status")).toHaveTextContent("开发 正在处理中");
    rerender(<GroupChatActivity {...props} coordination={{ status }} />);
    expect(screen.getByRole("status")).toBeEmptyDOMElement();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("shows authorization only for the matching Agent and session", () => {
    const approvals = [
      { id: "other", state: "pending", agent_id: "codex", session_id: "design-thread" },
      { id: "mine", state: "pending", agent_id: "codex", session_id: "dev-thread" },
    ] as Approval[];
    const { rerender } = render(<GroupChatActivity {...props} approvals={approvals} coordination={{ status: "running" }} />);
    const status = screen.getByRole("status");
    expect(status).toHaveTextContent("开发 等待你的授权");
    expect(status.querySelector(".group-chat-activity-dots")).toBeNull();
    rerender(<GroupChatActivity {...props} approvals={approvals.slice(0, 1)} coordination={{ status: "running" }} />);
    expect(within(status).getByText("开发 正在处理中")).toBeInTheDocument();
  });
});
