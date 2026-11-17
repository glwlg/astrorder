import { Select } from "@mantine/core";
import { IconArrowLeft } from "@tabler/icons-react";
import { useNavigate, useParams } from "react-router-dom";
import { useAstrorderStore } from "../../state/store";
import { GroupsPage } from "../chat/GroupsPage";
import "./mobile.css";

export function MobileGroupsPage() {
  const navigate = useNavigate();
  const { groupId } = useParams();
  const groups = useAstrorderStore((state) => state.botGroups);
  return <div className="mobile-workspace">
    <header className="m-header">
      <div className="m-brand"><button type="button" aria-label="返回会话" onClick={() => navigate("/mobile")}><IconArrowLeft size={21} /></button><strong>群聊</strong></div>
      <Select aria-label="选择群聊" placeholder="选择群聊" value={groupId || null} data={Object.values(groups).map((group) => ({ value: group.id, label: group.name }))} onChange={(id) => { if (id) navigate(`/mobile/groups/${id}`); }} maw={220} />
    </header>
    <main style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column", overflow: "hidden" }}>
      <GroupsPage routePrefix="/mobile" />
    </main>
  </div>;
}
