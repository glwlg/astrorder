export const REASONING_EFFORTS = [
  { value: 'low', label: '低' },
  { value: 'medium', label: '中' },
  { value: 'high', label: '高' },
  { value: 'xhigh', label: '超高' },
  { value: 'max', label: '最高' },
] as const

export function clipboardFiles(event: { clipboardData?: DataTransfer | null }): File[] {
  const data = event.clipboardData
  if (!data) return []
  const files = [...data.files]
  if (files.length) return files.filter(file => file.size > 0)
  const fromItems: File[] = []
  for (const item of data.items) {
    if (item.kind !== 'file') continue
    const file = item.getAsFile()
    if (file && file.size > 0) fromItems.push(file)
  }
  return fromItems
}
