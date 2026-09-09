export function connectionNotice(text: string): string {
  if (text.startsWith('Astrorder-owned TUI sessions use documented native prompt.submit;')) return '星序接管的终端会话通过原生接口提交消息；其他运行环境依赖宿主的消息注入接口。此连接器未提供附件、停止、排队、审批与消息记录能力。'
  return text.replaceAll('原生 app-server 连接', '原生应用服务连接')
}
