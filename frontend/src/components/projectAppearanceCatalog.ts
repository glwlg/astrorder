import type { ComponentType, CSSProperties } from 'react'
import {
  IconFolder, IconRocket, IconTools, IconDatabase, IconCloud, IconCode,
  IconPackage, IconBook, IconBug, IconFlame, IconBolt, IconStar, IconHeart,
  IconWorld, IconServer, IconTerminal2, IconBulb, IconShield, IconTarget,
  IconSettings, IconCoffee, IconCube, IconDiamond, IconPlanet, IconSparkles,
  IconAtom, IconBrain, IconCpu, IconNetwork, IconGitBranch, IconGitPullRequest,
  IconBraces, IconChartLine, IconActivity, IconLayoutDashboard, IconFlask,
  IconLeaf, IconFlower, IconPalette, IconMusic, IconCamera, IconPuzzle,
} from '@tabler/icons-react'

export interface ProjectAppearanceEntry {
  color?: string
  icon?: string
}

export type ProjectAppearanceMap = Record<string, ProjectAppearanceEntry>

export const PROJECT_APPEARANCE_COLORS: Record<string, string> = {
  red: '#e11d48', orange: '#ea580c', amber: '#d97706', yellow: '#ca8a04',
  lime: '#65a30d', green: '#16a34a', emerald: '#059669', teal: '#0d9488',
  cyan: '#0891b2', sky: '#0284c7', blue: '#2563eb', indigo: '#4f46e5',
  violet: '#7c3aed', purple: '#9333ea', fuchsia: '#c026d3', pink: '#db2777',
  rose: '#f43f5e', slate: '#64748b',
  coral: '#f9735b', salmon: '#f47279', terracotta: '#c76b50', mint: '#10b981',
  turquoise: '#06b6d4', ocean: '#0369a1', periwinkle: '#818cf8', lavender: '#a78bfa',
  grape: '#a855f7', berry: '#a21caf', mulberry: '#be185d', graphite: '#475569',
}

export const PROJECT_COLOR_LABELS: Record<string, string> = {
  red: '绯红', orange: '橙色', amber: '琥珀', yellow: '明黄', lime: '青柠',
  green: '草绿', emerald: '翡翠', teal: '青绿', cyan: '湖蓝', sky: '天蓝',
  blue: '钴蓝', indigo: '靛蓝', violet: '紫罗兰', purple: '紫色',
  fuchsia: '品红', pink: '玫红', rose: '蔷薇', slate: '石板灰',
  coral: '珊瑚', salmon: '樱粉', terracotta: '陶红', mint: '薄荷',
  turquoise: '碧青', ocean: '深海', periwinkle: '长春花', lavender: '薰衣草',
  grape: '葡萄', berry: '莓紫', mulberry: '桑葚', graphite: '石墨',
}

const LINE_ICONS = [
  ['folder', '文件夹', IconFolder], ['rocket', '火箭', IconRocket],
  ['tools', '工具', IconTools], ['database', '数据库', IconDatabase],
  ['cloud', '云端', IconCloud], ['code', '代码', IconCode],
  ['package', '软件包', IconPackage], ['book', '书籍', IconBook],
  ['bug', '调试', IconBug], ['flame', '火焰', IconFlame],
  ['zap', '闪电', IconBolt], ['star', '星星', IconStar],
  ['heart', '爱心', IconHeart], ['globe', '地球', IconWorld],
  ['server', '服务器', IconServer], ['terminal', '终端', IconTerminal2],
  ['lightbulb', '灵感', IconBulb], ['shield', '盾牌', IconShield],
  ['target', '目标', IconTarget], ['gear', '设置', IconSettings],
  ['coffee', '咖啡', IconCoffee], ['cube', '方块', IconCube],
  ['diamond', '钻石', IconDiamond], ['planet', '行星', IconPlanet],
  ['sparkles', '星光', IconSparkles], ['atom', '原子', IconAtom],
  ['brain', '大脑', IconBrain], ['cpu', '芯片', IconCpu],
  ['network', '网络', IconNetwork], ['git-branch', '分支', IconGitBranch],
  ['git-pull-request', '合并', IconGitPullRequest], ['braces', '编程', IconBraces],
  ['chart', '图表', IconChartLine], ['activity', '监测', IconActivity],
  ['dashboard', '仪表盘', IconLayoutDashboard], ['flask', '实验', IconFlask],
  ['leaf', '叶片', IconLeaf], ['flower', '花朵', IconFlower],
  ['palette', '调色板', IconPalette], ['music', '音乐', IconMusic],
  ['camera', '相机', IconCamera], ['puzzle', '拼图', IconPuzzle],
] as const

export type ProjectAppearanceIcon = (typeof LINE_ICONS)[number][0]
export const PROJECT_APPEARANCE_ICONS = LINE_ICONS.map(([id]) => id)
export const PROJECT_ICON_LABELS: Record<string, string> = Object.fromEntries(LINE_ICONS.map(([id, label]) => [id, label]))
export const ICON_COMPONENT_MAP: Record<string, ComponentType<{ size?: number; color?: string; style?: CSSProperties }>> =
  Object.fromEntries(LINE_ICONS.map(([id, , component]) => [id, component]))

export const PROJECT_ART_ICONS: Record<string, { label: string; src: string; color: string }> = {
  'glass-orbit': { label: '星环', src: '/project-icons/orbit.webp', color: 'blue' },
  'glass-prism': { label: '紫晶', src: '/project-icons/prism.webp', color: 'violet' },
  'glass-rocket': { label: '启航', src: '/project-icons/rocket.webp', color: 'red' },
  'glass-layers': { label: '叠光', src: '/project-icons/layers.webp', color: 'emerald' },
  'glass-cube': { label: '冰方', src: '/project-icons/cube.webp', color: 'cyan' },
  'glass-flower': { label: '绽放', src: '/project-icons/flower.webp', color: 'fuchsia' },
}

export function projectAppearanceColor(appearance?: ProjectAppearanceEntry): string | undefined {
  const color = (appearance?.icon && PROJECT_ART_ICONS[appearance.icon]?.color) || appearance?.color
  return color ? PROJECT_APPEARANCE_COLORS[color] || color : undefined
}
