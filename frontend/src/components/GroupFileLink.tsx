import { useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { IconFileText, IconPhoto } from "@tabler/icons-react";
import { notifications } from "@mantine/notifications";
import type { GroupFileReference, Session } from "../domain/types";
import { FileSourceContext } from "../features/sidecar/FileSourceContext";
import { resolveArtifactFromPath, splitPathAndLine } from "../features/sidecar/resolver";
import { findGroupFile } from "../features/chat/groupFileSources";
import { artifactViewerRegistry } from "../features/sidecar/registry";
import { groupSidecarKey, useSidecarStore } from "../features/sidecar/sidecarStore";
import { hasActiveDatabaseTool, isSqlFilePath, openSqlFileInDatabaseTool } from "../features/sidecar/databaseTool";
import { MarkdownLinkMenu } from "./MarkdownLinkMenu";

export function groupFileSession(file: GroupFileReference): Session {
  return {
    id: file.session_id, agent_id: file.agent_id, connection_id: file.connection_id,
    workspace: file.workspace, title: file.member_name, status: "idle", updated_at: file.created_at,
  };
}

export function GroupFileLink({ href, children, image, onImageClick }: {
  href: string;
  children?: ReactNode;
  image?: string;
  onImageClick?: (url: string) => void;
}) {
  const scope = useContext(FileSourceContext)!;
  const [menu, setMenu] = useState<{ x: number; y: number; file: GroupFileReference } | null>(null);
  const pending = useRef(false);
  const mounted = useRef(true);
  const identity = `${scope.groupId}:${href}`;
  const identityRef = useRef(identity);
  identityRef.current = identity;
  useEffect(() => {
    mounted.current = true;
    setMenu(null);
    return () => { mounted.current = false; };
  }, [identity]);
  const registered = findGroupFile(scope.files, href);
  const showError = (error: unknown) => {
    if (mounted.current && identityRef.current === identity) notifications.show({ color: "red", title: "打开文件失败", message: error instanceof Error ? error.message : "文件来源不可用" });
  };
  const resolve = async (action: (file: GroupFileReference) => void) => {
    if (pending.current) return;
    pending.current = true;
    try {
      const file = registered || findGroupFile(await scope.load(), href);
      if (!file) throw new Error("该文件的来源尚未核验");
      if (!mounted.current || identityRef.current !== identity || useSidecarStore.getState().activeSessionKey !== groupSidecarKey(scope.groupId)) return;
      action(file);
    } catch (error) { showError(error); }
    finally { pending.current = false; }
  };
  const artifact = (file: GroupFileReference) => {
    const ref = resolveArtifactFromPath(file.path.replace(/%/g, "%25"), groupFileSession(file), { name: file.name.replace(/%/g, "%25") });
    const { line } = splitPathAndLine(href.replace(/%(?![\da-f]{2})/gi, "%25"));
    return { ...ref, id: `group-file:${file.group_id}:${file.id}`, metadata: { ...ref.metadata, ...(line ? { line } : {}), groupFileReference: file } };
  };
  const open = (file: GroupFileReference) => {
    const ref = artifact(file);
    if (image && onImageClick) { onImageClick(ref.readUrl); return; }
    if (!image && hasActiveDatabaseTool() && isSqlFilePath(file.path)) {
      const line = typeof ref.metadata?.line === "number" ? ref.metadata.line : undefined;
      void openSqlFileInDatabaseTool({ path: file.path, session: groupFileSession(file), line });
      return;
    }
    const viewer = artifactViewerRegistry.findViewer(ref);
    if (viewer) useSidecarStore.getState().openArtifact(ref, viewer.id);
    else useSidecarStore.getState().revealFileInTree(ref.sessionId, ref.agentId, file.path, file.workspace, file.member_name, ref.connectionId);
  };
  return <>
    <button type="button" className={`markdown-local-link markdown-file-link${image ? " markdown-image-link" : ""}`}
      title={registered ? `来自 ${registered.member_name}：${registered.path}` : "打开群聊文件"}
      onClick={() => void resolve(open)}
      onContextMenu={(event) => {
        event.preventDefault();
        event.stopPropagation();
        const { clientX: x, clientY: y } = event;
        void resolve((file) => setMenu({ x, y, file }));
      }}>
      {image ? <IconPhoto size={13} aria-hidden /> : <IconFileText size={13} aria-hidden />}
      {image && registered ? <img src={artifact(registered).readUrl} alt={image} loading="lazy" /> : <span>{children || image}</span>}
      {!image && <span className="markdown-file-link-label">[文件]</span>}
    </button>
    {menu && createPortal(<MarkdownLinkMenu target={{ x: menu.x, y: menu.y, kind: "file", value: menu.file.path }} session={groupFileSession(menu.file)} onClose={() => setMenu(null)} />, document.body)}
  </>;
}
