import { useContext } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { api } from "../../api/client";
import type { GroupFiles, GroupFileReference } from "../../domain/types";
import { FileSourceContext } from "../sidecar/FileSourceContext";
import { GroupFileSourceScope } from "./GroupFileSourceScope";

const reference: GroupFileReference = {
  id: "skill", group_id: "g", member_id: "dev", member_name: "开发", agent_id: "dev", session_id: "thread",
  workspace: "/project", connection_id: "ssh-dev", path: "/tools/SKILL.md", aliases: [], name: "SKILL.md", content_key: "", created_at: "",
};
const empty: GroupFiles = { items: [], references: [], checking: false };
function Probe() {
  const scope = useContext(FileSourceContext)!;
  return <>
    <output aria-label="文件引用">{scope.files.map((file) => file.path).join(",")}</output>
    <output aria-label="核验状态">{scope.checking ? "核验中" : "就绪"}{scope.error}</output>
    <button onClick={() => void scope.recheck!()}>重新核验</button>
  </>;
}
beforeEach(() => { vi.spyOn(api, "getGroupArtifacts").mockResolvedValue(empty); });
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });

it("loads verified references independently of the delivered artifact catalog", async () => {
  vi.mocked(api.getGroupArtifacts).mockResolvedValue({ ...empty, references: [reference] });
  render(<GroupFileSourceScope groupId="g"><Probe /></GroupFileSourceScope>);
  expect(await screen.findByText("/tools/SKILL.md")).toBeInTheDocument();
});

it("refreshes references on matching artifact events and ignores late responses", async () => {
  let finishOld!: (files: GroupFiles) => void;
  vi.mocked(api.getGroupArtifacts).mockReturnValueOnce(new Promise((resolve) => { finishOld = resolve; }))
    .mockResolvedValueOnce({ ...empty, references: [reference] });
  render(<GroupFileSourceScope groupId="g"><Probe /></GroupFileSourceScope>);
  act(() => window.dispatchEvent(new CustomEvent("astrorder:blackboard-change", { detail: { namespace: "group:other", key: "artifacts" } })));
  expect(api.getGroupArtifacts).toHaveBeenCalledTimes(1);
  act(() => window.dispatchEvent(new CustomEvent("astrorder:blackboard-change", { detail: { namespace: "group:g", key: "artifacts" } })));
  expect(await screen.findByText("/tools/SKILL.md")).toBeInTheDocument();
  await act(async () => finishOld(empty));
  expect(screen.getByLabelText("文件引用")).toHaveTextContent("/tools/SKILL.md");
});

it("never applies an old group's results after switching groups", async () => {
  let finishOld!: (files: GroupFiles) => void;
  vi.mocked(api.getGroupArtifacts).mockReturnValueOnce(new Promise((resolve) => { finishOld = resolve; })).mockResolvedValue(empty);
  const view = render(<GroupFileSourceScope groupId="g"><Probe /></GroupFileSourceScope>);
  view.rerender(<GroupFileSourceScope groupId="other"><Probe /></GroupFileSourceScope>);
  await waitFor(() => expect(api.getGroupArtifacts).toHaveBeenCalledWith("other"));
  await act(async () => finishOld({ ...empty, references: [reference] }));
  expect(screen.getByLabelText("文件引用")).toBeEmptyDOMElement();
});

it("polls only while checking and offers an explicit recheck after failure", async () => {
  vi.useFakeTimers();
  vi.mocked(api.getGroupArtifacts).mockResolvedValueOnce({ ...empty, checking: true }).mockResolvedValue({ ...empty, error: "来源服务器离线" });
  vi.spyOn(api, "recheckGroupArtifacts").mockResolvedValue({ ...empty, references: [reference] });
  render(<GroupFileSourceScope groupId="g"><Probe /></GroupFileSourceScope>);
  await act(async () => {});
  expect(screen.getByLabelText("核验状态")).toHaveTextContent("核验中");
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(screen.getByLabelText("核验状态")).toHaveTextContent("来源服务器离线");
  await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
  expect(api.getGroupArtifacts).toHaveBeenCalledTimes(2);
  await act(async () => fireEvent.click(screen.getByRole("button", { name: "重新核验" })));
  expect(api.recheckGroupArtifacts).toHaveBeenCalledWith("g");
  expect(screen.getByLabelText("文件引用")).toHaveTextContent("/tools/SKILL.md");
});
