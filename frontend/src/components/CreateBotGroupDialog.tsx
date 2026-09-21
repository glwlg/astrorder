import { useEffect, useState } from "react";
import {
  Badge,
  Button,
  Checkbox,
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
import { api } from "../api/client";
import type { Agent, BotGroup, BotGroupMember } from "../domain/types";
import { AgentBrandIcon } from "./AgentBrandIcon";
import { REASONING_EFFORTS } from "../features/chat/composerMedia";

type MemberConfig = {
  alias: string;
  role: string;
  workspace: string;
  model: string;
  effort: string;
  approval: string;
};

const emptyConfig = (): MemberConfig => ({
  alias: "",
  role: "",
  workspace: "",
  model: "",
  effort: "",
  approval: "manual",
});

function machineLabel(agent: Agent): string {
  if (agent.connection_id) {
    return agent.connection_id.startsWith("ssh-")
      ? `SSH ${agent.connection_id.slice(4)}`
      : agent.connection_id;
  }
  return "本机";
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
  const [maxHops, setMaxHops] = useState<number>(3);
  const [selectedAgentIds, setSelectedAgentIds] = useState<Set<string>>(
    new Set(),
  );
  const [memberRoles, setMemberRoles] = useState<Record<string, MemberConfig>>(
    {},
  );
  const [modelOptions, setModelOptions] = useState<
    Record<string, Array<{ value: string; label: string }>>
  >({});
  const [busy, setBusy] = useState(false);

  const activeAgents = Object.values(agents).filter(
    (a) => a.status === "ready",
  );

  useEffect(() => {
    if (!opened) return;
    setName(group?.name || "");
    setDescription(group?.description || "");
    setMaxHops(group?.max_hops || 3);
    setSelectedAgentIds(
      new Set(group?.members.map((member) => member.agent_id) || []),
    );
    setMemberRoles(
      Object.fromEntries(
        (group?.members || []).map((member) => [
          member.agent_id,
          {
            alias: member.alias || "",
            role: member.system_role_prompt || "",
            workspace: member.workspace || "",
            model:
              member.model_provider && member.model_name
                ? JSON.stringify([member.model_provider, member.model_name])
                : "",
            effort: member.thinking_effort || "",
            approval: member.approval_policy || "manual",
          },
        ]),
      ),
    );
    for (const member of group?.members || [])
      void loadAgentDefaults(member.agent_id);
  }, [opened, group]);

  const loadAgentDefaults = async (agentId: string) => {
    try {
      const sessions = await api.getSessions(agentId);
      const session = sessions.items[0];
      if (!session) return;
      const [models, binding] = await Promise.all([
        api.getSessionModels(session.id, agentId),
        api.getSessionModel(session.id, agentId).catch(() => null),
      ]);
      setModelOptions((prev) => ({
        ...prev,
        [agentId]: models.items.map((item) => ({
          value: JSON.stringify([item.provider, item.model]),
          label: item.label,
        })),
      }));
      setMemberRoles((prev) => {
        const current = prev[agentId] || emptyConfig();
        return {
          ...prev,
          [agentId]: {
            ...current,
            workspace: current.workspace || session.workspace || "",
            model:
              current.model ||
              (binding?.provider && binding.model
                ? JSON.stringify([binding.provider, binding.model])
                : ""),
            effort: current.effort || binding?.effort || "",
          },
        };
      });
    } catch {
      // Runtime configuration remains optional when the agent has no readable session yet.
    }
  };

  const toggleSelect = (agentId: string) => {
    setSelectedAgentIds((prev) => {
      const next = new Set(prev);
      if (next.has(agentId)) {
        next.delete(agentId);
      } else {
        next.add(agentId);
        void loadAgentDefaults(agentId);
      }
      return next;
    });
  };

  const updateRole = (
    agentId: string,
    field: keyof MemberConfig,
    val: string,
  ) => {
    setMemberRoles((prev) => ({
      ...prev,
      [agentId]: { ...(prev[agentId] || emptyConfig()), [field]: val },
    }));
  };

  const submit = async () => {
    if (busy || !name.trim() || selectedAgentIds.size === 0) return;
    setBusy(true);
    try {
      const members: BotGroupMember[] = Array.from(selectedAgentIds).map(
        (aid) => {
          const ag = agents[aid];
          const config = memberRoles[aid] || emptyConfig();
          const [modelProvider, modelName] = config.model
            ? (JSON.parse(config.model) as [string, string])
            : ["", ""];
          return {
            machine_id: ag?.connection_id || "local",
            agent_id: aid,
            name: ag?.name || ag?.kind || aid,
            alias: config.alias.trim() || undefined,
            system_role_prompt: config.role.trim() || undefined,
            workspace: config.workspace.trim() || undefined,
            model_provider: modelProvider || undefined,
            model_name: modelName || undefined,
            thinking_effort: config.effort || undefined,
            approval_policy: (config.approval ||
              "manual") as BotGroupMember["approval_policy"],
          };
        },
      );
      const payload = {
        name: name.trim(),
        description: description.trim() || undefined,
        members,
        max_hops: Number(maxHops) || 3,
      };
      const res = group
        ? await api.updateBotGroup(group.id, payload)
        : await api.createBotGroup(payload);
      notifications.show({
        color: "teal",
        message: `群聊「${res.group.name}」已${group ? "更新" : "创建"}`,
      });
      onCreated(res.group);
      onClose();
    } catch (err) {
      notifications.show({
        color: "red",
        message: err instanceof Error ? err.message : "创建群聊失败",
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      opened={opened}
      onClose={() => {
        if (!busy) onClose();
      }}
      title={group ? "群聊设置" : "新建多 Agent 群聊 (Bots Group)"}
      size="lg"
      centered
      zIndex={400}
    >
      <Stack gap="md">
        <TextInput
          label="群聊名称"
          placeholder="如：全栈跨机发布组、代码审查突击队"
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
          required
          disabled={busy}
        />
        <TextInput
          label="职责说明（可选）"
          placeholder="简述该群的协作目标"
          value={description}
          onChange={(e) => setDescription(e.currentTarget.value)}
          disabled={busy}
        />
        <NumberInput
          label="单次提问最大自主接力跳数 (Max Hops)"
          description="防死循环硬限制：单次人类提问后，各 Agent 间自动接力移交的最大轮数（1~10）"
          min={1}
          max={10}
          value={maxHops}
          onChange={(val) => setMaxHops(typeof val === "number" ? val : 3)}
          disabled={busy}
        />

        <div>
          <Text size="sm" fw={600} mb={6}>
            选择群成员 Agent ({selectedAgentIds.size}/{activeAgents.length})
          </Text>
          <Stack gap={8} style={{ maxHeight: 280, overflowY: "auto" }}>
            {activeAgents.map((ag) => {
              const checked = selectedAgentIds.has(ag.id);
              const roleInfo = memberRoles[ag.id] || emptyConfig();
              return (
                <Paper
                  key={ag.id}
                  withBorder
                  p="xs"
                  radius="md"
                  style={{
                    borderColor: checked
                      ? "var(--mantine-color-indigo-6)"
                      : undefined,
                    background: checked
                      ? "color-mix(in srgb, var(--mantine-color-indigo-6) 6%, transparent)"
                      : undefined,
                  }}
                >
                  <Group justify="space-between" align="center" wrap="nowrap">
                    <Group gap="xs" wrap="nowrap">
                      <Checkbox
                        checked={checked}
                        onChange={() => toggleSelect(ag.id)}
                        disabled={busy}
                      />
                      <AgentBrandIcon kind={ag.kind} size={18} />
                      <div>
                        <Group gap={6} align="center">
                          <Text size="sm" fw={500}>
                            {ag.name || ag.id}
                          </Text>
                          <Badge size="xs" variant="outline" color="gray">
                            {machineLabel(ag)}
                          </Badge>
                        </Group>
                      </div>
                    </Group>
                  </Group>
                  {checked && (
                    <Stack gap="xs" mt={8}>
                      <Group grow gap="xs">
                        <TextInput
                          size="xs"
                          placeholder="群内别名（如：架构师）"
                          value={roleInfo.alias}
                          onChange={(e) =>
                            updateRole(ag.id, "alias", e.currentTarget.value)
                          }
                          disabled={busy}
                        />
                        <TextInput
                          size="xs"
                          placeholder="定制角色 Prompt（可选）"
                          value={roleInfo.role}
                          onChange={(e) =>
                            updateRole(ag.id, "role", e.currentTarget.value)
                          }
                          disabled={busy}
                        />
                      </Group>
                      <TextInput
                        size="xs"
                        label="工作路径"
                        placeholder="留空则沿用该 Agent 最近使用的路径"
                        value={roleInfo.workspace}
                        onChange={(e) =>
                          updateRole(ag.id, "workspace", e.currentTarget.value)
                        }
                        disabled={busy}
                      />
                      <Group grow gap="xs" align="end">
                        <Select
                          size="xs"
                          label="模型"
                          comboboxProps={{ withinPortal: true, zIndex: 500 }}
                          placeholder="运行时默认"
                          clearable
                          searchable
                          data={modelOptions[ag.id] || []}
                          value={roleInfo.model || null}
                          onDropdownOpen={() => void loadAgentDefaults(ag.id)}
                          onChange={(value) =>
                            updateRole(ag.id, "model", value || "")
                          }
                          disabled={busy}
                        />
                        <Select
                          size="xs"
                          label="思考强度"
                          comboboxProps={{ withinPortal: true, zIndex: 500 }}
                          placeholder="运行时默认"
                          clearable
                          data={REASONING_EFFORTS.map((item) => ({
                            value: item.value,
                            label: item.label,
                          }))}
                          value={roleInfo.effort || null}
                          onChange={(value) =>
                            updateRole(ag.id, "effort", value || "")
                          }
                          disabled={busy}
                        />
                        <Select
                          size="xs"
                          label="审批模式"
                          comboboxProps={{ withinPortal: true, zIndex: 500 }}
                          data={[
                            { value: "manual", label: "每次询问" },
                            { value: "auto", label: "自动审批" },
                            { value: "full_access", label: "完全访问" },
                          ]}
                          value={roleInfo.approval}
                          onChange={(value) =>
                            updateRole(ag.id, "approval", value || "manual")
                          }
                          disabled={busy}
                        />
                      </Group>
                    </Stack>
                  )}
                </Paper>
              );
            })}
          </Stack>
        </div>

        <Group justify="flex-end" mt="sm">
          <Button variant="default" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button
            onClick={() => void submit()}
            loading={busy}
            disabled={!name.trim() || selectedAgentIds.size === 0}
          >
            {group ? "保存设置" : "创建群聊"}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
