import { useContext, useState, type ComponentProps } from "react";
import { Badge, Button, Group, Paper, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { GroupFileLink } from "../../components/GroupFileLink";
import type { GroupFileReference } from "../../domain/types";
import { BlackboardItemRenderer } from "../monitor/BlackboardJsonRender";
import { FileSourceContext } from "../sidecar/FileSourceContext";
import "./groupArtifacts.css";

export function GroupBlackboardItem(props: ComponentProps<typeof BlackboardItemRenderer>) {
  const scope = useContext(FileSourceContext);
  const [retrying, setRetrying] = useState(false);
  if (props.itemKey !== "artifacts" || !props.namespace?.startsWith("group:")) return <BlackboardItemRenderer {...props} />;
  let value = props.itemValue;
  if (typeof value === "string") {
    try { value = JSON.parse(value); } catch { return <BlackboardItemRenderer {...props} />; }
  }
  const files = (value as { items?: GroupFileReference[] } | null)?.items;
  if (!Array.isArray(files)) return <BlackboardItemRenderer {...props} />;
  return <Paper withBorder p="sm" radius="md" aria-label="群聊产物">
    <Group justify="space-between" mb="sm">
      <Group gap="xs"><Text fw={600} size="sm">产物</Text><Badge variant="light" color="gray">{files.length}</Badge></Group>
      {scope?.recheck && <Button size="compact-xs" variant="subtle" loading={retrying || scope.checking} onClick={async () => {
        setRetrying(true);
        try { await scope.recheck!(); }
        catch (error) { notifications.show({ color: "red", message: error instanceof Error ? error.message : "重新核验失败" }); }
        finally { setRetrying(false); }
      }}>重新核验</Button>}
    </Group>
    {scope?.checking && <Text size="xs" c="dimmed" role="status" mb="xs">正在核验文件与交付记录…</Text>}
    {scope?.error && <Text size="xs" c="orange" role="status" mb="xs" lineClamp={3} title={scope.error} style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>部分文件未完成核验：{scope.error}</Text>}
    {!files.length && !scope?.checking && <Text size="xs" c="dimmed">暂无已确认的产物</Text>}
    <ul className="group-artifacts-list">
      {files.map((file) => <li key={file.id}>
        <GroupFileLink href={file.path}>{file.name}</GroupFileLink>
        <Text size="xs" c="dimmed">{file.member_name}</Text>
        <Text size="xs" c="dimmed" className="group-artifact-path">{file.path}</Text>
      </li>)}
    </ul>
  </Paper>;
}
