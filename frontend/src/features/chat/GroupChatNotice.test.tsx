import { MantineProvider } from "@mantine/core";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Agent, BotGroupMember, GroupMessage } from "../../domain/types";
import { GroupChatNotice } from "./GroupChatNotice";

const members: BotGroupMember[] = [{ id: "design", agent_id: "ssh-codex-long-id", machine_id: "ssh", name: "Codex", alias: "方案设计" }];
const agents: Record<string, Agent> = {
  "ssh-codex-long-id": { id: "ssh-codex-long-id", name: "远程 Codex", kind: "codex", status: "ready", capabilities: [], limitation: null },
};
const message: GroupMessage = {
  id: "notice", group_id: "group", sender_type: "system", sender_id: "astrorder:blackboard", sender_name: "作战黑板",
  text: "📋 【黑板更新】ssh-codex-long-id 更新了黑板条目「api-review.plan」",
  mentions: [], hop_count: 0, created_at: "2026-11-12T05:00:00Z",
};

describe("group system notices", () => {
  it("uses the member alias and opens the exact blackboard key", () => {
    const open = vi.fn();
    render(<GroupChatNotice message={message} members={members} agents={agents} onOpenBlackboard={open} />);
    const notice = screen.getByRole("article", { name: "黑板更新" });
    expect(notice).toHaveTextContent("方案设计 更新了黑板");
    expect(notice).not.toHaveTextContent("ssh-codex-long-id");
    expect(notice.querySelector("time")).toHaveAttribute("datetime", message.created_at);
    fireEvent.click(screen.getByRole("button", { name: "查看黑板条目 api-review.plan" }));
    expect(open).toHaveBeenCalledWith("api-review.plan");
  });

  it("does not attribute an ambiguous same-Agent update to one of its members", () => {
    const duplicates = [...members, { ...members[0], id: "dev", alias: "开发" }];
    const { rerender } = render(<GroupChatNotice message={message} members={duplicates} agents={agents} />);
    expect(screen.getByRole("article")).toHaveTextContent("远程 Codex 更新了黑板");
    expect(screen.getByRole("article")).not.toHaveTextContent("方案设计");
    rerender(<GroupChatNotice message={{ ...message, sender_member_id: "dev" }} members={duplicates} agents={agents} />);
    expect(screen.getByRole("article")).toHaveTextContent("开发 更新了黑板");
  });

  it("keeps unknown historical actor IDs in the tooltip without inventing a member alias", () => {
    const historical = { ...message, text: "📋 【黑板更新】old-ssh-codex-profile 更新了黑板条目「api-review.plan」" };
    render(<GroupChatNotice message={historical} members={members} agents={agents} />);
    expect(screen.getByRole("article")).toHaveTextContent("群成员 更新了黑板");
    expect(screen.getByRole("article")).not.toHaveTextContent("old-ssh-codex-profile");
    expect(screen.getByText("群成员")).toHaveAttribute("title", "old-ssh-codex-profile");
  });

  it("preserves unrecognized system text without interpreting it as a blackboard event", () => {
    const { container } = render(<MantineProvider><GroupChatNotice message={{ ...message, sender_id: "other", text: "**系统提醒**\n\n原始内容\n\n<script>alert(1)</script>", created_at: "" }} members={members} agents={agents} /></MantineProvider>);
    expect(screen.getByRole("article", { name: "系统通知" })).toHaveTextContent("原始内容");
    expect(screen.getByText("系统提醒").tagName).toBe("STRONG");
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("time")).toBeNull();
  });
});
