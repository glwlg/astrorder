import { beforeEach, expect, it } from "vitest";
import type { BotGroup } from "../domain/types";
import { useAstrorderStore } from "./store";

const group: BotGroup = { id: "g", name: "群", members: [], max_hops: 10, created_at: "", updated_at: "", coordination: { status: "idle" } };
beforeEach(() => useAstrorderStore.getState().resetRuntime());

it("preserves newer group events and deletions over an in-flight snapshot", () => {
  const store = useAstrorderStore.getState();
  store.hydrateBotGroups([group], 0);
  const before = useAstrorderStore.getState().botGroupRevision;
  store.applyEvent({ id: "run", cursor: 1, type: "bot_group.upsert", agent_id: null, session_id: null, data: { ...group, coordination: { status: "running" } } });
  store.hydrateBotGroups([group], before);
  expect(useAstrorderStore.getState().botGroups.g.coordination?.status).toBe("running");
  const pending = useAstrorderStore.getState().botGroupRevision;
  store.applyEvent({ id: "delete", cursor: 2, type: "bot_group.delete", agent_id: null, session_id: null, data: { id: "g" } });
  store.hydrateBotGroups([group], pending);
  expect(useAstrorderStore.getState().botGroups.g).toBeUndefined();
});

it("does not resurrect a group created and deleted during the initial fetch", () => {
  const store = useAstrorderStore.getState();
  store.applyEvent({ id: "create", cursor: 1, type: "bot_group.upsert", agent_id: null, session_id: null, data: { ...group, coordination: { status: "running" } } });
  store.applyEvent({ id: "delete", cursor: 2, type: "bot_group.delete", agent_id: null, session_id: null, data: { id: group.id } });
  store.hydrateBotGroups([group], 0);
  expect(useAstrorderStore.getState().botGroups).toEqual({});
});
