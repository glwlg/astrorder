import { useState } from "react";
import { IconPlayerPause, IconPlayerPlay } from "@tabler/icons-react";
import "./groupTaskIndicator.css";

export function GroupTaskIndicator() {
  const [paused, setPaused] = useState(false);
  return <span className="group-task-indicator" data-paused={paused || undefined}>
    <span className="group-task-dots" aria-hidden="true"><i /><i /><i /></span>
    <button type="button" className="group-task-motion-toggle" aria-label={paused ? "播放群聊任务动效" : "暂停群聊任务动效"} onClick={() => setPaused((value) => !value)}>
      {paused ? <IconPlayerPlay size={10} /> : <IconPlayerPause size={10} />}
    </button>
  </span>;
}
