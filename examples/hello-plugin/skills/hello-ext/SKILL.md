---
name: hello-ext
description: 使用 Hello Extension 示例插件回显消息并检查当前工作区 Git 上下文。
---

# Hello Extension Skill

当用户希望验证第三方插件是否已接入 Astrorder Agent 能力总线时，可以使用以下 CLI 命令：

1. 查看当前已启用的全部插件扩展能力：
   ```bash
   astrorder-agent ext list
   ```

2. 调用 `hello-plugin` 提供的 `echo` 能力（会自动携带当前会话的工作区和连接上下文）：
   ```bash
   astrorder-agent ext call hello-plugin echo --json '{"message": "hello from agent"}'
   ```
