import { useEffect, useRef, useState } from "react";
import {
  ActionIcon,
  Alert,
  Autocomplete,
  Badge,
  Button,
  Group,
  Modal,
  NumberInput,
  Paper,
  Select,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import { api, ApiError } from "../api/client";
import { scopeKey } from "../domain/semantics";
import type { Agent, BotGroup, BotGroupMember, Session } from "../domain/types";
import { useAstrorderStore } from "../state/store";
import { AgentBrandIcon } from "./AgentBrandIcon";
import { REASONING_EFFORTS } from "../features/chat/composerMedia";

type MemberDraft = {
  key: string;
  id?: string;
  agentId: string;
  machineId: string;
  name: string;
  alias: string;
  role: string;
  workspace: string;
  model: string;
  effort: string;
  approval: string;
  sourceSessionId: string;
  forkSessionId: string;
  locked: boolean;
  forkUncertain: boolean;
};

type AgentOptions = {
  sessions: Session[];
  models: Array<{ value: string; label: string }>;
  model: string;
  effort: string;
  error?: string;
};

function machineLabel(connection: string): string {
  if (!connection || connection === "local") return "本机";
  return connection.startsWith("ssh-") ? `SSH ${connection.slice(4)}` : connection;
}

function memberDraft(member: BotGroupMember, group: BotGroup): MemberDraft {
  const key = member.id || member.agent_id;
  return {
    key,
    id: member.id,
    agentId: member.agent_id,
    machineId: member.machine_id,
    name: member.name || member.agent_id,
    alias: member.alias || "",
    role: member.system_role_prompt || "",
    workspace: member.workspace || "",
    model: member.model_provider && member.model_name
      ? JSON.stringify([member.model_provider, member.model_name]) : "",
    effort: member.thinking_effort || "",
    approval: member.approval_policy || "manual",
    sourceSessionId: member.source_session_id || "",
    forkSessionId: member.fork_session_id || "",
    locked: Boolean(group.member_states?.[key]?.session_id || member.fork_session_id),
    forkUncertain: false,
  };
}

function uniqueAlias(agent: Agent, members: MemberDraft[]): string {
  const base = (agent.name || agent.kind || "Agent").trim().replace(/[\s@]+/g, "-") || "Agent";
  const used = new Set(members.map((member) => member.alias.trim().toLowerCase()));
  let suffix = 1;
  while (used.has(`${base}-${suffix}`.toLowerCase())) suffix++;
  return `${base}-${suffix}`;
}

export function CreateBotGroupDialog({
  agents,
  opened,
  onClose,
  onCreated,
  group,
}: {
  agents: Record<string, Agent>;
  opened: boolean;
  onClose: () => void;
  onCreated: (group: BotGroup) => void;
  group?: BotGroup | null;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [maxHops, setMaxHops] = useState<number>(10);
  const [members, setMembers] = useState<MemberDraft[]>([]);
  const [agentOptions, setAgentOptions] = useState<Record<string, AgentOptions>>({});
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState("");
  const forked = useRef<Record<string, string>>({});
  const projects = useAstrorderStore((state) => state.projects);
  const activeAgents = Object.values(agents).filter(
    (agent) => agent.status === "ready" && agent.kind !== "antigravity" && agent.kind !== "antigravity-ssh",
  );

  useEffect(() => {
    if (!opened) return;
    setName(group?.name || "");
    setDescription(group?.description || "");
    setMaxHops(group?.max_hops ?? 10);
    setMembers(group ? group.members.map((member) => memberDraft(member, group)) : []);
    setAgentOptions({});
    setFormError("");
    forked.current = {};
    for (const agentId of new Set(group?.members.map((member) => member.agent_id) || [])) {
      void loadAgentOptions(agentId);
    }
  }, [opened, group]);

  const loadAgentOptions = async (agentId: string, newMemberKey?: string) => {
    try {
      const { items } = await api.getSessions(agentId);
      const connection = agents[agentId]?.connection_id || "local";
      const session = items.find((item) => (item.connection_id || "local") === connection);
      let models: AgentOptions["models"] = [];
      let model = "";
      let effort = "";
      let error: string | undefined;
      if (session) {
        const results = await Promise.allSettled([
          api.getSessionModels(session.id, agentId),
          api.getSessionModel(session.id, agentId),
        ]);
        if (results[0].status === "fulfilled") {
          models = results[0].value.items.map((item) => ({
            value: JSON.stringify([item.provider, item.model]), label: item.label,
          }));
        } else {
          error = "模型列表加载失败";
        }
        if (results[1].status === "fulfilled") {
          const binding = results[1].value;
          if (binding.provider && binding.model) model = JSON.stringify([binding.provider, binding.model]);
          effort = binding.effort || "";
        } else {
          error = error ? `${error}；当前模型加载失败` : "当前模型加载失败";
        }
      }
      setAgentOptions((prev) => ({ ...prev, [agentId]: { sessions: items, models, model, effort, error } }));
      if (newMemberKey && session) {
        setMembers((prev) => prev.map((member) => member.key === newMemberKey && !member.sourceSessionId
          ? { ...member, workspace: member.workspace || session.workspace || "", model: member.model || model, effort: member.effort || effort }
          : member));
      }
    } catch (err) {
      setAgentOptions((prev) => ({
        ...prev,
        [agentId]: { sessions: [], models: [], model: "", effort: "", error: err instanceof Error ? err.message : "会话列表加载失败" },
      }));
    }
  };

  const addMember = (agent: Agent) => {
    const id = `member-${Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, "0")).join("")}`;
    const known = agentOptions[agent.id];
    const connection = agent.connection_id || "local";
    setMembers((prev) => [...prev, {
      key: id, id, agentId: agent.id, machineId: connection,
      name: agent.name || agent.kind || agent.id, alias: uniqueAlias(agent, prev),
      role: "", workspace: known?.sessions.find((session) => (session.connection_id || "local") === connection)?.workspace || "", model: known?.model || "",
      effort: known?.effort || "", approval: "manual", sourceSessionId: "", forkSessionId: "",
      locked: false, forkUncertain: false,
    }]);
    if (!known) void loadAgentOptions(agent.id, id);
  };

  const updateMember = (key: string, patch: Partial<MemberDraft>) => {
    setMembers((prev) => prev.map((member) => member.key === key ? { ...member, ...patch } : member));
    setFormError("");
  };

  const submit = async () => {
    if (busy || !name.trim() || members.length === 0) return;
    const aliases = new Set<string>();
    for (const member of members) {
      const alias = member.alias.trim();
      const key = alias.toLowerCase();
      // Existing legacy members keep their original alias until the user changes it.
      const original = group?.members.find((item) => (item.id || item.agent_id) === member.key);
      const legacy = original && !original.id && (original.alias || "").trim() === alias;
      if (member.forkUncertain) {
        setFormError("分叉结果未确认，请先检查会话列表，再移除并重新添加该成员。");
        return;
      }
      if ((!alias && members.filter((item) => item.agentId === member.agentId).length > 1) ||
          (!legacy && (!alias || /[\s@]/u.test(alias) || key === "all" || key === "所有人" || aliases.has(key)))) {
        setFormError("请为每个新成员设置不重复的别名，别名不能包含空格或 @。");
        return;
      }
      if (alias) aliases.add(key);
    }
    setFormError("");
    setBusy(true);
    let hasCreatedFork = false;
    try {
      const payloadMembers: BotGroupMember[] = [];
      for (const member of members) {
        let child = member.forkSessionId || forked.current[member.key];
        if (member.sourceSessionId && !child) {
          try {
            const result = await api.forkSession(member.sourceSessionId, {
              agent_id: member.agentId, title: `${name.trim()} · ${member.alias.trim()}`, worktree: false,
            });
            if (!result.id || result.id === member.sourceSessionId || result.agent_id !== member.agentId) {
              throw new Error("分叉会话身份未确认");
            }
            child = result.id;
            forked.current[member.key] = child;
            hasCreatedFork = true;
            updateMember(member.key, { forkSessionId: child, locked: true });
            useAstrorderStore.setState((state) => ({
              sessions: { ...state.sessions, [scopeKey(result.agent_id, result.id)]: result },
            }));
          } catch (err) {
            if (!(err instanceof ApiError) || err.status >= 500) {
              updateMember(member.key, { forkUncertain: true });
              throw new Error(`${err instanceof Error ? err.message : "分叉失败"}。分叉结果未确认，请检查会话列表后再操作。`);
            }
            throw err;
          }
        }
        const [provider, model] = member.model ? JSON.parse(member.model) as [string, string] : ["", ""];
        payloadMembers.push({
          id: member.id, machine_id: member.machineId, agent_id: member.agentId, name: member.name,
          alias: member.alias.trim() || undefined, system_role_prompt: member.role.trim() || undefined,
          workspace: member.workspace.trim() || undefined, model_provider: provider || undefined,
          model_name: model || undefined, thinking_effort: member.effort || undefined,
          approval_policy: member.approval as BotGroupMember["approval_policy"],
          source_session_id: member.sourceSessionId || undefined, fork_session_id: child || undefined,
        });
      }
      const limitOnly = group && name.trim() === group.name.trim() &&
        description.trim() === (group.description || "").trim() &&
        JSON.stringify(members) === JSON.stringify(group.members.map((member) => memberDraft(member, group)));
      const payload = { name: name.trim(), description: description.trim() || "", members: payloadMembers, max_hops: maxHops };
      const res = group ? await api.updateBotGroup(group.id, limitOnly ? { max_hops: maxHops } : payload) : await api.createBotGroup(payload);
      notifications.show({ color: "teal", message: `群聊「${res.group.name}」已${group ? "更新" : "创建"}` });
      onCreated(res.group);
      onClose();
    } catch (err) {
      const message = err instanceof Error ? err.message : "保存群聊失败";
      setFormError(`${message}${hasCreatedFork || Object.keys(forked.current).length ? " 已创建的分叉会话会保留，重试时继续使用。" : ""}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal opened={opened} onClose={() => { if (!busy) onClose(); }}
      title={group ? "群聊设置" : "新建多 Agent 群聊"} size="lg" centered zIndex={400}>
      <Stack gap="md">
        <TextInput label="群聊名称" placeholder="如：全栈跨机发布组" value={name}
          onChange={(event) => setName(event.currentTarget.value)} required disabled={busy} />
        <TextInput label="职责说明（可选）" placeholder="简述该群的协作目标" value={description}
          onChange={(event) => setDescription(event.currentTarget.value)} disabled={busy} />
        <NumberInput label="单次提问最大接力轮数" description="默认 10 次，可设置 1～20 次；暂停后提高上限，再点「重新判断」继续。"
          min={1} max={20} allowDecimal={false} value={maxHops} onChange={(value) => setMaxHops(typeof value === "number" ? value : 10)} disabled={busy} />
        <div>
          <Text size="sm" fw={600} mb={6}>添加 Agent</Text>
          <Stack gap={6} style={{ maxHeight: 180, overflowY: "auto" }}>
            {activeAgents.length === 0 && <Text size="sm" c="dimmed">没有可用的 Agent</Text>}
            {activeAgents.map((agent) => (
              <Paper key={agent.id} withBorder p="xs" radius="md">
                <Group justify="space-between" wrap="nowrap">
                  <Group gap="xs" wrap="nowrap">
                    <AgentBrandIcon kind={agent.kind} size={18} />
                    <Text size="sm" fw={500}>{agent.name || agent.id}</Text>
                    <Badge size="xs" variant="outline" color="gray">{machineLabel(agent.connection_id || "local")}</Badge>
                  </Group>
                  <ActionIcon variant="light" aria-label={`添加 ${agent.name || agent.id}`} onClick={() => addMember(agent)} disabled={busy || members.length >= 16}>
                    <IconPlus size={16} />
                  </ActionIcon>
                </Group>
              </Paper>
            ))}
          </Stack>
        </div>
        <div>
          <Text size="sm" fw={600} mb={6}>群成员（{members.length}/16）</Text>
          <Stack gap="sm">
            {members.length === 0 && <Text size="sm" c="dimmed">点击 + 添加群成员</Text>}
            {members.map((member) => {
              const agent = agents[member.agentId];
              const options = agentOptions[member.agentId];
              const forkSupported = agent?.kind === "codex" || agent?.kind === "hermes" || agent?.kind === "antigravity";
              const candidates = (options?.sessions || []).filter(
                (session) => (session.connection_id || "local") === member.machineId,
              );
              const sources = candidates.map((session) => ({
                value: session.id,
                label: `${session.title || session.id} · ${session.workspace || "未设置工作区"}`,
                disabled: !session.workspace?.trim(),
              }));
              if (member.sourceSessionId && !sources.some((item) => item.value === member.sourceSessionId)) {
                sources.push({ value: member.sourceSessionId, label: member.sourceSessionId, disabled: false });
              }
              const workspaces = Array.from(new Set([
                ...candidates.map((session) => session.workspace),
                ...Object.values(projects).filter((project) => (project.connection_id || "local") === member.machineId).map((project) => project.workspace),
              ].filter((value): value is string => Boolean(value?.trim()))));
              const models = options?.models || [];
              const selectedModel = member.model && !models.some((item) => item.value === member.model)
                ? [{ value: member.model, label: (JSON.parse(member.model) as string[]).join(" / ") }, ...models] : models;
              return (
                <Paper key={member.key} withBorder p="sm" radius="md">
                  <Group justify="space-between" wrap="nowrap" mb="xs">
                    <Group gap="xs" wrap="nowrap">
                      {agent && <AgentBrandIcon kind={agent.kind} size={18} />}
                      <Text size="sm" fw={600}>{member.name}</Text>
                      <Badge size="xs" variant="outline" color="gray">{machineLabel(member.machineId)}</Badge>
                      {!agent && <Badge size="xs" color="gray">离线</Badge>}
                    </Group>
                    <ActionIcon variant="subtle" color="red" aria-label={`移除 ${member.alias || member.name}`}
                      disabled={busy} onClick={() => setMembers((prev) => prev.filter((item) => item.key !== member.key))}>
                      <IconTrash size={16} />
                    </ActionIcon>
                  </Group>
                  <Stack gap="xs">
                    <Group grow gap="xs">
                      <TextInput size="xs" label="群内别名" placeholder="用于 @ 成员" value={member.alias}
                        onChange={(event) => updateMember(member.key, { alias: event.currentTarget.value })} disabled={busy} />
                      <TextInput size="xs" label="角色职责（可选）" value={member.role}
                        onChange={(event) => updateMember(member.key, { role: event.currentTarget.value })} disabled={busy} />
                    </Group>
                    {(forkSupported || member.sourceSessionId) && (
                      <Select size="xs" label="从已有会话分叉（可选）" placeholder="新建独立会话" clearable searchable
                        comboboxProps={{ withinPortal: true, zIndex: 500 }} data={sources} value={member.sourceSessionId || null}
                        onChange={(value) => {
                          const session = candidates.find((item) => item.id === value);
                          updateMember(member.key, { sourceSessionId: value || "", workspace: session?.workspace || member.workspace });
                        }} disabled={busy || member.locked || member.forkUncertain} />
                    )}
                    {member.sourceSessionId && <Text size="xs" c="dimmed">继承来源工作区，会话记录独立；文件仍在同一工作区。</Text>}
                    <Autocomplete size="xs" label="工作路径" placeholder="输入或选择工作路径" data={workspaces}
                      comboboxProps={{ withinPortal: true, zIndex: 500 }} value={member.workspace}
                      onChange={(value) => updateMember(member.key, { workspace: value })}
                      disabled={busy || member.locked || Boolean(member.sourceSessionId)} />
                    {member.locked && <Text size="xs" c="dimmed">已有会话的成员不能更换工作区或来源；需要变更时请移除后重新添加。</Text>}
                    {member.forkUncertain && <Text size="xs" c="red">分叉结果未确认，请检查会话列表，再移除并重新添加该成员。</Text>}
                    {options?.error && <Text size="xs" c="red">{options.error}</Text>}
                    <Group grow gap="xs" align="end">
                      <Select size="xs" label="模型" comboboxProps={{ withinPortal: true, zIndex: 500 }} placeholder="运行时默认" clearable searchable
                        data={selectedModel} value={member.model || null} onChange={(value) => updateMember(member.key, { model: value || "" })} disabled={busy} />
                      <Select size="xs" label="思考强度" comboboxProps={{ withinPortal: true, zIndex: 500 }} placeholder="运行时默认" clearable
                        data={REASONING_EFFORTS.map((item) => ({ value: item.value, label: item.label }))} value={member.effort || null}
                        onChange={(value) => updateMember(member.key, { effort: value || "" })} disabled={busy || agent?.kind === "antigravity"} />
                      <Select size="xs" label="审批模式" comboboxProps={{ withinPortal: true, zIndex: 500 }}
                        data={agent?.kind === "antigravity"
                          ? [{ value: "manual", label: "每次询问" }, { value: "full_access", label: "完全访问" }]
                          : [{ value: "manual", label: "每次询问" }, { value: "auto", label: "自动审批" }, { value: "full_access", label: "完全访问" }]}
                        value={agent?.kind === "antigravity" && member.approval === "auto" ? "manual" : member.approval}
                        onChange={(value) => updateMember(member.key, { approval: value || "manual" })} disabled={busy} />
                    </Group>
                  </Stack>
                </Paper>
              );
            })}
          </Stack>
        </div>
        {formError && <Alert color="red" role="alert">{formError}</Alert>}
        <Group justify="flex-end" mt="sm">
          <Button variant="default" onClick={onClose} disabled={busy}>取消</Button>
          <Button onClick={() => void submit()} loading={busy} disabled={!name.trim() || members.length === 0}>
            {group ? "保存设置" : "创建群聊"}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
