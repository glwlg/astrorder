import { describe, expect, it } from "vitest";
import { groupHasActiveTask } from "./groupActivity";
import type { BotGroup } from "./types";

const group: BotGroup = { id: "g", name: "群", members: [], max_hops: 10, created_at: "", updated_at: "" };
describe("group task activity", () => {
  it.each(["deciding", "running"] as const)("includes %s", (status) => {
    expect(groupHasActiveTask({ ...group, coordination: { status } })).toBe(true);
  });
  it.each(["idle", "waiting", "complete", "error", "stopped"] as const)("does not animate %s without a running member", (status) => {
    expect(groupHasActiveTask({ ...group, coordination: { status } })).toBe(false);
  });
  it("keeps activity visible while another member runs after a coordination error", () => {
    expect(groupHasActiveTask({ ...group, coordination: { status: "error" }, member_states: { a: { status: "running" } } })).toBe(true);
    expect(groupHasActiveTask({ ...group, member_states: { a: { status: "waiting" } } })).toBe(false);
  });
});
