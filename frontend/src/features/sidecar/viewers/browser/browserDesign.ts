import { request, type BrowserSnapshot } from '../../../../api/client'
import { newCommandId, scopeKey } from '../../../../domain/semantics'
import { useAstrorderStore } from '../../../../state/store'
import { draftStorage } from '../../../chat/draftStorage'
import { designPreviewFile } from './browserDesignScreenshot'

export const designProperties = {
  'font-size': '字号', width: '宽度', height: '高度', 'border-radius': '圆角',
  gap: '元素间距', 'padding-top': '上内边距', 'padding-right': '右内边距',
  'padding-bottom': '下内边距', 'padding-left': '左内边距',
  'margin-top': '上外边距', 'margin-right': '右外边距',
  'margin-bottom': '下外边距', 'margin-left': '左外边距',
  color: '文字颜色', 'background-color': '背景颜色',
} as const

export type DesignProperty = keyof typeof designProperties
export interface DesignElement {
  id: string
  selector: string
  tag: string
  text: string
  classes: string[]
  rect: { x: number; y: number; width: number; height: number }
  styles: Record<DesignProperty, string>
  has_parent: boolean
}
export interface DesignChange {
  element_id: string
  selector: string
  tag: string
  text: string
  classes: string[]
  properties: Array<{ property: DesignProperty; before: string; after: string }>
}
export interface BrowserDesignState {
  document_id: string
  url: string
  title: string
  viewport: { width: number; height: number }
  selected: DesignElement | null
  changes: DesignChange[]
  can_undo: boolean
}
export interface BrowserDesignResponse { state: BrowserDesignState; snapshot: BrowserSnapshot }
export interface BrowserDesignCommand {
  action: 'state' | 'select' | 'parent' | 'apply' | 'undo' | 'reset' | 'export'
  document_id?: string
  element_id?: string
  ratio_x?: number
  ratio_y?: number
  property?: DesignProperty
  value?: string
}

export function browserDesign(agentId: string, sessionId: string, targetId: string, command: BrowserDesignCommand, signal?: AbortSignal) {
  return request<BrowserDesignResponse>('/browser/design', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ agent_id: agentId, session_id: sessionId, target_id: targetId, ...command }),
    signal,
  })
}

export function designTaskText(state: BrowserDesignState, note: string) {
  const edits = state.changes.map(change => {
    const label = change.text ? `“${change.text}”` : '选中区域'
    return `${label}：${change.properties.map(edit => `${designProperties[edit.property]} ${edit.before} → ${edit.after}`).join('；')}`
  }).join('\n')
  return [note.trim(), edits].filter(Boolean).join('\n\n')
}

export async function addDesignToDraft(agentId: string, sessionId: string, response: BrowserDesignResponse, note: string) {
  const key = scopeKey(agentId, sessionId)
  const [saved, file] = await Promise.all([draftStorage.load(key), designPreviewFile(response)])
  // Read after awaiting storage so concurrent typing and attachments survive.
  const store = useAstrorderStore.getState()
  const current = store.drafts[key] || saved || { text: '', attachments: [], sessionRefs: [] }
  const id = newCommandId()
  const text = designTaskText(response.state, note)
  const next = {
    ...current,
    text: [current.text, text].filter(Boolean).join('\n\n'),
    attachments: [...current.attachments, { key: `browser-design-${id}`, file }],
  }
  store.setDraft(agentId, sessionId, next)
  await draftStorage.save(key, next)
}

export function cssColorToHex(value: string): string | null {
  if (/^#[0-9a-f]{6}$/i.test(value)) return value
  const components = value.match(/^rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)(?:\s*[,/]\s*([\d.]+))?\s*\)$/)
  if (!components || (components[4] !== undefined && Number(components[4]) !== 1)) return null
  return '#' + components.slice(1, 4).map(channel => Number(channel).toString(16).padStart(2, '0')).join('')
}
