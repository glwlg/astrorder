import { useCallback, useEffect, useState } from 'react'
import { api, type OfficePersonaProfile, type OfficeWritingStyle } from '../../api/client'

export const OFFICE_PERSONA_SYNC_KEY = 'astrorder:office:persona:sync'
const OFFICE_PERSONA_CACHE_KEY = 'astrorder:office:persona:cache:v1'

export const DEFAULT_OFFICE_PERSONA: OfficePersonaProfile = {
  display_name: '',
  role: '',
  department: '',
  default_audience: '',
  writing_style: 'concise_exec',
  tone_rules: '',
  glossary: '',
  avoid_words: '',
  auto_inject_chat: true,
}

export const WRITING_STYLE_OPTIONS: Array<{
  value: OfficeWritingStyle
  label: string
  summary: string
}> = [
  { value: 'concise_exec', label: '结论先行 · 管理汇报', summary: '开篇直给结论与量化结果，简洁紧凑' },
  { value: 'formal_doc', label: '正式公文 · 严谨书面', summary: '措辞规范庄重，适合通知、立项与制度文档' },
  { value: 'friendly_sync', label: '清晰务实 · 团队协作', summary: '自然直接、重点清晰，适合跨部门同步' },
  { value: 'client_facing', label: '专业得体 · 对外商务', summary: '礼貌克制、突出交付价值，自动避开内部细节' },
]

export function writingStyleLabel(style?: string): string {
  return WRITING_STYLE_OPTIONS.find((o) => o.value === style)?.label || '结论先行 · 管理汇报'
}

export function readCachedOfficePersona(): OfficePersonaProfile {
  try {
    const raw = localStorage.getItem(OFFICE_PERSONA_CACHE_KEY)
    if (!raw) return DEFAULT_OFFICE_PERSONA
    const parsed = JSON.parse(raw) as Partial<OfficePersonaProfile>
    return { ...DEFAULT_OFFICE_PERSONA, ...parsed }
  } catch {
    return DEFAULT_OFFICE_PERSONA
  }
}

export function writeCachedOfficePersona(profile: OfficePersonaProfile): void {
  try {
    localStorage.setItem(OFFICE_PERSONA_CACHE_KEY, JSON.stringify(profile))
    localStorage.setItem(OFFICE_PERSONA_SYNC_KEY, String(Date.now()))
  } catch {
    // ignore storage errors
  }
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent('astrorder:office-persona-changed', { detail: profile }))
  }
}

export function hasConfiguredOfficePersona(p: OfficePersonaProfile): boolean {
  return Boolean(
    p.display_name.trim() ||
      p.role.trim() ||
      p.department.trim() ||
      p.default_audience.trim() ||
      p.tone_rules.trim() ||
      p.glossary.trim() ||
      p.avoid_words.trim() ||
      p.writing_style !== 'concise_exec',
  )
}

export function buildPersonaPromptBlock(p: OfficePersonaProfile): string {
  if (!p.auto_inject_chat || !hasConfiguredOfficePersona(p)) return ''
  const lines: string[] = []
  const idParts = [
    p.display_name.trim() ? `称呼：${p.display_name.trim()}` : '',
    p.department.trim() ? `部门：${p.department.trim()}` : '',
    p.role.trim() ? `岗位：${p.role.trim()}` : '',
  ].filter(Boolean)
  if (idParts.length) lines.push(`身份：${idParts.join(' / ')}`)
  if (p.default_audience.trim()) lines.push(`常见汇报对象：${p.default_audience.trim()}`)
  lines.push(`写作风格：${writingStyleLabel(p.writing_style)}`)
  if (p.tone_rules.trim()) lines.push(`表达偏好：${p.tone_rules.trim()}`)
  if (p.glossary.trim()) lines.push(`常用术语：${p.glossary.trim()}`)
  if (p.avoid_words.trim()) lines.push(`规避/脱敏词：${p.avoid_words.trim()}`)
  return `【个人办公偏好参考：${lines.join('；')}】`
}

export function useOfficePersona() {
  const [profile, setProfile] = useState<OfficePersonaProfile>(() => readCachedOfficePersona())
  const [loading, setLoading] = useState(false)

  const refresh = useCallback(async () => {
    if (typeof api.getOfficeProfile !== 'function') return
    setLoading(true)
    try {
      const next = await api.getOfficeProfile()
      const merged = { ...DEFAULT_OFFICE_PERSONA, ...next }
      setProfile(merged)
      try {
        localStorage.setItem(OFFICE_PERSONA_CACHE_KEY, JSON.stringify(merged))
      } catch {}
    } catch {
      // fall back to cached value silently
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
    const onCustom = (ev: Event) => {
      const detail = (ev as CustomEvent<OfficePersonaProfile>).detail
      if (detail) setProfile({ ...DEFAULT_OFFICE_PERSONA, ...detail })
    }
    const onStorage = (ev: StorageEvent) => {
      if (ev.key === OFFICE_PERSONA_SYNC_KEY || ev.key === OFFICE_PERSONA_CACHE_KEY) {
        setProfile(readCachedOfficePersona())
      }
    }
    window.addEventListener('astrorder:office-persona-changed', onCustom)
    window.addEventListener('storage', onStorage)
    return () => {
      window.removeEventListener('astrorder:office-persona-changed', onCustom)
      window.removeEventListener('storage', onStorage)
    }
  }, [refresh])

  const save = useCallback(async (next: OfficePersonaProfile) => {
    const saved = await api.saveOfficeProfile(next)
    const merged = { ...DEFAULT_OFFICE_PERSONA, ...saved }
    setProfile(merged)
    writeCachedOfficePersona(merged)
    return merged
  }, [])

  return { profile, loading, refresh, save }
}
