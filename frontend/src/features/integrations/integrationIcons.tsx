import { useEffect, useState } from 'react'
import {
  IconActivity,
  IconApi,
  IconBook2,
  IconBrandGit,
  IconChartBar,
  IconCloud,
  IconCode,
  IconCpu,
  IconDatabase,
  IconFlame,
  IconGlobe,
  IconLayoutDashboard,
  IconLayersSubtract,
  IconRobot,
  IconServer,
  IconShieldCheck,
  IconSparkles,
  IconTerminal2,
} from '@tabler/icons-react'
import type { IntegrationIconType } from '../../api/client'

export interface BuiltinIntegrationIcon {
  id: string
  label: string
  component: typeof IconGlobe
}

export const BUILTIN_INTEGRATION_ICONS: BuiltinIntegrationIcon[] = [
  { id: 'globe', label: '网页', component: IconGlobe },
  { id: 'gateway', label: '网关 / API', component: IconApi },
  { id: 'dashboard', label: '控制台', component: IconLayoutDashboard },
  { id: 'chart', label: '图表统计', component: IconChartBar },
  { id: 'activity', label: '监控指标', component: IconActivity },
  { id: 'server', label: '服务器', component: IconServer },
  { id: 'database', label: '数据库', component: IconDatabase },
  { id: 'cloud', label: '云服务', component: IconCloud },
  { id: 'git', label: '代码仓库', component: IconBrandGit },
  { id: 'code', label: '开发工具', component: IconCode },
  { id: 'terminal', label: '终端', component: IconTerminal2 },
  { id: 'book', label: '文档知识', component: IconBook2 },
  { id: 'bot', label: 'AI 服务', component: IconRobot },
  { id: 'cpu', label: '算力节点', component: IconCpu },
  { id: 'shield', label: '安全认证', component: IconShieldCheck },
  { id: 'layers', label: '容器编排', component: IconLayersSubtract },
  { id: 'flame', label: '热点任务', component: IconFlame },
  { id: 'sparkles', label: '实验室', component: IconSparkles },
]

const ICON_MAP = new Map(BUILTIN_INTEGRATION_ICONS.map((entry) => [entry.id, entry.component]))

const BADGE_PALETTE = [
  '#4f46e5',
  '#2563eb',
  '#0891b2',
  '#0d9488',
  '#16a34a',
  '#ca8a04',
  '#ea580c',
  '#db2777',
  '#7c3aed',
]

function pickMonogramColor(seed: string): string {
  let hash = 0
  for (let i = 0; i < seed.length; i++) {
    hash = ((hash * 31) + seed.charCodeAt(i)) >>> 0
  }
  return BADGE_PALETTE[hash % BADGE_PALETTE.length]
}

function monogramChar(name: string, url?: string): string {
  const cleanName = (name || '').trim()
  if (cleanName) return [...cleanName][0].toUpperCase()
  try {
    const host = new URL(url || '').hostname.replace(/^www\./i, '')
    if (host) return host[0].toUpperCase()
  } catch {}
  return 'W'
}

export function IntegrationIcon({
  name,
  url,
  iconType,
  iconValue,
  size = 20,
}: {
  name: string
  url?: string
  iconType: IntegrationIconType
  iconValue: string
  size?: number
}) {
  const [imgFailed, setImgFailed] = useState(false)
  useEffect(() => {
    setImgFailed(false)
  }, [iconType, iconValue])

  if ((iconType === 'auto' || iconType === 'image') && iconValue && !imgFailed) {
    return (
      <img
        src={iconValue}
        alt=""
        aria-hidden="true"
        className="integration-icon-img"
        style={{ width: size, height: size, borderRadius: Math.max(4, Math.round(size * 0.22)), objectFit: 'contain', display: 'block', flexShrink: 0 }}
        onError={() => setImgFailed(true)}
      />
    )
  }

  if (iconType === 'builtin') {
    const Builtin = ICON_MAP.get(iconValue) || IconGlobe
    return <Builtin size={size} stroke={1.8} aria-hidden="true" />
  }

  if (iconType === 'emoji' && iconValue.trim()) {
    return (
      <span
        aria-hidden="true"
        style={{
          width: size,
          height: size,
          fontSize: Math.max(13, Math.round(size * 0.85)),
          lineHeight: 1,
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          flexShrink: 0,
        }}
      >
        {iconValue.trim()}
      </span>
    )
  }

  const ch = monogramChar(name, url)
  const bg = pickMonogramColor(name || url || ch)
  return (
    <span
      aria-hidden="true"
      className="integration-icon-monogram"
      style={{
        width: size,
        height: size,
        borderRadius: Math.max(5, Math.round(size * 0.28)),
        background: bg,
        color: '#ffffff',
        fontSize: Math.max(10, Math.round(size * 0.56)),
        fontWeight: 700,
        lineHeight: 1,
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        flexShrink: 0,
        letterSpacing: '-0.02em',
      }}
    >
      {ch}
    </span>
  )
}
