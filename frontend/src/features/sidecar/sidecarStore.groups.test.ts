import { beforeEach, expect, it } from "vitest";
import { groupSidecarKey, useSidecarStore } from "./sidecarStore";

beforeEach(() => {
  useSidecarStore.setState({ activeSessionKey: null, tabs: [], activeTabId: "", isOpen: false, dirtyTabs: {}, mountedTabs: {}, sessionMemories: {} });
});

it("isolates each group's pane and reuses its blackboard while advancing focus requests", () => {
  const store = useSidecarStore.getState();
  store.openGroupBlackboard("one", "第一群", "review");
  const first = useSidecarStore.getState().tabs[0].artifact!;
  store.openGroupBlackboard("one", "第一群", "review");
  expect(useSidecarStore.getState().tabs).toHaveLength(1);
  expect(useSidecarStore.getState().tabs[0].artifact?.metadata?.focusRequest).toBe(Number(first.metadata?.focusRequest) + 1);
  store.openGroupBlackboard("two", "第二群");
  expect(useSidecarStore.getState().activeSessionKey).toBe(groupSidecarKey("two"));
  expect(useSidecarStore.getState().tabs[0].artifact?.metadata?.namespace).toBe("group:two");
  store.setIsOpen(false);
  store.switchSession(groupSidecarKey("one"));
  expect(useSidecarStore.getState().isOpen).toBe(true);
  expect(useSidecarStore.getState().tabs[0].artifact?.metadata?.focusKey).toBe("review");
  store.switchSession(groupSidecarKey("two"));
  expect(useSidecarStore.getState().isOpen).toBe(false);
});
