import { useEffect, useRef, useState } from "react";
import { useComputedColorScheme } from "@mantine/core";
import {
  IconArrowLeft,
  IconCheck,
  IconChevronDown,
  IconPlus,
  IconUsers,
  IconX,
} from "@tabler/icons-react";
import { useNavigate, useParams } from "react-router-dom";
import { api } from "../../api/client";
import { CreateBotGroupDialog } from "../../components/CreateBotGroupDialog";
import type { BotGroup } from "../../domain/types";
import { useAstrorderStore } from "../../state/store";
import { GroupsPage } from "../chat/GroupsPage";
import { useMobileVisualViewport } from "./useMobileVisualViewport";
import "./mobile.css";

export function MobileGroupsPage() {
  const navigate = useNavigate();
  const { groupId } = useParams();
  const workspaceRef = useRef<HTMLDivElement>(null);
  useMobileVisualViewport(workspaceRef);
  const storeGroups = useAstrorderStore((state) => state.botGroups);
  const agents = useAstrorderStore((state) => state.agents);
  const computedColorScheme = useComputedColorScheme("light");
  const [fetchedGroups, setFetchedGroups] = useState<BotGroup[]>([]);
  const [pickerOpened, setPickerOpened] = useState(false);
  const [createOpened, setCreateOpened] = useState(false);

  useEffect(() => {
    const themeColor = computedColorScheme === "dark" ? "#11141b" : "#f6f4fa";
    let meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]:not([media])');
    if (!meta) {
      meta = document.createElement("meta");
      meta.name = "theme-color";
      document.head.appendChild(meta);
    }
    meta.content = themeColor;
  }, [computedColorScheme]);

  useEffect(() => {
    let active = true;
    void api.listBotGroups()
      .then((res) => {
        if (active && Array.isArray(res.items)) setFetchedGroups(res.items);
      })
      .catch(() => {});
    return () => { active = false; };
  }, [groupId]);

  const groupMap = new Map<string, BotGroup>();
  Object.values(storeGroups).forEach((g) => groupMap.set(g.id, g));
  fetchedGroups.forEach((g) => groupMap.set(g.id, g));
  const groups = Array.from(groupMap.values());
  const currentGroup = groups.find((g) => g.id === groupId) || null;

  return (
    <div ref={workspaceRef} className="mobile-workspace m-groups-workspace" data-sheet={pickerOpened ? "groups" : undefined}>
      <header className="m-header">
        <div className="m-brand m-groups-brand">
          <button
            type="button"
            className="m-session-nav"
            aria-label="返回会话"
            onClick={() => navigate("/mobile")}
          >
            <IconArrowLeft size={21} />
          </button>
          <button
            type="button"
            className="m-group-picker-btn"
            aria-label="切换群聊"
            aria-expanded={pickerOpened}
            onClick={() => setPickerOpened(true)}
          >
            <IconUsers size={17} className="m-group-picker-icon" aria-hidden="true" />
            <span>{currentGroup ? currentGroup.name : "群聊"}</span>
            <IconChevronDown size={15} className="m-group-picker-chevron" aria-hidden="true" />
          </button>
        </div>
        <div className="m-header-actions">
          <button
            type="button"
            aria-label="新建群聊"
            onClick={() => setCreateOpened(true)}
          >
            <IconPlus size={21} />
          </button>
        </div>
      </header>

      <main className="m-groups-main">
        <GroupsPage routePrefix="/mobile" surface="mobile" />
      </main>

      {pickerOpened && (
        <div className="m-backdrop" onClick={() => setPickerOpened(false)}>
          <section
            className="m-sheet m-groups-sheet"
            role="dialog"
            aria-label="选择群聊"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="m-handle" />
            <header>
              <h2>群聊 ({groups.length})</h2>
              <div className="m-sheet-header-actions">
                <button
                  type="button"
                  aria-label="新建群聊"
                  onClick={() => {
                    setPickerOpened(false);
                    setCreateOpened(true);
                  }}
                >
                  <IconPlus size={20} />
                </button>
                <button
                  type="button"
                  aria-label="关闭面板"
                  onClick={() => setPickerOpened(false)}
                >
                  <IconX size={20} />
                </button>
              </div>
            </header>
            <div className="m-sheet-body m-groups-list">
              {groups.length === 0 ? (
                <div className="m-empty">暂无群聊，点击右上角新建</div>
              ) : (
                groups.map((group) => {
                  const selected = group.id === groupId;
                  return (
                    <button
                      type="button"
                      key={group.id}
                      className={`m-group-row${selected ? " is-selected" : ""}`}
                      aria-current={selected ? "page" : undefined}
                      onClick={() => {
                        setPickerOpened(false);
                        navigate(`/mobile/groups/${group.id}`);
                      }}
                    >
                      <span className="m-group-row-icon" aria-hidden="true">
                        <IconUsers size={18} />
                      </span>
                      <span className="m-group-row-copy">
                        <strong>{group.name}</strong>
                        <small>
                          {group.members.length} 位成员
                          {group.description ? ` · ${group.description}` : ""}
                        </small>
                      </span>
                      {selected && <IconCheck size={17} className="m-group-row-check" aria-hidden="true" />}
                    </button>
                  );
                })
              )}
            </div>
          </section>
        </div>
      )}

      <CreateBotGroupDialog
        agents={agents}
        opened={createOpened}
        onClose={() => setCreateOpened(false)}
        onCreated={(created) => {
          setFetchedGroups((prev) => [created, ...prev.filter((item) => item.id !== created.id)]);
          setCreateOpened(false);
          navigate(`/mobile/groups/${created.id}`);
        }}
      />
    </div>
  );
}
