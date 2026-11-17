import { MantineProvider } from "@mantine/core";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { api } from "../../api/client";
import type { BotGroup, GroupMessage } from "../../domain/types";
import { GroupsPage } from "./GroupsPage";
import { groupSidecarKey, useSidecarStore } from "../sidecar/sidecarStore";
import { useAstrorderStore } from "../../state/store";
import type { ArtifactRef } from "../../domain/artifact";

vi.mock("@monaco-editor/react", () => ({
  default: ({ value, onChange }: { value: string; onChange: (value: string) => void }) =>
    <textarea aria-label="测试代码编辑器" value={value} onChange={(event) => onChange(event.currentTarget.value)} />,
}));

vi.mock("../monitor/BlackboardJsonRender", () => ({
  BlackboardItemRenderer: ({ itemKey }: { itemKey: string }) => <div>{itemKey} 的内容</div>,
}));
const group: BotGroup = {
  id: "group-focus", name: "方案讨论", members: [{ id: "design", agent_id: "codex", machine_id: "local", alias: "设计" }],
  max_hops: 10, coordination: { status: "complete" }, created_at: "", updated_at: "",
};
const notice: GroupMessage = {
  id: "notice", group_id: group.id, sender_type: "system", sender_id: "astrorder:blackboard",
  text: "📋 【黑板更新】codex 更新了黑板条目「api-review.plan」", mentions: [], hop_count: 0, created_at: "",
};
const scrollDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "scrollIntoView");
const scroll = vi.fn();

beforeEach(() => {
  vi.spyOn(api, "getGroupArtifacts").mockResolvedValue({ items: [], references: [], checking: false });
  localStorage.clear();
  useSidecarStore.setState({ activeSessionKey: null, tabs: [], activeTabId: "", isOpen: false, dirtyTabs: {}, mountedTabs: {}, sessionMemories: {} });
  useAstrorderStore.getState().resetRuntime();
  scroll.mockClear();
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scroll });
});
afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
  if (scrollDescriptor) Object.defineProperty(HTMLElement.prototype, "scrollIntoView", scrollDescriptor);
  else Reflect.deleteProperty(HTMLElement.prototype, "scrollIntoView");
});

describe("group blackboard navigation", () => {
  it("opens the group namespace and focuses the selected entry, including repeated clicks", async () => {
    vi.spyOn(api, "listBotGroups").mockResolvedValue({ items: [group], count: 1 });
    vi.spyOn(api, "getBotGroup").mockResolvedValue({ group });
    vi.spyOn(api, "listGroupMessages").mockResolvedValue({ items: [notice], count: 1 });
    const read = vi.spyOn(api, "getBlackboard").mockResolvedValue({ namespace: `group:${group.id}`, items: { other: {}, "api-review.plan": { version: 3 } }, count: 2 });
    render(<MantineProvider env="test"><MemoryRouter initialEntries={[`/groups/${group.id}`]}><Routes><Route path="/groups/:groupId" element={<GroupsPage />} /></Routes></MemoryRouter></MantineProvider>);
    fireEvent.click(await screen.findByRole("button", { name: "查看黑板条目 api-review.plan" }));
    const entry = await screen.findByLabelText("黑板条目 api-review.plan");
    await waitFor(() => expect(entry).toHaveFocus());
    expect(entry).toHaveAttribute("data-focused", "true");
    expect(read).toHaveBeenCalledWith(`group:${group.id}`);
    expect(scroll).toHaveBeenCalledWith({ block: "nearest", behavior: "instant" });
    const calls = scroll.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "查看黑板条目 api-review.plan" }));
    await waitFor(() => expect(scroll).toHaveBeenCalledTimes(calls + 1));
    expect(localStorage.getItem(`astrorder:group:blackboard:${group.id}`)).toBe("open");
  });

  it("opens member documents in the group pane with exact file scope and preserves the single-chat pane", async () => {
    const members: BotGroup = {
      ...group,
      members: [
        { id: "design", agent_id: "codex", machine_id: "ssh:work", alias: "设计", workspace: "/design" },
        { id: "dev", agent_id: "codex", machine_id: "ssh:work", alias: "开发", workspace: "/dev" },
      ],
      member_states: { design: { status: "success", session_id: "design-thread" }, dev: { status: "success", session_id: "dev-thread" } },
    };
    const solo: ArtifactRef = {
      id: "solo-file", sessionId: "solo", agentId: "codex", name: "单聊文件.md", kind: "workspace_file",
      readUrl: "/solo", path: "/solo/README.md", mediaType: "text/markdown", writable: true,
    };
    const sidecar = useSidecarStore.getState();
    sidecar.switchSession("codex:solo");
    sidecar.openArtifact(solo, "monaco-viewer");
    sidecar.setTabDirty(solo.id, true);
    vi.spyOn(api, "listBotGroups").mockResolvedValue({ items: [members], count: 1 });
    vi.spyOn(api, "getBotGroup").mockResolvedValue({ group: members });
    vi.spyOn(api, "listGroupMessages").mockResolvedValue({ items: [
      { ...notice, id: "dev-file", sender_type: "agent", sender_id: "codex", sender_member_id: "dev", text: "[开发方案](plans/technical.md)" },
      { ...notice, id: "design-file", sender_type: "agent", sender_id: "codex", sender_member_id: "design", text: "[设计方案](/design/plans/technical.md)" },
    ], count: 2 });
    vi.spyOn(api, "getWorkspaceRootFiles").mockResolvedValue({ root: "", items: [] });
    vi.spyOn(api, "checkWorkspaceFiles").mockResolvedValue({ existing: [] });
    vi.spyOn(api, "getBlackboard").mockResolvedValue({ namespace: `group:${group.id}`, items: {}, count: 0 });
    const reads: URL[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = new URL(String(input), "http://localhost");
      reads.push(url);
      return new Response(url.searchParams.get("session_id") === "dev-thread" ? "# 开发文档\n\n[后续说明](next.md)" : "# 设计文档", { status: 200 });
    });
    const save = vi.spyOn(api, "writeFile").mockResolvedValue({ status: "ok", path: "/dev/plans/technical.md" });
    vi.mocked(api.getGroupArtifacts).mockResolvedValue({ items: [], checking: false, references: members.members.map((member) => ({
        id: member.id === "dev" ? "file-bbbb" : "file-aaaa", group_id: group.id, member_id: member.id!, member_name: member.alias!,
        agent_id: member.agent_id, session_id: member.id === "dev" ? "dev-thread" : "design-thread",
        connection_id: "ssh:work", workspace: member.workspace!, path: `${member.workspace}/plans/technical.md`,
        aliases: member.id === "dev" ? ["plans/technical.md"] : [], content_key: `artifacts.file-${member.id}`,
        name: "technical.md", created_at: "",
      })) });
    render(<MantineProvider env="test"><MemoryRouter initialEntries={[`/groups/${members.id}`]}><Routes><Route path="/groups/:groupId" element={<GroupsPage />} /></Routes></MemoryRouter></MantineProvider>);
    fireEvent.click(await screen.findByRole("button", { name: /开发方案/ }));
    await waitFor(() => expect(useSidecarStore.getState().isOpen).toBe(true));
    expect(useSidecarStore.getState().activeSessionKey).toBe(groupSidecarKey(group.id));
    expect(useSidecarStore.getState().isOpen).toBe(true);
    const pane = await screen.findByRole("complementary", { name: "群聊工作台" });
    expect(await within(pane).findByRole("heading", { name: "开发文档" })).toBeInTheDocument();
    expect(reads[0].searchParams.get("path")).toBe("/dev/plans/technical.md");
    expect(reads[0].searchParams.get("session_id")).toBe("dev-thread");
    expect(reads[0].searchParams.get("connection_id")).toBe("ssh:work");
    expect(useSidecarStore.getState().activeSessionKey).toBe(groupSidecarKey(group.id));
    expect(useSidecarStore.getState().sessionMemories["codex:solo"].tabs.map((tab) => tab.id)).toEqual(["solo-file"]);
    expect(useSidecarStore.getState().dirtyTabs[solo.id]).toBe(true);
    fireEvent.click(within(pane).getByRole("button", { name: "编辑 Markdown" }));
    fireEvent.change(within(pane).getByLabelText("测试代码编辑器"), { target: { value: "# 修改后的开发文档" } });
    fireEvent.keyDown(window, { key: "s", ctrlKey: true });
    await waitFor(() => expect(save).toHaveBeenCalledWith({
      path: "/dev/plans/technical.md", content: "# 修改后的开发文档", session_id: "dev-thread", connection_id: "ssh:work",
    }));
    fireEvent.click(screen.getByRole("button", { name: /设计方案/ }));
    expect(await within(pane).findByRole("heading", { name: "设计文档" })).toBeInTheDocument();
    expect(reads.at(-1)?.searchParams.get("path")).toBe("/design/plans/technical.md");
    expect(reads.at(-1)?.searchParams.get("session_id")).toBe("design-thread");
    fireEvent.click(screen.getByRole("button", { name: "黑板" }));
    expect(await screen.findByText(`群聊黑板 · ${members.name}`)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /开发方案/ }));
    expect(within(pane).getByLabelText("测试代码编辑器")).toBeVisible();
    expect(within(pane).getByLabelText("测试代码编辑器")).toHaveValue("# 修改后的开发文档");
    fireEvent.click(screen.getByRole("button", { name: "收起群聊工作台" }));
    expect(pane).not.toBeVisible();
    act(() => sidecar.switchSession("codex:solo"));
    expect(useSidecarStore.getState().tabs.map((tab) => tab.id)).toEqual(["solo-file"]);
    expect(useSidecarStore.getState().activeTabId).toBe("solo-file");
    act(() => sidecar.switchSession(groupSidecarKey(group.id)));
    expect(useSidecarStore.getState().tabs).toHaveLength(3);
  });
});
