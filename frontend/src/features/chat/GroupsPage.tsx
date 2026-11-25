import { useEffect, useState } from "react";
import { Button, Center, Paper, Stack, Text, Title } from "@mantine/core";
import { IconUsers } from "@tabler/icons-react";
import { useNavigate, useParams } from "react-router-dom";
import { api } from "../../api/client";
import type { BotGroup } from "../../domain/types";
import { useAstrorderStore } from "../../state/store";
import { BotGroupChatPage } from "./BotGroupChatPage";
import { CreateBotGroupDialog } from "../../components/CreateBotGroupDialog";
import { groupSidecarKey, useSidecarStore } from "../sidecar/sidecarStore";
import { GroupSidecarPanel } from "./GroupSidecarPanel";
import { isUserIdle, subscribeUserIdle } from "../../hooks/useUserIdle";

export function GroupsPage({
  routePrefix = "",
  surface = "desktop",
  groupId: groupIdProp,
  onGroupUpdated,
}: {
  routePrefix?: string;
  surface?: "desktop" | "mobile";
  groupId?: string;
  onGroupUpdated?: (group: BotGroup) => void;
}) {
  const params = useParams();
  const groupId = groupIdProp ?? params.groupId;
  const navigate = useNavigate();
  const agents = useAstrorderStore((state) => state.agents);
  const storeGroup = useAstrorderStore((state) => (groupId ? state.botGroups[groupId] : undefined));
  const [botGroups, setBotGroups] = useState<BotGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [activeGroup, setActiveGroup] = useState<BotGroup | null>(null);
  const [createOpened, setCreateOpened] = useState(false);
  const [settingsOpened, setSettingsOpened] = useState(false);

  const loadGroups = async () => {
    try {
      const res = await api.listBotGroups();
      setBotGroups(res.items || []);
      if (groupId) {
        const current = res.items?.find((group) => group.id === groupId);
        if (current) setActiveGroup(current);
      }
      return res.items || [];
    } catch {
      return [];
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let mounted = true;
    void loadGroups().then((items) => {
      if (!mounted) return;
      if (groupId) {
        const found = items.find((g) => g.id === groupId);
        if (found) setActiveGroup(found);
        else {
          void api
            .getBotGroup(groupId)
            .then((res) => {
              if (mounted) setActiveGroup(res.group);
            })
            .catch(() => {
              if (mounted) setActiveGroup(null);
            });
        }
      } else if (items.length > 0) {
        navigate(`${routePrefix}/groups/${items[0].id}`, { replace: true });
      }
    });
    const timer = setInterval(() => {
      if (isUserIdle()) return;
      void loadGroups();
    }, 10000);
    const unsubscribeIdle = subscribeUserIdle((idle) => {
      if (!idle) void loadGroups();
    });
    return () => {
      mounted = false;
      clearInterval(timer);
      unsubscribeIdle();
    };
  }, [groupId, navigate, routePrefix]);

  const activeSidecarKey = useSidecarStore((state) => state.activeSessionKey);
  const activeTabId = useSidecarStore((state) => state.activeTabId);
  const paneOpen = useSidecarStore((state) => state.isOpen);
  const ownerKey = groupId ? groupSidecarKey(groupId) : null;
  const sidecarOpen = activeSidecarKey === ownerKey && paneOpen;
  const blackboardOpen = sidecarOpen && activeTabId === `blackboard:${ownerKey}`;
  useEffect(() => {
    if (!groupId || activeGroup?.id !== groupId) return;
    const store = useSidecarStore.getState();
    const key = groupSidecarKey(groupId);
    const remembered = store.activeSessionKey === key || Boolean(store.sessionMemories[key]);
    store.switchSession(key);
    if (surface === "mobile") {
      if (useSidecarStore.getState().isOpen) useSidecarStore.getState().setIsOpen(false);
      return;
    }
    if (!remembered && localStorage.getItem(`astrorder:group:blackboard:${groupId}`) === "open") {
      store.openGroupBlackboard(groupId, activeGroup.name);
    }
    const onBlackboardChange = (event: Event) => {
      const detail = (event as CustomEvent<{ namespace?: string }>).detail;
      if (detail?.namespace === `group:${groupId}`) {
        localStorage.setItem(`astrorder:group:blackboard:${groupId}`, "open");
        useSidecarStore.getState().openGroupBlackboard(groupId, activeGroup.name);
      }
    };
    window.addEventListener("astrorder:blackboard-change", onBlackboardChange);
    return () =>
      window.removeEventListener(
        "astrorder:blackboard-change",
        onBlackboardChange,
      );
  }, [groupId, activeGroup?.id, activeGroup?.name, surface]);

  const resolvedGroup =
    activeGroup && (!groupId || activeGroup.id === groupId)
      ? activeGroup
      : groupId
        ? botGroups.find((g) => g.id === groupId) || storeGroup || null
        : null;

  const toggleBlackboard = () => {
    if (!resolvedGroup || resolvedGroup.id !== groupId) return;
    const store = useSidecarStore.getState();
    if (blackboardOpen) store.setIsOpen(false);
    else store.openGroupBlackboard(resolvedGroup.id, resolvedGroup.name);
    if (surface !== "mobile") {
      localStorage.setItem(`astrorder:group:blackboard:${groupId}`, blackboardOpen ? "closed" : "open");
    }
  };
  if (resolvedGroup) {
    return (
      <div className="route-page chat-page group-page">
        <div
          className={`chat-layout group-chat-layout${sidecarOpen ? " has-group-sidecar" : ""}`}
        >
          <section
            className="chat-column"
            style={{
              display: "flex",
              flexDirection: "column",
              minWidth: 0,
              height: "100%",
            }}
          >
            <BotGroupChatPage
              group={resolvedGroup}
              agents={agents}
              surface={surface}
              blackboardOpen={blackboardOpen}
              onToggleBlackboard={toggleBlackboard}
              onOpenBlackboard={(key) => {
                useSidecarStore.getState().openGroupBlackboard(resolvedGroup.id, resolvedGroup.name, key);
                localStorage.setItem(`astrorder:group:blackboard:${resolvedGroup.id}`, "open");
              }}
              onEdit={() => setSettingsOpened(true)}
            />
          </section>
          {activeSidecarKey === ownerKey && (
            <div className="group-sidecar-slot" hidden={!sidecarOpen}>
              <GroupSidecarPanel group={resolvedGroup} />
            </div>
          )}
        </div>
        <CreateBotGroupDialog
          agents={agents}
          opened={settingsOpened}
          group={resolvedGroup}
          onClose={() => setSettingsOpened(false)}
          onCreated={(updated) => {
            setActiveGroup(updated);
            setBotGroups((prev) => prev.map((item) => (item.id === updated.id ? updated : item)));
            useAstrorderStore.setState((state) => ({
              botGroups: { ...state.botGroups, [updated.id]: updated },
            }));
            onGroupUpdated?.(updated);
          }}
        />
      </div>
    );
  }

  return (
    <Center style={{ flex: 1, minHeight: "100%", padding: 24 }}>
      <Paper
        withBorder
        radius="lg"
        p="xl"
        style={{ maxWidth: 460, textAlign: "center" }}
      >
        <Stack align="center" gap="md">
          <IconUsers
            size={38}
            color="var(--astr-indigo, #5b6cff)"
            stroke={1.5}
          />
          <Title order={3}>群聊协作 (Bots)</Title>
          <Text size="sm" c="dimmed">
            {loading
              ? "正在加载群聊会话…"
              : "当前暂无活跃群聊。支持将多个 Agent 汇聚于同一群聊，通过 @ 协同接力完成复合任务。"}
          </Text>
          <Button
            color="violet"
            onClick={() => setCreateOpened(true)}
            leftSection={<IconUsers size={16} />}
          >
            新建群聊
          </Button>
        </Stack>
      </Paper>
      <CreateBotGroupDialog
        agents={agents}
        opened={createOpened}
        onClose={() => setCreateOpened(false)}
        onCreated={(g) => {
          setBotGroups((prev) => [g, ...prev]);
          useAstrorderStore.setState((state) => ({
            botGroups: { ...state.botGroups, [g.id]: g },
          }));
          onGroupUpdated?.(g);
          navigate(`${routePrefix}/groups/${g.id}`);
        }}
      />
    </Center>
  );
}
