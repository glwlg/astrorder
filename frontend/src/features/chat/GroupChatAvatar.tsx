import { IconUser } from "@tabler/icons-react";
import { AgentBrandIcon } from "../../components/AgentBrandIcon";

export function GroupChatAvatar({ kind, tone, user = false }: { kind?: string; tone: string; user?: boolean }) {
  return (
    <span className="group-member-avatar group-member-tone" data-tone={tone} aria-hidden="true">
      {user ? <IconUser size={19} /> : <AgentBrandIcon kind={kind} size={19} />}
    </span>
  );
}
