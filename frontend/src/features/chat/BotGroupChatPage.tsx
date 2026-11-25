import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type ClipboardEvent } from "react";
import {
  ActionIcon,
  Badge,
  Button,
  Group,
  Paper,
  Stack,
  Text,
  Textarea,
  UnstyledButton,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
  IconAt,
  IconChevronDown,
  IconLoader2,
  IconMicrophone,
  IconPlayerStop,
  IconPlus,
  IconChalkboard,
  IconSend,
  IconSettings,
  IconUsers,
  IconX,
} from "@tabler/icons-react";
import { api } from "../../api/client";
import { isUserIdle, subscribeUserIdle } from "../../hooks/useUserIdle";
import type {
  Agent,
  Approval,
  BotGroup,
  BotGroupMemberState,
  DraftState,
  GroupMessage,
} from "../../domain/types";
import { AgentBrandIcon } from "../../components/AgentBrandIcon";
import { useAstrorderStore } from "../../state/store";
import { useShallow } from "zustand/react/shallow";
import { groupChatStatus } from "./groupChatStatus";
import { GroupChatMessage } from "./GroupChatMessage";
import { GroupChatActivity } from "./GroupChatActivity";
import { GroupChatHeader } from "./GroupChatHeader";
import { GroupChatNotice } from "./GroupChatNotice";
import { GroupChatImageDraft } from "./GroupChatImages";
import { clipboardFiles } from "./composerMedia";
import { scopeKey } from "../../domain/semantics";
import { GroupFileSourceScope } from "./GroupFileSourceScope";
import { applyVoiceTranscript, useVoiceInput } from "./useVoiceInput";
import { MobileVoiceHold } from "../mobile/MobileVoiceHold";
import { VOICE_INPUT_PLUGIN_ID } from "../plugins/builtinPlugins";
import { usePluginSettingsStore } from "../plugins/pluginSettingsStore";

const EMPTY_GROUP_DRAFT: DraftState = { text: "", attachments: [] };

export function BotGroupChatPage({
  group,
  agents,
  surface = "desktop",
  onClose,
  blackboardOpen,
  onToggleBlackboard,
  onOpenBlackboard,
  onEdit,
}: {
  group: BotGroup;
  agents: Record<string, Agent>;
  surface?: "desktop" | "mobile";
  onClose?: () => void;
  blackboardOpen?: boolean;
  onToggleBlackboard?: () => void;
  onOpenBlackboard?: (key: string) => void;
  onEdit?: () => void;
}) {
  const isMobile = surface === "mobile";
  const imageInputRef = useRef<HTMLInputElement>(null);
  const [messages, setMessages] = useState<GroupMessage[]>([]);
  const [historyLoaded, setHistoryLoaded] = useState(false);
  const [historyError, setHistoryError] = useState("");
  const [stateError, setStateError] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const draft = useAstrorderStore((state) => state.drafts[scopeKey("group", group.id)] || EMPTY_GROUP_DRAFT);
  const input = draft.text;
  const updateDraft = (update: (current: DraftState) => DraftState) => {
    const store = useAstrorderStore.getState();
    store.setDraft("group", group.id, update(store.drafts[scopeKey("group", group.id)] || EMPTY_GROUP_DRAFT));
  };
  const setInput = (value: string | ((current: string) => string)) =>
    updateDraft((current) => ({ ...current, text: typeof value === "function" ? value(current.text) : value }));
  const [submitting, setSubmitting] = useState(false);
  const submittingRef = useRef(false);
  const [activeHop, setActiveHop] = useState(group.active_hop || 0);
  const [maxHops, setMaxHops] = useState(group.max_hops);
  const [activeSpeaker, setActiveSpeaker] = useState<string | null>(
    group.active_speaker_agent_id || null,
  );
  const [activeSpeakers, setActiveSpeakers] = useState<string[]>(
    group.active_speakers || [],
  );
  const [memberStates, setMemberStates] = useState<
    Record<string, BotGroupMemberState>
  >(group.member_states || {});
  const [coordination, setCoordination] = useState(group.coordination);
  const [muteBusy, setMuteBusy] = useState(false);
  const muteBusyRef = useRef(false);
  const muteRevisionRef = useRef(0);
  const [retrying, setRetrying] = useState(false);
  const [recovering, setRecovering] = useState(false);
  const [approvalBusy, setApprovalBusy] = useState<string | null>(null);
  const [motionPaused, setMotionPaused] = useState(false);
  const approvals = useAstrorderStore(
    useShallow((state) => Object.values(state.approvals)),
  );
  const groupSessionIds = new Set(
    Object.values(memberStates)
      .map((state) => state.session_id)
      .filter(Boolean),
  );
  const pendingApprovals = approvals.filter(
    (approval) =>
      approval.state === "pending" &&
      approval.session_id &&
      groupSessionIds.has(approval.session_id),
  );
  const [mentionMenuOpen, setMentionMenuOpen] = useState(false);
  const [mentionQuery, setMentionQuery] = useState("");
  const [mentionIndex, setMentionIndex] = useState(0);
  const scrollRef = useRef<HTMLDivElement>(null);
  const pageRef = useRef<HTMLDivElement>(null);
  const composerRef = useRef<HTMLElement>(null);
  const followLatestRef = useRef(true);
  const [followingLatest, setFollowingLatest] = useState(true);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const currentGroupRef = useRef(group.id);
  const loadRequestRef = useRef(0);
  const activeLoadRef = useRef<{ groupId: string; request: number } | null>(null);
  currentGroupRef.current = group.id;
  const groupScope = scopeKey("group", group.id);
  const voicePluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(VOICE_INPUT_PLUGIN_ID));
  const voice = useVoiceInput(groupScope, (transcript, event) => {
    if (event.kind === "final" && event.submit) {
      void send(transcript);
    } else {
      setInput((current) => applyVoiceTranscript(current, transcript, event));
    }
  });

  useLayoutEffect(() => {
    const page = pageRef.current;
    const composer = composerRef.current;
    if (!page || !composer) return;
    const measure = () => {
      const height = Math.ceil(composer.getBoundingClientRect().height);
      if (height > 0) page.style.setProperty("--group-composer-height", `${height}px`);
      if (followLatestRef.current && scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(composer);
    return () => observer.disconnect();
  }, []);

  useLayoutEffect(() => {
    if (!isMobile) return;
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "38px";
    el.style.height = `${Math.min(132, Math.max(38, el.scrollHeight))}px`;
  }, [input, group.id, isMobile]);

  const applyGroupSnapshot = (snapshot: BotGroup) => {
    setActiveHop(snapshot.active_hop || 0);
    setMaxHops(snapshot.max_hops);
    setActiveSpeaker(snapshot.active_speaker_agent_id || null);
    setActiveSpeakers(snapshot.active_speakers ||
      (snapshot.active_speaker_agent_id ? [snapshot.active_speaker_agent_id] : []));
    setMemberStates(snapshot.member_states || {});
    setCoordination(snapshot.coordination);
    setStateError("");
  };

  const loadMessages = async () => {
    if (activeLoadRef.current?.groupId === group.id) return;
    const requestedGroup = group.id;
    const request = ++loadRequestRef.current;
    const muteRevision = muteRevisionRef.current;
    activeLoadRef.current = { groupId: requestedGroup, request };
    const [resMsgs, resGroup] = await Promise.allSettled([
      api.listGroupMessages(requestedGroup),
      api.getBotGroup(requestedGroup),
    ]);
    if (activeLoadRef.current?.request === request) activeLoadRef.current = null;
    if (currentGroupRef.current !== requestedGroup || request !== loadRequestRef.current) return;
    if (resMsgs.status === "fulfilled") {
      setMessages(resMsgs.value.items);
      setHistoryLoaded(true);
      setHistoryError("");
    } else {
      setHistoryError(resMsgs.reason instanceof Error ? resMsgs.reason.message : "请稍后重试");
    }
    if (resGroup.status === "fulfilled") {
      if (muteRevision === muteRevisionRef.current && !muteBusyRef.current) {
        applyGroupSnapshot(resGroup.value.group);
      }
    } else if (muteRevision === muteRevisionRef.current && !muteBusyRef.current) {
      setStateError(resGroup.reason instanceof Error ? resGroup.reason.message : "请稍后重试");
    }
  };

  useEffect(() => {
    followLatestRef.current = true;
    setFollowingLatest(true);
    setMessages([]);
    setHistoryLoaded(false);
    setHistoryError("");
    setStateError("");
    setActiveHop(group.active_hop || 0);
    setMaxHops(group.max_hops);
    setActiveSpeaker(group.active_speaker_agent_id || null);
    setActiveSpeakers(group.active_speakers || []);
    setMemberStates(group.member_states || {});
    setCoordination(group.coordination);
    void loadMessages();
    const timer = setInterval(() => {
      if (isUserIdle()) return;
      void loadMessages();
    }, 3000);
    const unsubscribeIdle = subscribeUserIdle((idle) => {
      if (!idle) void loadMessages();
    });
    return () => {
      clearInterval(timer);
      unsubscribeIdle();
    };
  }, [group.id]);

  useEffect(() => {
    setMaxHops(group.max_hops);
  }, [group.max_hops]);

  const activityKey = JSON.stringify([coordination?.status, activeSpeaker, activeSpeakers,
    Object.entries(memberStates).map(([id, state]) => [id, state.status])]);
  useEffect(() => {
    if (scrollRef.current && followLatestRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages.length, historyLoaded, activityKey]);

  // Mention items including @all
  const mentionItems = [
    {
      id: "__all__",
      name: "all",
      label: "所有人 (@all)",
      desc: "发送给所有未禁言成员",
      kind: undefined as string | undefined,
      isAll: true,
    },
    ...group.members.map((m) => {
      const ag = agents[m.agent_id];
      return {
        id: m.id || m.agent_id,
        name: m.alias || m.name || m.agent_id,
        label: `${m.name || m.agent_id}${m.alias ? " · " + m.alias : ""}`,
        desc: memberStates[m.id || m.agent_id]?.muted ? "已禁言，不接收消息" : ag
          ? `${ag.name || ag.id} (${m.machine_id === "local" ? "本机" : m.machine_id})${m.workspace ? ` · ${m.workspace}` : ""}`
          : `${m.machine_id}${m.workspace ? ` · ${m.workspace}` : ""}`,
        kind: ag?.kind,
        isAll: false,
      };
    }),
  ].filter((item) => {
    if (!mentionQuery) return true;
    const q = mentionQuery.toLowerCase();
    return (
      item.name.toLowerCase().includes(q) ||
      item.label.toLowerCase().includes(q) ||
      item.desc.toLowerCase().includes(q)
    );
  });

  const checkMentionTrigger = (val: string, cursor: number) => {
    const textBefore = val.slice(0, cursor);
    const match = textBefore.match(/@([^\s@]*)$/);
    if (match) {
      setMentionMenuOpen(true);
      setMentionQuery(match[1]);
      setMentionIndex(0);
    } else {
      setMentionMenuOpen(false);
      setMentionQuery("");
    }
  };

  const selectMention = (item: (typeof mentionItems)[0]) => {
    const el = textareaRef.current;
    const cursor = el?.selectionStart ?? input.length;
    const textBefore = input.slice(0, cursor);
    const textAfter = input.slice(cursor);
    const match = textBefore.match(/@([^\s@]*)$/);
    const mentionTag = item.isAll ? "@all " : `@${item.name} `;
    if (match) {
      const start = match.index ?? cursor - match[0].length;
      const nextText = input.slice(0, start) + mentionTag + textAfter;
      setInput(nextText);
      setTimeout(() => {
        if (el) {
          el.focus();
          const nextPos = start + mentionTag.length;
          el.setSelectionRange(nextPos, nextPos);
        }
      }, 20);
    } else {
      setInput((prev) => `${prev}${mentionTag}`);
    }
    setMentionMenuOpen(false);
    setMentionQuery("");
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (mentionMenuOpen && mentionItems.length > 0) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setMentionIndex((prev) => (prev + 1) % mentionItems.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setMentionIndex(
          (prev) => (prev - 1 + mentionItems.length) % mentionItems.length,
        );
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        if (e.nativeEvent.isComposing || e.keyCode === 229) return;
        selectMention(mentionItems[mentionIndex] || mentionItems[0]);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setMentionMenuOpen(false);
        return;
      }
    }

    if (isMobile) {
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        if (e.nativeEvent.isComposing || e.keyCode === 229) return;
        void send();
      }
      return;
    }

    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) {
      e.preventDefault();
      void send();
    }
  };

  const appendImageFiles = (files: File[], insertText?: { value: string; start: number; end: number }) => {
    const images = files.filter((file) => file.type.startsWith("image/"));
    if (!images.length || submittingRef.current) return false;
    if (images.some((file) => !["image/png", "image/jpeg", "image/gif", "image/webp"].includes(file.type))) {
      notifications.show({ color: "red", message: "群聊支持 PNG、JPEG、GIF 和 WebP 图片" });
      return true;
    }
    if (draft.attachments.length + images.length > 32) {
      notifications.show({ color: "red", message: "一次最多发送 32 张图片" });
      return true;
    }
    updateDraft((current) => ({
      ...current,
      text: insertText?.value
        ? current.text.slice(0, insertText.start) + insertText.value + current.text.slice(insertText.end)
        : current.text,
      attachments: [...current.attachments, ...images.map((file) => ({ key: crypto.randomUUID(), file }))],
    }));
    return true;
  };

  const handlePaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const files = clipboardFiles(event).filter((file) => file.type.startsWith("image/"));
    if (!files.length || submittingRef.current) return;
    event.preventDefault();
    const text = event.clipboardData.getData?.("text/plain") || "";
    const { selectionStart: start, selectionEnd: end } = event.currentTarget;
    appendImageFiles(files, { value: text, start, end });
  };

  const send = async (overrideText?: string) => {
    const mergedRaw = overrideText !== undefined
      ? (input ? (input.endsWith(" ") ? `${input}${overrideText}` : `${input}\n${overrideText}`) : overrideText)
      : input;
    if (submittingRef.current || (!mergedRaw.trim() && !draft.attachments.length) || !historyLoaded || historyError || stateError) return;
    const text = mergedRaw.trim();
    const requestedGroup = group.id;
    submittingRef.current = true;
    setSubmitting(true);
    setMentionMenuOpen(false);
    try {
      const attachmentIds: string[] = [];
      for (const item of draft.attachments) {
        const attachment = item.attachment || await api.uploadAttachment(item.file);
        attachmentIds.push(attachment.id);
        updateDraft((current) => ({ ...current, attachments: current.attachments.map((entry) => entry.key === item.key ? { ...entry, attachment } : entry) }));
      }
      const res = await api.sendGroupMessage(requestedGroup, { text, ...(attachmentIds.length ? { attachment_ids: attachmentIds } : {}) });
      const sentKeys = new Set(draft.attachments.map((item) => item.key));
      updateDraft((current) => ({
        ...current,
        text: current.text === draft.text ? "" : current.text,
        attachments: current.attachments.filter((item) => !sentKeys.has(item.key)),
      }));
      if (currentGroupRef.current !== requestedGroup) return;
      setMessages((prev) => [...prev, res.message]);
      await loadMessages();
    } catch (err) {
      notifications.show({
        color: "red",
        message: err instanceof Error ? err.message : "发送群消息失败",
      });
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
  };

  const stopGroup = async () => {
    try {
      await api.stopGroup(group.id);
      setActiveSpeaker(null);
      setActiveSpeakers([]);
      setActiveHop(0);
      setCoordination({ status: "stopped" });
      notifications.show({ color: "yellow", message: "已中断群聊接力流转" });
    } catch (err) {
      notifications.show({ color: "red", message: "中断失败" });
    }
  };

  const retryGroup = async () => {
    setRetrying(true);
    try {
      const result = await api.retryGroup(group.id);
      setCoordination(result.group.coordination);
      await loadMessages();
    } catch (err) {
      notifications.show({
        color: "red",
        message: err instanceof Error ? err.message : "重试失败",
      });
    } finally {
      setRetrying(false);
    }
  };

  const reloadHistory = async () => {
    setRefreshing(true);
    try {
      await loadMessages();
    } finally {
      setRefreshing(false);
    }
  };

  const recoverGroup = async () => {
    setRecovering(true);
    try {
      const result = await api.recoverGroup(group.id);
      setCoordination(result.group.coordination);
      await loadMessages();
    } catch (err) {
      notifications.show({
        color: "red",
        message: err instanceof Error ? err.message : "恢复回复失败",
      });
    } finally {
      setRecovering(false);
    }
  };

  const roundActive = coordination &&
    ["deciding", "running", "waiting", "error"].includes(coordination.status);
  const status = groupChatStatus(coordination, Boolean(activeSpeaker || activeSpeakers.length));
  const loadFailed = Boolean(historyError || stateError);
  const notice = historyError
    ? `群聊记录加载失败：${historyError}`
    : stateError
      ? `群聊状态更新失败：${stateError}`
      : coordination?.status === "error"
        ? coordination.error || "群聊任务未能继续"
        : "";
  let currentHop = activeHop;
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].sender_type === "user") break;
    currentHop = Math.max(currentHop, messages[i].hop_count);
  }
  const canRecover = Object.values(memberStates).some(
    (state) => state.status === "failed" && state.last_error === "context deadline exceeded",
  );

  const insertMentionDirect = (name: string) => {
    setInput((prev) => `${prev}@${name} `);
    textareaRef.current?.focus();
  };

  const toggleMemberMuted = async (ids: string[], muted: boolean) => {
    if (muteBusyRef.current) return;
    const requestedGroup = group.id;
    muteBusyRef.current = true;
    muteRevisionRef.current++;
    setMuteBusy(true);
    try {
      const result = await api.updateBotGroup(requestedGroup, {
        member_mutes: Object.fromEntries(ids.map((id) => [id, muted])),
      });
      if (currentGroupRef.current === requestedGroup) applyGroupSnapshot(result.group);
    } catch (err) {
      notifications.show({
        color: "red",
        message: err instanceof Error ? err.message : "更新成员禁言失败",
      });
    } finally {
      muteRevisionRef.current++;
      muteBusyRef.current = false;
      setMuteBusy(false);
    }
  };

  const respondToApproval = async (
    approval: Approval,
    action: "approve" | "cancel",
  ) => {
    if (!approval.agent_id || !approval.session_id || approvalBusy) return;
    setApprovalBusy(approval.id);
    try {
      await api.createCommand({
        id: crypto.randomUUID(),
        agent_id: approval.agent_id,
        session_id: approval.session_id,
        action,
        text: "",
        attachment_ids: [],
        target_id: approval.target_id || approval.id,
      });
      notifications.show({
        color: action === "approve" ? "teal" : "gray",
        message: action === "approve" ? "已允许" : "已拒绝",
      });
    } catch (err) {
      notifications.show({
        color: "red",
        message: err instanceof Error ? err.message : "审批失败",
      });
    } finally {
      setApprovalBusy(null);
    }
  };

  return (
    <GroupFileSourceScope groupId={group.id} revision={messages.at(-1)?.id}>
    <div ref={pageRef} className={`bot-group-chat-page${isMobile ? " is-mobile" : ""}`}>
      <GroupChatHeader
        group={group}
        agents={agents}
        surface={surface}
        memberStates={memberStates}
        coordination={coordination}
        activeSpeakers={activeSpeakers.length ? activeSpeakers : activeSpeaker ? [activeSpeaker] : []}
        status={status}
        currentHop={historyLoaded || activeHop > 0 ? currentHop : "—"}
        maxHops={maxHops}
        onToggleMuted={(ids, muted) => void toggleMemberMuted(ids, muted)}
        muteBusy={muteBusy}
        motionPaused={motionPaused}
        onToggleMotion={() => setMotionPaused((value) => !value)}
        actions={<>
            {canRecover && (
              <Button
                size={isMobile ? "compact-xs" : "compact-sm"}
                variant="light"
                loading={recovering}
                aria-label="恢复回复并继续"
                onClick={() => void recoverGroup()}
              >
                {isMobile ? "恢复" : "恢复回复并继续"}
              </Button>
            )}
            {onEdit && (
              isMobile ? (
                <ActionIcon
                  size={30}
                  radius="md"
                  variant="light"
                  color="gray"
                  aria-label="群设置"
                  title="群设置"
                  onClick={onEdit}
                >
                  <IconSettings size={15} />
                </ActionIcon>
              ) : (
                <Button
                  size="compact-sm"
                  variant="light"
                  color="gray"
                  leftSection={<IconSettings size={13} />}
                  onClick={onEdit}
                >
                  群设置
                </Button>
              )
            )}
            {onToggleBlackboard && (
              <Button
                size={isMobile ? "compact-xs" : "compact-sm"}
                variant={blackboardOpen ? "filled" : "light"}
                color="indigo"
                leftSection={<IconChalkboard size={13} />}
                onClick={onToggleBlackboard}
              >
                黑板
              </Button>
            )}
            {(activeSpeaker || roundActive) && (
              isMobile ? (
                <ActionIcon
                  size={30}
                  radius="md"
                  color="red"
                  variant="light"
                  aria-label="中断流转"
                  title="中断流转"
                  onClick={() => void stopGroup()}
                >
                  <IconPlayerStop size={15} />
                </ActionIcon>
              ) : (
                <Button
                  size="compact-sm"
                  color="red"
                  variant="light"
                  leftSection={<IconPlayerStop size={13} />}
                  onClick={() => void stopGroup()}
                >
                  中断流转
                </Button>
              )
            )}
            {onClose && (
              <ActionIcon variant="subtle" color="gray" onClick={onClose} aria-label="关闭群聊">
                <IconX size={16} />
              </ActionIcon>
            )}
        </>}
      >
        {notice && (
          <Paper mt="sm" p="sm" radius="md" withBorder role="alert">
            <Group justify="space-between" align="center">
              <Text size="sm" c="red" style={{ flex: "1 1 220px", overflowWrap: "anywhere" }}>
                {notice}
              </Text>
              <Group gap="xs">
                {loadFailed && (
                  <Button size="compact-xs" variant="light" loading={refreshing} onClick={() => void reloadHistory()}>
                    重新加载记录
                  </Button>
                )}
                {coordination?.status === "error" && (
                  <Button size="compact-xs" variant="light" disabled={loadFailed} loading={retrying} onClick={() => void retryGroup()}>
                    重新判断
                  </Button>
                )}
              </Group>
            </Group>
          </Paper>
        )}
        {!loadFailed && coordination?.status === "waiting" && (
          <Text mt="sm" size="sm" c="dimmed" role="status" aria-label="群聊发送状态">
            System One 暂缓发送，待发消息已保留；有新回复或消息后将重新判断。
          </Text>
        )}
      </GroupChatHeader>

      <div
        ref={scrollRef}
        className="bot-group-chat-messages"
        onScroll={(event) => {
          const el = event.currentTarget;
          const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 100;
          followLatestRef.current = atBottom;
          setFollowingLatest((prev) => (prev === atBottom ? prev : atBottom));
        }}
        aria-busy={!historyLoaded && !historyError}
      >
        <div className="group-chat-feed">
        {pendingApprovals.map((approval) => (
          <Paper
            key={approval.id}
            p="sm"
            radius="md"
            withBorder
            style={{ borderColor: "var(--mantine-color-yellow-5)" }}
          >
            <Group justify="space-between" align="center" wrap="nowrap">
              <div style={{ minWidth: 0 }}>
                <Text size="sm" fw={600}>
                  {approval.title || "等待授权"}
                </Text>
                <Text size="xs" c="dimmed" lineClamp={2}>
                  {approval.detail}
                </Text>
              </div>
              <Group gap="xs" wrap="nowrap">
                <Button
                  size="compact-xs"
                  color="teal"
                  loading={approvalBusy === approval.id}
                  onClick={() => void respondToApproval(approval, "approve")}
                >
                  允许
                </Button>
                <Button
                  size="compact-xs"
                  color="red"
                  variant="light"
                  disabled={Boolean(approvalBusy)}
                  onClick={() => void respondToApproval(approval, "cancel")}
                >
                  拒绝
                </Button>
              </Group>
            </Group>
          </Paper>
        ))}
        {!historyLoaded && !historyError && (
          <Text size="sm" c="dimmed" style={{ margin: "auto" }}>正在加载群聊记录…</Text>
        )}
        {historyLoaded && !historyError && messages.length === 0 && (
          <div className="group-chat-empty">
            <span className="group-chat-empty-icon"><IconUsers size={32} stroke={1.5} /></span>
            <Text fw={600} mb={6}>群聊已就绪</Text>
            <Text size="sm">
              输入 @ 选择成员，或 @all 发送给所有人。
            </Text>
            <Text size="xs" c="dimmed" mt={4}>
              每轮最多接力 {maxHops} 次。
            </Text>
          </div>
        )}

        {messages.map((message) => message.sender_type === "system" ? (
          <GroupChatNotice key={message.id} message={message} members={group.members} agents={agents} onOpenBlackboard={onOpenBlackboard} />
        ) : (
          <GroupChatMessage key={message.id} message={message} members={group.members} memberStates={memberStates} agents={agents} />
        ))}
        <GroupChatActivity
          key={group.id}
          members={historyLoaded && !loadFailed ? group.members : []}
          memberStates={memberStates}
          coordination={historyLoaded && !loadFailed ? coordination : { status: "idle" }}
          activeSpeakers={activeSpeakers.length ? activeSpeakers : activeSpeaker ? [activeSpeaker] : []}
          agents={agents}
          approvals={pendingApprovals}
          motionPaused={motionPaused}
          onToggleMotion={() => setMotionPaused((value) => !value)}
        />
        </div>
      </div>

      {isMobile && !followingLatest && (
        <button
          type="button"
          className="m-return-bottom m-group-return-bottom"
          aria-label="回到底部"
          onClick={() => {
            followLatestRef.current = true;
            setFollowingLatest(true);
            scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: "smooth" });
          }}
        >
          {roundActive || activeSpeaker ? <span className="return-bottom-wave" aria-hidden="true"><i /><i /><i /></span> : <IconChevronDown size={19} />}
        </button>
      )}

      {isMobile ? (
        <footer ref={composerRef} className="m-composer-float m-group-composer-float">
          {!mentionMenuOpen && group.members.length > 0 && group.members.every((member) => memberStates[member.id || member.agent_id]?.muted) && (
            <div className="m-group-muted-notice" role="status">全员已禁言，消息仅保存在群聊中</div>
          )}
          {draft.attachments.length > 0 && (
            <div className="group-chat-image-drafts m-group-image-drafts" aria-label="待发送图片">
              {draft.attachments.map((item) => (
                <GroupChatImageDraft
                  key={item.key}
                  file={item.file}
                  disabled={submitting}
                  onRemove={() => updateDraft((current) => ({ ...current, attachments: current.attachments.filter((entry) => entry.key !== item.key) }))}
                />
              ))}
            </div>
          )}
          <div style={{ position: "relative" }}>
            {mentionMenuOpen && mentionItems.length > 0 && (
              <div
                role="listbox"
                aria-label="群成员建议"
                className="group-chat-mention-menu m-group-mention-menu"
              >
                {mentionItems.map((item, idx) => {
                  const isSelected = idx === mentionIndex;
                  return (
                    <button
                      type="button"
                      key={item.id}
                      className={`m-group-mention-item${isSelected ? " is-selected" : ""}`}
                      onMouseDown={(e) => {
                        e.preventDefault();
                      }}
                      onClick={() => selectMention(item)}
                    >
                      {item.isAll ? (
                        <span className="m-group-mention-avatar is-all" aria-hidden="true">
                          <IconUsers size={15} />
                        </span>
                      ) : item.kind ? (
                        <span className="m-group-mention-avatar" aria-hidden="true">
                          <AgentBrandIcon kind={item.kind} size={17} />
                        </span>
                      ) : (
                        <span className="m-group-mention-avatar" aria-hidden="true">
                          <IconAt size={15} />
                        </span>
                      )}
                      <span className="m-group-mention-copy">
                        <strong>@{item.name}</strong>
                        <small>{item.desc}</small>
                      </span>
                      {item.isAll && <span className="m-group-mention-tag">全员</span>}
                    </button>
                  );
                })}
              </div>
            )}
            {voice.error && <div className="mobile-voice-status" role="alert">{voice.error}</div>}
            {voice.status === "transcribing" && <div className="mobile-voice-status" role="status">正在转写…</div>}
            <div className="m-composer m-group-composer">
              <input
                hidden
                ref={imageInputRef}
                type="file"
                accept="image/png,image/jpeg,image/gif,image/webp"
                multiple
                onChange={(event) => {
                  const chosen = Array.from(event.target.files || []);
                  if (chosen.length) appendImageFiles(chosen);
                  event.target.value = "";
                }}
              />
              <button
                type="button"
                aria-label="添加图片"
                disabled={submitting}
                onClick={() => imageInputRef.current?.click()}
              >
                <IconPlus size={20} />
              </button>
              <button
                type="button"
                aria-label="提及成员"
                aria-expanded={mentionMenuOpen}
                disabled={submitting}
                onClick={() => {
                  setMentionQuery("");
                  setMentionIndex(0);
                  setMentionMenuOpen((open) => !open);
                }}
              >
                <IconAt size={19} />
              </button>
              <MobileVoiceHold scope={groupScope} voice={voice} disabled={!voicePluginEnabled || input.length > 0}>
                <textarea
                  ref={textareaRef}
                  aria-label="群聊消息"
                  enterKeyHint="enter"
                  placeholder={voicePluginEnabled ? "发群消息，按住说话…" : "发群消息…"}
                  rows={1}
                  value={input}
                  onChange={(e) => {
                    const val = e.currentTarget.value;
                    setInput(val);
                    checkMentionTrigger(val, e.currentTarget.selectionStart);
                  }}
                  onKeyDown={handleKeyDown}
                  onPaste={handlePaste}
                  disabled={submitting}
                />
              </MobileVoiceHold>
              {voicePluginEnabled && (
                <button
                  type="button"
                  aria-label={voice.status === "recording" || voice.status === "requesting" ? "停止录音" : voice.status === "transcribing" ? "正在转写" : "语音消息"}
                  disabled={submitting || voice.status === "transcribing"}
                  onClick={voice.toggle}
                >
                  {voice.status === "recording" || voice.status === "requesting" ? <IconPlayerStop size={19} color="#ef4444" /> : <IconMicrophone size={19} />}
                </button>
              )}
              <button
                type="button"
                className="m-send"
                aria-label="发送"
                onClick={() => void send()}
                disabled={submitting || (!input.trim() && !draft.attachments.length) || !historyLoaded || loadFailed}
              >
                {submitting ? <IconLoader2 className="m-spin" size={18} /> : <IconSend size={18} />}
              </button>
            </div>
          </div>
        </footer>
      ) : (
      <footer ref={composerRef} className="group-chat-composer">
        <div className="group-chat-composer-card">
        {group.members.length > 0 && group.members.every((member) => memberStates[member.id || member.agent_id]?.muted) && (
          <Text size="xs" c="dimmed" px="sm" pt="xs" role="status">全员已禁言，消息仅保存在群聊中</Text>
        )}
        {draft.attachments.length > 0 && <div className="group-chat-image-drafts" aria-label="待发送图片">
          {draft.attachments.map((item) => <GroupChatImageDraft key={item.key} file={item.file} disabled={submitting} onRemove={() => updateDraft((current) => ({ ...current, attachments: current.attachments.filter((entry) => entry.key !== item.key) }))} />)}
        </div>}
        {mentionMenuOpen && mentionItems.length > 0 && (
          <Paper
            withBorder
            shadow="lg"
            radius="md"
            p={4}
            role="listbox"
            aria-label="群成员建议"
            className="group-chat-mention-menu"
            style={{
              position: "absolute",
              zIndex: 50,
              left: 12,
              right: 12,
              bottom: "calc(100% + 8px)",
              maxHeight: 220,
              overflowY: "auto",
              background: "var(--astr-surface, #fff)",
            }}
          >
            <Stack gap={2}>
              {mentionItems.map((item, idx) => {
                const isSelected = idx === mentionIndex;
                return (
                  <UnstyledButton
                    key={item.id}
                    p="xs"
                    style={{
                      borderRadius: 6,
                      background: isSelected
                        ? "var(--mantine-color-indigo-light)"
                        : undefined,
                    }}
                    onMouseDown={(e) => {
                      e.preventDefault();
                      selectMention(item);
                    }}
                  >
                    <Group gap="xs" wrap="nowrap">
                      {item.isAll ? (
                        <ActionIcon
                          size="sm"
                          variant="light"
                          color="indigo"
                          radius="xl"
                        >
                          <IconUsers size={14} />
                        </ActionIcon>
                      ) : item.kind ? (
                        <AgentBrandIcon kind={item.kind} size={18} />
                      ) : (
                        <IconAt size={16} />
                      )}
                      <div style={{ minWidth: 0, flex: 1 }}>
                        <Text size="sm" fw={600}>
                          @{item.name}
                        </Text>
                        <Text size="xs" c="dimmed" lineClamp={1}>
                          {item.desc}
                        </Text>
                      </div>
                      {item.isAll && (
                        <Badge size="xs" color="indigo">
                          全员
                        </Badge>
                      )}
                    </Group>
                  </UnstyledButton>
                );
              })}
            </Stack>
          </Paper>
        )}

          <Textarea
            ref={textareaRef}
            aria-label="群聊消息"
            placeholder="输入消息或粘贴图片，@ 选择成员…"
            variant="unstyled"
            classNames={{ input: "group-chat-input" }}
            minRows={2}
            maxRows={6}
            autosize
            value={input}
            onChange={(e) => {
              const val = e.currentTarget.value;
              setInput(val);
              checkMentionTrigger(val, e.currentTarget.selectionStart);
            }}
            onKeyDown={handleKeyDown}
            onPaste={handlePaste}
            disabled={submitting}
          />
          <div className="group-chat-composer-actions">
            <div className="group-chat-composer-tools">
              <button type="button" className="group-chat-mention-all" onClick={() => insertMentionDirect("all")} aria-label="在消息中提及所有成员">
                <IconAt size={16} aria-hidden="true" />所有人
              </button>
              <span className="group-chat-composer-shortcuts">Enter 发送 <span aria-hidden="true">·</span> Shift + Enter 换行</span>
            </div>
            <Button
              size="sm"
              radius="md"
              className="group-chat-send"
              color="violet"
              rightSection={<IconSend size={14} />}
              onClick={() => void send()}
              loading={submitting}
              disabled={(!input.trim() && !draft.attachments.length) || !historyLoaded || loadFailed}
            >
              发送
            </Button>
          </div>
        </div>
      </footer>
      )}
    </div>
    </GroupFileSourceScope>
  );
}
