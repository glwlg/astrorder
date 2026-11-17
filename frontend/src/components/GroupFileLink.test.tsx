import { MantineProvider } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { GroupFileReference, Session } from "../domain/types";
import { FileSourceContext } from "../features/sidecar/FileSourceContext";
import { groupSidecarKey, useSidecarStore } from "../features/sidecar/sidecarStore";
import { MarkdownContent } from "./MarkdownContent";
import { GroupBlackboardItem } from "../features/chat/GroupBlackboardItem";
import { GroupFileLink } from "./GroupFileLink";

const file: GroupFileReference = {
  id: "file-bbbbbbbb", group_id: "group-files", member_id: "dev", member_name: "开发",
  agent_id: "agent-dev", session_id: "same-native-session", connection_id: "ssh-dev",
  workspace: "/home/luwei/project", path: "/home/luwei/project/docs/方案.md", aliases: ["docs/方案.md"],
  name: "方案.md", content_key: "artifacts.file-bbbbbbbb", created_at: "",
};
const windows: Session = {
  id: "design-session", agent_id: "agent-design", connection_id: "local",
  workspace: "C:/project", title: "设计", status: "idle", updated_at: "",
};
const load = vi.fn<() => Promise<GroupFileReference[]>>();
function mount(text: string, files = [file]) {
  return render(<MantineProvider env="test"><FileSourceContext value={{ groupId: file.group_id, files, load }}>
    <MarkdownContent value={text} session={windows} />
  </FileSourceContext></MantineProvider>);
}

beforeEach(() => {
  localStorage.clear();
  load.mockReset().mockResolvedValue([file]);
  useSidecarStore.setState({ activeSessionKey: null, tabs: [], activeTabId: "", isOpen: false, dirtyTabs: {}, mountedTabs: {}, sessionMemories: {} });
  useSidecarStore.getState().switchSession(groupSidecarKey(file.group_id));
  vi.spyOn(api, "getWorkspaceRootFiles").mockResolvedValue({ root: "", items: [] });
  vi.spyOn(api, "checkWorkspaceFiles").mockResolvedValue({ existing: [] });
  vi.spyOn(notifications, "show").mockReturnValue("error");
  vi.spyOn(notifications, "hide").mockReturnValue("error");
  vi.spyOn(notifications, "update").mockReturnValue("error");
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); delete (window as unknown as { astrorderDesktop?: unknown }).astrorderDesktop; });

describe("automatic group artifacts", () => {
  it("opens a path forwarded by Windows using the first Linux publisher without a source dialog", async () => {
    mount(`[设计转发](</home/luwei/project/docs/%E6%96%B9%E6%A1%88.md:12>)`);
    fireEvent.click(screen.getByRole("button", { name: /设计转发/ }));
    await waitFor(() => expect(useSidecarStore.getState().tabs).toHaveLength(1));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    const ref = useSidecarStore.getState().tabs[0].artifact!;
    expect(ref).toMatchObject({ agentId: "agent-dev", sessionId: file.session_id, connectionId: "ssh-dev", path: file.path, metadata: { line: 12 } });
    const query = new URL(ref.readUrl, "http://localhost").searchParams;
    expect(query.get("connection_id")).toBe("ssh-dev");
    expect(query.get("path")).toBe(file.path);
  });

  it("treats relative and absolute paths from two Linux members as the first artifact", async () => {
    const otherLinux = { ...windows, workspace: file.workspace, connection_id: "ssh-other" };
    render(<MantineProvider env="test"><FileSourceContext value={{ groupId: file.group_id, files: [file], load }}>
      <MarkdownContent value={`[原件](${file.path}) [转发](docs/方案.md)`} session={otherLinux} />
    </FileSourceContext></MantineProvider>);
    fireEvent.click(screen.getByRole("button", { name: /原件/ }));
    fireEvent.click(screen.getByRole("button", { name: /转发/ }));
    expect(useSidecarStore.getState().tabs).toHaveLength(1);
    expect(useSidecarStore.getState().tabs[0].artifact?.connectionId).toBe("ssh-dev");
  });

  it("links bare paths and inline filenames from the directory without probing the forwarding machine", () => {
    mount(`产物：${file.path}\n\n代码路径：\`docs/方案.md\``);
    const buttons = screen.getAllByRole("button", { name: /方案.md/ });
    expect(buttons).toHaveLength(2);
    fireEvent.click(buttons[0]);
    expect(useSidecarStore.getState().tabs[0].artifact?.connectionId).toBe("ssh-dev");
    expect(api.getWorkspaceRootFiles).not.toHaveBeenCalled();
    expect(api.checkWorkspaceFiles).not.toHaveBeenCalled();
  });

  it("keeps unknown paths and blackboard keys as text without probing the sender's filesystem", () => {
    load.mockResolvedValue([]);
    mount("[未收集](docs/missing.md) `$.data.records[*]` `api-review.opscore.assessment`");
    expect(screen.getByText("未收集")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(load).not.toHaveBeenCalled();
    expect(api.checkWorkspaceFiles).not.toHaveBeenCalled();
    expect(useSidecarStore.getState().tabs).toHaveLength(0);
  });

  it("stages a forwarded file from its original connection in the system application", async () => {
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = {};
    const fetch = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ local_paths: ["C:\\Temp\\方案.md"] }), { status: 200 }))
      .mockResolvedValueOnce(new Response('{"status":"ok"}', { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    mount(`[设计转发](${file.path})`);
    fireEvent.contextMenu(screen.getByRole("button", { name: /设计转发/ }), { clientX: 100, clientY: 120 });
    fireEvent.click(await screen.findByRole("menuitem", { name: "用系统默认应用打开" }));
    await waitFor(() => {
      expect(notifications.update).not.toHaveBeenCalled();
      expect(fetch).toHaveBeenCalledTimes(2);
    });
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ paths: [file.path], session_id: file.session_id, connection_id: "ssh-dev", copy_to_clipboard: false });
  });

  it("does not open a file after navigating away during a directory request", async () => {
    let complete!: (value: GroupFileReference[]) => void;
    load.mockReturnValue(new Promise((resolve) => { complete = resolve; }));
    const view = render(<MantineProvider env="test"><FileSourceContext value={{ groupId: file.group_id, files: [], load }}>
      <GroupFileLink href={file.path}>转发</GroupFileLink>
    </FileSourceContext></MantineProvider>);
    fireEvent.click(screen.getByRole("button", { name: /转发/ }));
    view.unmount();
    useSidecarStore.getState().switchSession("other::session");
    await act(async () => complete([file]));
    expect(useSidecarStore.getState().tabs).toHaveLength(0);
  });

  it("renders the blackboard artifact directory as a file list with its source", () => {
    render(<MantineProvider env="test"><FileSourceContext value={{ groupId: file.group_id, files: [file], load }}>
      <GroupBlackboardItem itemKey="artifacts" itemValue={JSON.stringify({ items: [file] })} namespace={`group:${file.group_id}`} />
    </FileSourceContext></MantineProvider>);
    expect(screen.getByLabelText("群聊产物")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /方案.md/ }));
    expect(useSidecarStore.getState().tabs[0].artifact?.connectionId).toBe("ssh-dev");
    expect(screen.queryByRole("button", { name: /删除/ })).not.toBeInTheDocument();
  });

  it("opens an existing skill reference without placing it in the delivered artifact list", () => {
    const skill = { ...file, id: "skill", path: "/home/luwei/.codex/skills/astrorder-blackboard/SKILL.md", name: "SKILL.md", content_key: "", aliases: [] };
    mount(`[工具说明](${skill.path})`, [skill]);
    fireEvent.click(screen.getByRole("button", { name: /工具说明/ }));
    expect(useSidecarStore.getState().tabs[0].artifact?.path).toBe(skill.path);
  });
});
