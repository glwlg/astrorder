import { MobileWorkspace } from "./MobileWorkspace";

/**
 * Compatibility wrapper: mobile group chats now share the unified MobileWorkspace shell
 * and left sidebar drawer (with the sliding 对话 / 群聊 switcher).
 */
export function MobileGroupsPage() {
  return <MobileWorkspace />;
}
