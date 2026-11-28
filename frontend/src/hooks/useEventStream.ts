import { useEffect, useRef } from "react";
import { notifications as mantineNotifications } from "@mantine/notifications";
import type { QueryClient } from "@tanstack/react-query";
import { connectEventStream, type EventStreamResumeReason, type EventStreamStatus } from "../api/eventStream";
import {
  deliverBrowserNotification,
  notificationForEvent,
  notificationPath,
  restorePushSubscription,
} from "../domain/notifications";
import {
  hasIslandNotificationHost,
  pushIslandNotification,
} from "../domain/islandNotifications";
import { useAstrorderStore } from "../state/store";
import { useExtensionStore } from "../features/extensions/extensionStore";
import { extensionsApi } from "../api/extensions";
import { pluginEventBus } from "../features/extensions/pluginEventBus";
import { usePluginSettingsStore } from "../features/plugins/pluginSettingsStore";
import { api } from "../api/client";
import { nativeActivityEvent } from "./useSessionOrder";
import { scopeKey } from "../domain/semantics";
import type { EventEnvelope } from "../domain/types";
import { clientPerf } from "../perf/clientPerf";

function isRawMessageDelta(event: EventEnvelope): boolean {
  if (event.cursor !== 0) return false;
  const frame = (event.data.frame || event.data) as Record<string, unknown>;
  if (event.type === "codex.notification") {
    return ["item/agentMessage/delta", "item/reasoning/delta", "item/plan/delta"].includes(String(frame.method));
  }
  if (event.type === "hermes.stream") {
    return typeof (frame.delta ?? event.data.delta) === "string";
  }
  if (event.type === "grok.notification") {
    const params = frame.params as { update?: { sessionUpdate?: string } } | undefined;
    return ["agent_message_chunk", "agent_thought_chunk", "user_message_chunk"].includes(String(params?.update?.sessionUpdate));
  }
  return false;
}

function storeStatus(status: EventStreamStatus) {
  const connection =
    status === "connected"
      ? "connected"
      : status === "error"
        ? "error"
        : status;
  useAstrorderStore.getState().setConnection(connection);
}

export function useEventStream(
  authenticated: boolean,
  queryClient: QueryClient,
  navigate?: (to: string) => void,
): void {
  const navigateRef = useRef(navigate);
  navigateRef.current = navigate;
  useEffect(() => {
    if (!authenticated) return;
    void restorePushSubscription().catch(error => mantineNotifications.show({
      id: "push-registration-error", color: "red",
      message: `后台通知连接失败：${error instanceof Error ? error.message : '请重新启用通知'}`,
    }));
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let lastPresenceAt = 0;
    const schedulePresence = (delayMs: number) => {
      if (stopped) return;
      if (timer !== undefined) clearTimeout(timer);
      timer = setTimeout(() => {
        timer = undefined;
        void poll();
      }, delayMs);
    };
    const poll = async () => {
      if (stopped) return;
      if (typeof document !== "undefined" && document.visibilityState === "hidden") {
        return;
      }
      lastPresenceAt = Date.now();
      try {
        const result = await api.getPresence();
        if (!stopped)
          window.dispatchEvent(
            new CustomEvent(nativeActivityEvent, { detail: result }),
          );
      } catch {
        /* A temporarily unavailable source must not discard its last activity. */
      }
      if (!stopped) schedulePresence(30_000);
    };
    const onVisibilityChange = () => {
      if (stopped) return;
      if (document.visibilityState === "visible" && timer === undefined) {
        const elapsed = Date.now() - lastPresenceAt;
        schedulePresence(elapsed >= 10_000 ? 500 : 10_000 - elapsed);
      }
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    schedulePresence(2000);
    return () => {
      stopped = true;
      if (timer !== undefined) clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [authenticated]);
  useEffect(() => {
    if (!authenticated) {
      useAstrorderStore.getState().setConnection("disconnected");
      return undefined;
    }

    let disposed = false;
    let groupRefresh: Promise<void> | undefined;
    let groupRefreshPending = false;
    const refreshGroups = (): Promise<void> => {
      if (groupRefresh) {
        groupRefreshPending = true;
        return groupRefresh;
      }
      const before = useAstrorderStore.getState().botGroupRevision;
      groupRefresh = api.listBotGroups().then((result) => {
        if (!disposed) useAstrorderStore.getState().hydrateBotGroups(result.items, before);
      }).catch(() => {
        // The stream reconnect or explicit resync requests another snapshot.
      }).finally(() => {
        groupRefresh = undefined;
        if (groupRefreshPending && !disposed) {
          groupRefreshPending = false;
          void refreshGroups();
        }
      });
      return groupRefresh;
    };
    void refreshGroups();
    const hookRefreshes = new Map<string, {
      agent: string; session: string; dirty: boolean; running: boolean;
      timer?: ReturnType<typeof setTimeout>;
    }>();
    const createLeadingThrottler = (cooldownMs: number) => {
      const state = new Map<string, { lastAt: number; timer?: ReturnType<typeof setTimeout>; run?: () => void }>();
      return {
        trigger(key: string, fn: () => void) {
          const now = Date.now();
          let entry = state.get(key);
          if (!entry) {
            entry = { lastAt: 0 };
            state.set(key, entry);
          }
          const elapsed = now - entry.lastAt;
          if (entry.lastAt === 0 || elapsed >= cooldownMs) {
            if (entry.timer) {
              clearTimeout(entry.timer);
              entry.timer = undefined;
            }
            entry.run = undefined;
            entry.lastAt = now;
            fn();
            return;
          }
          entry.run = fn;
          if (!entry.timer) {
            entry.timer = setTimeout(() => {
              entry!.timer = undefined;
              if (disposed || !entry!.run) return;
              const pending = entry!.run;
              entry!.run = undefined;
              entry!.lastAt = Date.now();
              pending();
            }, cooldownMs - elapsed);
          }
        },
        clear() {
          for (const entry of state.values()) {
            if (entry.timer) clearTimeout(entry.timer);
          }
          state.clear();
        },
      };
    };
    const messageInvalidator = createLeadingThrottler(1500);
    const usageInvalidator = createLeadingThrottler(2500);
    const swarmInvalidator = createLeadingThrottler(3000);
    const invalidateSessionMessages = (agentId: string | null | undefined, sessionId: string | null | undefined) => {
      if (!agentId || !sessionId) return;
      const key = scopeKey(agentId, sessionId);
      const pendingHook = hookRefreshes.get(key);
      if (pendingHook?.timer) {
        clearTimeout(pendingHook.timer);
        pendingHook.timer = undefined;
        pendingHook.dirty = false;
      }
      messageInvalidator.trigger(key, () => {
        void queryClient.invalidateQueries({
          queryKey: ["astrorder", "messages", agentId, sessionId],
        });
      });
    };
    const invalidateSessionUsage = (sessionId: string | null | undefined, agentId: string | null | undefined) => {
      if (!sessionId || !agentId) return;
      usageInvalidator.trigger(scopeKey(agentId, sessionId), () => {
        void queryClient.invalidateQueries({
          queryKey: ["astrorder", "session_usage", sessionId, agentId],
        });
      });
    };
    const invalidateSessionSwarm = (agentId: string | null | undefined, sessionId: string | null | undefined) => {
      if (!agentId || !sessionId) return;
      swarmInvalidator.trigger(scopeKey(agentId, sessionId), () => {
        void queryClient.invalidateQueries({
          queryKey: ["astrorder", "session-swarm", agentId, sessionId],
        });
        void queryClient.invalidateQueries({
          queryKey: ["astrorder", "session-swarm-member", agentId, sessionId],
        });
      });
    };
    const scheduleHookRefresh = (agent: string, session: string) => {
      const key = scopeKey(agent, session);
      let refresh = hookRefreshes.get(key);
      if (!refresh) {
        refresh = { agent, session, dirty: false, running: false };
        hookRefreshes.set(key, refresh);
      }
      refresh.dirty = true;
      if (refresh.running || refresh.timer) return;
      const current = refresh;
      current.timer = setTimeout(async () => {
        current.timer = undefined;
        current.dirty = false;
        current.running = true;
        try {
          // Let an existing native read finish instead of restarting it on
          // every tool hook. Activity during that read schedules one follow-up.
          await queryClient.invalidateQueries({
            queryKey: ["astrorder", "messages", current.agent, current.session],
          }, { cancelRefetch: false });
        } catch {
          // A later hook or reconnect can retry an unavailable native source.
        } finally {
          current.running = false;
          if (!disposed && current.dirty) scheduleHookRefresh(current.agent, current.session);
          else hookRefreshes.delete(key);
        }
      }, 250);
    };
    let pendingDeltas: EventEnvelope[] = [];
    let deltaTimer: ReturnType<typeof setTimeout> | undefined;
    const flushDeltas = () => {
      clearTimeout(deltaTimer);
      deltaTimer = undefined;
      if (!pendingDeltas.length) return;
      const events = pendingDeltas;
      pendingDeltas = [];
      const store = useAstrorderStore.getState();
      const knownTurns = new Map<string, Set<unknown>>();
      for (const event of events) {
        if (event.agent_id && event.session_id && event.data.turn_id) {
          const key = scopeKey(event.agent_id, event.session_id);
          let turns = knownTurns.get(key);
          if (!turns) {
            turns = new Set(Object.values(store.messages[key] || {}).map(row => row.turn_id));
            knownTurns.set(key, turns);
          }
          if (!turns.has(event.data.turn_id)) {
            scheduleHookRefresh(event.agent_id, event.session_id);
            turns.add(event.data.turn_id);
          }
        }
      }
      const applyStart = typeof performance !== "undefined" ? performance.now() : Date.now();
      store.applyMessageDeltas(events);
      const applyEnd = typeof performance !== "undefined" ? performance.now() : Date.now();
      clientPerf.recordWsDeltaFlush(events.length, Math.max(0, applyEnd - applyStart));
    };
    let hiddenAt = typeof document !== "undefined" && document.visibilityState === "hidden" ? Date.now() : 0;
    let lastFullResumeAt = 0;
    let lastWakeResumeAt = 0;
    let lastResyncInvalidatedAt = 0;
    const onDocVisibilityChange = () => {
      if (typeof document !== "undefined" && document.visibilityState === "hidden") {
        hiddenAt = Date.now();
      }
    };
    if (typeof document !== "undefined") {
      document.addEventListener("visibilitychange", onDocVisibilityChange);
    }
    const stop = connectEventStream({
      after: useAstrorderStore.getState().cursor,
      onStatus: status => {
        if (status !== "connected") flushDeltas();
        clientPerf.recordWsStatus(status);
        storeStatus(status);
      },
      onResume: (reason: EventStreamResumeReason = "reconnect") => {
        const now = Date.now();
        if (reason === "wake") {
          const hiddenDuration = hiddenAt > 0 ? now - hiddenAt : 0;
          hiddenAt = 0;
          // Socket stayed open continuously: ignore brief tab/app switches and overlapping online+visibility wakes.
          if (hiddenDuration < 15_000 || (lastWakeResumeAt > 0 && now - lastWakeResumeAt < 20_000)) {
            return;
          }
          lastWakeResumeAt = now;
          void queryClient.invalidateQueries(
            { queryKey: ["astrorder", "messages"], refetchType: "active" },
            { cancelRefetch: false },
          );
          void queryClient.invalidateQueries(
            { queryKey: ["astrorder", "tasks"], refetchType: "active" },
            { cancelRefetch: false },
          );
          return;
        }
        hiddenAt = 0;
        if (lastFullResumeAt > 0 && now - lastFullResumeAt < 3_000) {
          return;
        }
        lastFullResumeAt = now;
        lastWakeResumeAt = now;
        void refreshGroups();
        for (const resource of ["bootstrap", "messages", "commands", "tasks", "session_usage", "session-swarm", "session-schedules", "checkpoints", "learning"]) {
          void queryClient.invalidateQueries({ queryKey: ["astrorder", resource] });
        }
      },
      onEvent: (event) => {
        pluginEventBus.emitHostEvent(event as unknown as import("../features/extensions/pluginEventBus").HostStreamEvent);
        if (event.type === "message.delta" && event.cursor === 0) {
          const store = useAstrorderStore.getState();
          const { id, agent_id: agentId, session_id: sessionId } = event.data;
          if (typeof id === "string" && typeof agentId === "string" && typeof sessionId === "string"
            && !store.messageEventRevisions[scopeKey(agentId, sessionId)]?.[id]) {
            if (event.agent_id && event.session_id && event.data.turn_id) {
              const rows = Object.values(store.messages[scopeKey(event.agent_id, event.session_id)] || {});
              if (!rows.some(row => row.turn_id === event.data.turn_id)) {
                scheduleHookRefresh(event.agent_id, event.session_id);
              }
            }
            const t0 = typeof performance !== "undefined" ? performance.now() : Date.now();
            store.applyEvent(event);
            const t1 = typeof performance !== "undefined" ? performance.now() : Date.now();
            clientPerf.recordWsEvent(event.type, 1, Math.max(0, t1 - t0));
            return;
          }
          pendingDeltas.push(event);
          if (pendingDeltas.length >= 512) flushDeltas();
          else if (!deltaTimer) deltaTimer = setTimeout(flushDeltas, 16);
          return;
        }
        if (isRawMessageDelta(event)) return;
        flushDeltas();
        const store = useAstrorderStore.getState();
        const notification =
          event.cursor > store.cursor ? notificationForEvent(event) : null;
        if (notification && store.addNotification(notification)) {
          if (notification.kind === "task_completed" || notification.kind === "task_failed") {
            invalidateSessionMessages(notification.agent_id, notification.session_id);
          }
          if (hasIslandNotificationHost()) {
            pushIslandNotification(notification);
          } else {
            mantineNotifications.show({
              id: notification.key,
              title: notification.title,
              message: `${notification.message}（点击跳转）`,
              color:
                notification.kind === "task_failed"
                  ? "red"
                  : notification.kind === "approval_pending"
                    ? "yellow"
                    : "teal",
              autoClose: 10000,
              style: { cursor: "pointer" },
              onClick: () => {
                if (notification.session_id && notification.agent_id) {
                  const isMobile =
                    typeof window !== "undefined" &&
                    (window.location.pathname.startsWith("/mobile") ||
                      window.innerWidth < 768 ||
                      window.matchMedia?.("(max-width: 767px)").matches);
                  const targetPath = notificationPath(notification, isMobile);
                  const nav = navigateRef.current;
                  if (nav) {
                    nav(targetPath);
                  } else if (typeof window !== "undefined") {
                    window.history.pushState({}, "", targetPath);
                    window.dispatchEvent(new PopStateEvent("popstate"));
                  }
                  mantineNotifications.hide(notification.key);
                }
              },
            });
          }
          void deliverBrowserNotification(notification);
        }
        const previousSessionStatus = event.agent_id && event.session_id
          ? store.sessions[scopeKey(event.agent_id, event.session_id)]?.status
          : undefined;
        if ((event.type === "message.upsert" || event.type === "message.delta") && event.agent_id && event.session_id && event.data.turn_id) {
          const rows = Object.values(store.messages[scopeKey(event.agent_id, event.session_id)] || {});
          if (!rows.some(row => row.turn_id === event.data.turn_id)) {
            scheduleHookRefresh(event.agent_id, event.session_id);
          }
        }
        const applyT0 = typeof performance !== "undefined" ? performance.now() : Date.now();
        store.applyEvent(event);
        const applyT1 = typeof performance !== "undefined" ? performance.now() : Date.now();
        clientPerf.recordWsEvent(event.type, 1, Math.max(0, applyT1 - applyT0));
        if (event.type === "agent.connection_choice" && event.cursor > store.cursor) {
          void queryClient.invalidateQueries({ queryKey: ["astrorder", "bootstrap"] });
        }
        if (event.type === "resync_required") void refreshGroups();
        const nativeFrame = event.data.frame as { method?: string } | undefined;
        if (
          (event.type === "codex.notification" && nativeFrame?.method === "turn/completed") ||
          (event.type === "session.upsert" && previousSessionStatus === "running" &&
            ["idle", "error"].includes(String(event.data.status)))
        ) {
          invalidateSessionMessages(event.agent_id, event.session_id);
          invalidateSessionUsage(event.session_id, event.agent_id);
        }
        if (event.type === "transcript.invalidate" && event.agent_id && event.session_id) {
          scheduleHookRefresh(event.agent_id, event.session_id);
        }
        if (event.type === "token.usage") {
          invalidateSessionUsage(event.session_id, event.agent_id);
        }
        if (
          event.type === "session_schedule.upsert" ||
          event.type === "session_schedule.delete" ||
          event.type === "session.delete"
        ) {
          void queryClient.invalidateQueries({
            queryKey: ["astrorder", "session-schedules"],
          });
        }
        if (
          event.type === "learning.scan.started" ||
          event.type === "learning.scan.completed" ||
          event.type === "learning.scan.timeout" ||
          event.type === "learning.scan.failed" ||
          event.type === "learning.topic.updated" ||
          event.type === "learning.reviews.due"
        ) {
          void queryClient.invalidateQueries({
            queryKey: ["astrorder", "learning"],
          });
          if (
            event.type === "learning.scan.completed" &&
            event.cursor > store.cursor &&
            event.data.notify_dynamic_island !== false
          ) {
            const added = Number(event.data.new_topics_count || 0);
            const merged = Number(event.data.merged_topics_count || 0);
            if (added + merged > 0) {
              const title = "成长雷达已完成盲区扫描";
              const message =
                typeof event.data.summary === "string" && event.data.summary.trim()
                  ? event.data.summary.trim()
                  : `新增 ${added} 条、更新 ${merged} 条待学知识卡片`;
              const notice = {
                cursor: event.cursor,
                key: `learning-scan:${String(event.data.run_id || event.id)}`,
                kind: "task_completed" as const,
                title,
                message,
                agent_id: "learning",
                session_id: String(event.data.run_id || "radar"),
                target_path: "/learning",
                created_at: new Date().toISOString(),
              };
              if (hasIslandNotificationHost()) {
                pushIslandNotification(notice);
              } else {
                mantineNotifications.show({
                  id: notice.key,
                  title: notice.title,
                  message: `${notice.message}（点击查看）`,
                  color: "blue",
                  autoClose: 8000,
                  style: { cursor: "pointer" },
                  onClick: () => {
                    const isMobile =
                      typeof window !== "undefined" &&
                      (window.location.pathname.startsWith("/mobile") ||
                        window.innerWidth < 768 ||
                        window.matchMedia?.("(max-width: 767px)").matches);
                    const targetPath = notificationPath(notice, isMobile);
                    navigateRef.current?.(targetPath);
                    mantineNotifications.hide(notice.key);
                  },
                });
              }
            }
          }
          if (event.type === "learning.reviews.due" && event.cursor > store.cursor) {
            const dueCount = Number(event.data.due_count || 0);
            if (dueCount > 0) {
              const notice = {
                cursor: event.cursor,
                key: `learning-due:${new Date().toISOString().slice(0, 10)}`,
                kind: "task_completed" as const,
                title: "成长雷达 · 间隔复习提醒",
                message: `有 ${dueCount} 条知识卡片已到期，趁热巩固记忆`,
                agent_id: "learning",
                session_id: "review",
                target_path: "/learning?tab=review",
                created_at: new Date().toISOString(),
              };
              if (hasIslandNotificationHost()) {
                pushIslandNotification(notice);
              }
            }
          }
        }
        if (event.type === "git.worktrees.changed" || event.type === "git.branch.changed") {
          void queryClient.invalidateQueries({ queryKey: ["astrorder", "git-worktrees"] });
          void queryClient.invalidateQueries({ queryKey: ["astrorder", "git-status"] });
        }
        if (event.type === "checkpoints.updated") {
          const aid = String(event.data.agent_id || event.agent_id || "");
          const sid = String(event.data.session_id || event.session_id || "");
          if (aid && sid) {
            void queryClient.invalidateQueries({
              queryKey: ["astrorder", "checkpoints", aid, sid],
            });
          } else {
            void queryClient.invalidateQueries({
              queryKey: ["astrorder", "checkpoints"],
            });
          }
          if (event.data.action === "restore") {
            void queryClient.invalidateQueries({ queryKey: ["astrorder", "git"] });
          }
        }
        if (event.type === "task.upsert" && event.agent_id && event.session_id) {
          const progress = event.data.progress as Record<string, unknown> | undefined;
          const tool = typeof progress?.tool === "string" ? progress.tool : "";
          if (progress?.source === "antigravity" && ["swarm", "invoke_subagent", "send_message"].includes(tool)) {
            invalidateSessionSwarm(event.agent_id, event.session_id);
          }
        }
        if (
          event.type === "command.upsert" &&
          ["completed", "failed", "cancelled"].includes(
            String(event.data.state),
          )
        ) {
          invalidateSessionMessages(event.agent_id, event.session_id);
          invalidateSessionUsage(event.session_id, event.agent_id);
        }
        if (event.type === "native.observation") {
          void queryClient.invalidateQueries({
            queryKey: ["astrorder", "observations", event.agent_id],
          });
          if (
            event.data.event === "UserPromptSubmit" &&
            typeof event.data.observed_at === "number"
          ) {
            window.dispatchEvent(
              new CustomEvent(nativeActivityEvent, {
                detail: [
                  {
                    agent_id: event.agent_id,
                    id: event.session_id,
                    last_user_at: new Date(
                      event.data.observed_at * 1000,
                    ).toISOString(),
                  },
                ],
              }),
            );
          }
          if (event.data.event === "Stop")
            invalidateSessionMessages(event.agent_id, event.session_id);
        }
        if (event.type === "blackboard.change") {
          window.dispatchEvent(
            new CustomEvent("astrorder:blackboard-change", {
              detail: event.data,
            }),
          );
        }
        if (event.type === "browser.mirror.updated") {
          window.dispatchEvent(new CustomEvent("astrorder:browser-mirror-updated", { detail: event.data }));
        }
        if (event.type === "sidecar.plugin.control" && event.data) {
          const sidecar = useSidecarStore.getState();
          const payload = event.data as Record<string, unknown>;
          const action = payload.action;
          if (action === "open") {
            const pid = String(payload.plugin_id || "");
            const sid = String(payload.session_id || "current");
            const aid = String(payload.agent_id || "");
            const titleStr =
              typeof payload.title === "string" ? payload.title : undefined;
            const pathStr =
              typeof payload.path === "string" ? payload.path : undefined;
            const urlStr =
              typeof payload.url === "string" ? payload.url : undefined;
            if (pid === "terminal") {
              sidecar.openTerminal(sid, aid, titleStr);
            } else if (pid === "browser") {
              sidecar.openBrowser(sid, aid, urlStr);
            } else if (pid === "gitdiff") {
              sidecar.openGitDiff(sid, aid, pathStr);
            } else if (pid === "filetree") {
              sidecar.openFileTree(sid, aid, pathStr);
            } else if (pid === "sidechat") {
              sidecar.openSideChat(sid, aid, titleStr);
            } else if (pid === "agentgraph") {
              sidecar.openAgentGraph(sid, aid, titleStr);
            } else if (pid === "blackboard") {
              sidecar.openBlackboard(sid, aid);
            } else {
              // Artifact viewers like drawio, mermaid, excalidraw, diff, three, html, monaco
              const filePath = pathStr || titleStr || pid;
              const name = filePath.split(/[\\/]/).pop() || filePath;
              let targetViewer = `${pid}-viewer`;
              if (
                pid === "html" ||
                filePath.toLowerCase().endsWith(".html") ||
                filePath.toLowerCase().endsWith(".htm")
              ) {
                targetViewer = "html-viewer";
              }
              const artifact: ArtifactRef = {
                id: `artifact:${pid}:${sid}:${filePath}`,
                name: titleStr || name,
                kind: "workspace_file",
                path: filePath,
                mediaType:
                  pid === "html" || filePath.toLowerCase().endsWith(".html")
                    ? "text/html"
                    : "text/plain",
                readUrl:
                  urlStr ||
                  (filePath
                    ? `/api/v1/files/raw?path=${encodeURIComponent(filePath)}`
                    : ""),
                writable: true,
                sessionId: sid,
                agentId: aid,
              };
              sidecar.openArtifact(artifact, targetViewer);
            }
          } else if (action === "close") {
            if (payload.collapse) {
              sidecar.setIsOpen(false);
            } else if (payload.tab_id) {
              sidecar.closeTab(String(payload.tab_id));
            }
          }
        }
        if (event.type === "monitor.control" && event.data) {
          const payload = event.data as Record<string, unknown>;
          const action = payload.action;
          const MONITOR_STORAGE_KEY = "astrorder:monitor-sessions";
          const GRID_STORAGE_KEY = "astrorder:monitor-grid-cols";
          if (action === "add_sessions") {
            const keys = (payload.keys as string[]) || [];
            try {
              const raw = localStorage.getItem(MONITOR_STORAGE_KEY);
              const current: string[] = raw ? JSON.parse(raw) : [];
              const next = [...current];
              for (const k of keys) {
                if (!next.includes(k)) next.push(k);
              }
              localStorage.setItem(MONITOR_STORAGE_KEY, JSON.stringify(next));
              window.dispatchEvent(
                new CustomEvent("astrorder:monitor-sessions-changed", {
                  detail: next,
                }),
              );
            } catch {}
          } else if (action === "remove_session") {
            const keyToRemove = String(payload.key || "");
            try {
              const raw = localStorage.getItem(MONITOR_STORAGE_KEY);
              const current: string[] = raw ? JSON.parse(raw) : [];
              const next = current.filter((k) => k !== keyToRemove);
              localStorage.setItem(MONITOR_STORAGE_KEY, JSON.stringify(next));
              window.dispatchEvent(
                new CustomEvent("astrorder:monitor-sessions-changed", {
                  detail: next,
                }),
              );
            } catch {}
          } else if (action === "set_layout") {
            const cols = Number(payload.columns || 2);
            try {
              localStorage.setItem(GRID_STORAGE_KEY, String(cols));
            } catch {}
          }
        }
        if (event.type === "swarm.telemetry.event" && event.data) {
          window.dispatchEvent(
            new CustomEvent("astrorder:swarm-telemetry-changed", {
              detail: event.data,
            }),
          );
        }
        if (event.type === "swarm.sos.event" && event.data) {
          window.dispatchEvent(
            new CustomEvent("astrorder:swarm-sos-changed", {
              detail: event.data,
            }),
          );
        }
        if (event.type === "extension.changed") {
          void useExtensionStore.getState().refresh();
          if (event.data?.action === "settings_updated") {
            void usePluginSettingsStore.getState().hydrate(true);
            const changedId = typeof event.data?.plugin_id === "string" ? event.data.plugin_id : "";
            if (changedId) {
              void extensionsApi.getExtensionConfig(changedId).then((res) => {
                if (res?.values) {
                  usePluginSettingsStore.getState().syncExtensionConfigLocal(changedId, res.values, false);
                }
              }).catch(() => {});
            }
          }
        }
        if (useAstrorderStore.getState().resyncRequired) {
          const now = Date.now();
          if (now - lastResyncInvalidatedAt >= 3_000) {
            lastResyncInvalidatedAt = now;
            void queryClient.invalidateQueries({
              queryKey: ["astrorder", "bootstrap"],
            });
            void queryClient.invalidateQueries({
              queryKey: ["astrorder", "messages"],
            });
            void queryClient.invalidateQueries({
              queryKey: ["astrorder", "commands"],
            });
          }
        }
      },
    });
    return () => {
      flushDeltas();
      disposed = true;
      if (typeof document !== "undefined") {
        document.removeEventListener("visibilitychange", onDocVisibilityChange);
      }
      for (const refresh of hookRefreshes.values()) clearTimeout(refresh.timer);
      hookRefreshes.clear();
      messageInvalidator.clear();
      usageInvalidator.clear();
      swarmInvalidator.clear();
      stop();
    };
  }, [authenticated, queryClient]);
}
import { useSidecarStore } from "../features/sidecar/sidecarStore";
import type { ArtifactRef } from "../domain/artifact";
