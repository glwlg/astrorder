import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api } from "../../api/client";
import type { GroupFiles } from "../../domain/types";
import { FileSourceContext } from "../sidecar/FileSourceContext";

export function GroupFileSourceScope({ groupId, revision, children }: {
  groupId: string;
  revision?: string;
  children: ReactNode;
}) {
  const [directory, setDirectory] = useState<{ groupId: string; result: GroupFiles }>({ groupId, result: { items: [], references: [], checking: false } });
  const currentGroup = useRef(groupId);
  currentGroup.current = groupId;
  const mounted = useRef(false);
  const requestId = useRef(0);
  const loadResult = useCallback(async (recheck = false) => {
    const id = ++requestId.current;
    try {
      const result = await (recheck ? api.recheckGroupArtifacts(groupId) : api.getGroupArtifacts(groupId));
      if (mounted.current && currentGroup.current === groupId && id === requestId.current) setDirectory({ groupId, result });
      return result.references;
    } catch (error) {
      if (mounted.current && currentGroup.current === groupId && id === requestId.current) {
        setDirectory((previous) => ({ groupId, result: {
          ...(previous.groupId === groupId ? previous.result : { items: [], references: [] }),
          checking: false, error: error instanceof Error ? error.message : "读取群聊文件记录失败",
        } }));
      }
      throw error;
    }
  }, [groupId]);
  const load = useCallback(() => loadResult(), [loadResult]);
  const recheck = useCallback(async () => { await loadResult(true); }, [loadResult]);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; requestId.current++; };
  }, [groupId]);
  useEffect(() => {
    const refresh = () => { void load().catch(() => { /* Clicks display the concrete request error. */ }); };
    const changed = (event: Event) => {
      const detail = (event as CustomEvent<{ namespace?: string; key?: string }>).detail;
      if (detail?.namespace === `group:${groupId}` && detail.key === "artifacts") refresh();
    };
    refresh();
    window.addEventListener("astrorder:blackboard-change", changed);
    return () => { window.removeEventListener("astrorder:blackboard-change", changed); };
  }, [groupId, revision, load]);
  const result = directory.groupId === groupId ? directory.result : null;
  useEffect(() => {
    if (!result?.checking) return;
    const timer = window.setTimeout(() => { void load().catch(() => {}); }, 1000);
    return () => window.clearTimeout(timer);
  }, [result, load]);
  const value = useMemo(() => ({ groupId, files: result?.references || [], checking: result?.checking, error: result?.error, load, recheck }), [groupId, result, load, recheck]);
  return <FileSourceContext value={value}>{children}</FileSourceContext>;
}
