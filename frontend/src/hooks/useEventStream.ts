import { useEffect, useRef } from "react";
import { notifications as mantineNotifications } from "@mantine/notifications";
import type { QueryClient } from "@tanstack/react-query";
import { connectEventStream, type EventStreamStatus } from "../api/eventStream";
import {
  deliverBrowserNotification,
  notificationForEvent,
} from "../domain/notifications";
import { useAstrorderStore } from "../state/store";
import { api } from "../api/client";
import { nativeActivityEvent } from "./useSessionOrder";

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
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const result = await api.getPresence();
        if (!stopped)
          window.dispatchEvent(
            new CustomEvent(nativeActivityEvent, { detail: result }),
          );
      } catch {
        /* A temporarily unavailable source must not discard its last activity. */
      }
      if (!stopped) timer = setTimeout(() => void poll(), 5000);
    };
    timer = setTimeout(() => void poll(), 2000);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [authenticated]);
  useEffect(() => {
    if (!authenticated) {
      useAstrorderStore.getState().setConnection("disconnected");
      return undefined;
    }

    const stop = connectEventStream({
      after: useAstrorderStore.getState().cursor,
      onStatus: storeStatus,
      onEvent: (event) => {
        const store = useAstrorderStore.getState();
        const notification =
          event.cursor > store.cursor ? notificationForEvent(event) : null;
        if (notification && store.addNotification(notification)) {
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
                const prefix = isMobile ? "/mobile" : "";
                const targetPath =
                  prefix +
                  "/chat/" +
                  encodeURIComponent(notification.session_id) +
                  "?agent_id=" +
                  encodeURIComponent(notification.agent_id);
                const nav = navigateRef.current || navigate;
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
          deliverBrowserNotification(notification);
        }
        store.applyEvent(event);
        if (
          event.type === "command.upsert" &&
          ["completed", "failed", "cancelled"].includes(
            String(event.data.state),
          )
        ) {
          void queryClient.invalidateQueries({
            queryKey: [
              "astrorder",
              "messages",
              event.agent_id,
              event.session_id,
            ],
          });
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
            void queryClient.invalidateQueries({
              queryKey: [
                "astrorder",
                "messages",
                event.agent_id,
                event.session_id,
              ],
            });
        }
        if (event.type === "blackboard.change") {
          window.dispatchEvent(
            new CustomEvent("astrorder:blackboard-change", {
              detail: event.data,
            }),
          );
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
        if (useAstrorderStore.getState().resyncRequired) {
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
      },
    });
    return stop;
  }, [authenticated, queryClient]);
}
import { useSidecarStore } from "../features/sidecar/sidecarStore";
import type { ArtifactRef } from "../domain/artifact";
