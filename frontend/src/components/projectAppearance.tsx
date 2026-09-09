import React from 'react'
import {
  IconFolder,
  IconRocket,
  IconTools,
  IconDatabase,
  IconCloud,
  IconCode,
  IconPackage,
  IconBook,
  IconBug,
  IconFlame,
  IconBolt,
  IconStar,
  IconHeart,
  IconWorld,
  IconServer,
  IconTerminal2,
  IconBulb,
  IconShield,
  IconTarget,
  IconSettings,
  IconCoffee,
} from '@tabler/icons-react'
import { Button, Group, Modal, Stack, Text, UnstyledButton } from '@mantine/core'

export const PROJECT_APPEARANCE_COLORS: Record<string, string> = {
  red: '#e11d48',
  orange: '#ea580c',
  amber: '#d97706',
  yellow: '#ca8a04',
  lime: '#65a30d',
  green: '#16a34a',
  emerald: '#059669',
  teal: '#0d9488',
  cyan: '#0891b2',
  sky: '#0284c7',
  blue: '#2563eb',
  indigo: '#4f46e5',
  violet: '#7c3aed',
  purple: '#9333ea',
  fuchsia: '#c026d3',
  pink: '#db2777',
  rose: '#f43f5e',
  slate: '#64748b',
}

export const PROJECT_APPEARANCE_ICONS = [
  'folder',
  'rocket',
  'tools',
  'database',
  'cloud',
  'code',
  'package',
  'book',
  'bug',
  'flame',
  'zap',
  'star',
  'heart',
  'globe',
  'server',
  'terminal',
  'lightbulb',
  'shield',
  'target',
  'gear',
  'coffee',
] as const

export type ProjectAppearanceIcon = (typeof PROJECT_APPEARANCE_ICONS)[number]

export interface ProjectAppearanceEntry {
  color?: string
  icon?: string
}

export type ProjectAppearanceMap = Record<string, ProjectAppearanceEntry>

const APPEARANCE_STORAGE_KEY = 'astrorder:project_appearance'
const PINNED_PROJECTS_KEY = 'astrorder:pinned_projects'

export function loadProjectAppearance(): ProjectAppearanceMap {
  try {
    const raw = localStorage.getItem(APPEARANCE_STORAGE_KEY)
    if (raw) return JSON.parse(raw)
  } catch {}
  return {}
}

export function saveProjectAppearance(map: ProjectAppearanceMap): void {
  try {
    localStorage.setItem(APPEARANCE_STORAGE_KEY, JSON.stringify(map))
  } catch {}
}

export function loadPinnedProjects(): string[] {
  try {
    const raw = localStorage.getItem(PINNED_PROJECTS_KEY)
    if (raw) return JSON.parse(raw)
  } catch {}
  return []
}

export function savePinnedProjects(pins: string[]): void {
  try {
    localStorage.setItem(PINNED_PROJECTS_KEY, JSON.stringify(pins))
  } catch {}
}

export const ICON_COMPONENT_MAP: Record<string, React.ComponentType<{ size?: number; color?: string; style?: React.CSSProperties }>> = {
  folder: IconFolder,
  rocket: IconRocket,
  tools: IconTools,
  database: IconDatabase,
  cloud: IconCloud,
  code: IconCode,
  package: IconPackage,
  book: IconBook,
  bug: IconBug,
  flame: IconFlame,
  zap: IconBolt,
  star: IconStar,
  heart: IconHeart,
  globe: IconWorld,
  server: IconServer,
  terminal: IconTerminal2,
  lightbulb: IconBulb,
  shield: IconShield,
  target: IconTarget,
  gear: IconSettings,
  coffee: IconCoffee,
}

export function ProjectGlyph({
  iconName,
  colorName,
  size = 15,
  style,
}: {
  iconName?: string
  colorName?: string
  size?: number
  style?: React.CSSProperties
}) {
  const Comp = (iconName && ICON_COMPONENT_MAP[iconName]) || IconFolder
  const hexColor = (colorName && PROJECT_APPEARANCE_COLORS[colorName]) || undefined
  return <Comp size={size} color={hexColor} style={{ flexShrink: 0, ...style }} />
}

export function ProjectAppearanceModal({
  opened,
  onClose,
  projectKey,
  projectLabel,
  appearance,
  onSave,
}: {
  opened: boolean
  onClose: () => void
  projectKey: string
  projectLabel: string
  appearance: ProjectAppearanceMap
  onSave: (key: string, entry: ProjectAppearanceEntry) => void
}) {
  const initial = appearance[projectKey] || {}
  const [color, setColor] = React.useState<string>(initial.color || '')
  const [icon, setIcon] = React.useState<string>(initial.icon || 'folder')

  React.useEffect(() => {
    if (opened) {
      const cur = appearance[projectKey] || {}
      setColor(cur.color || '')
      setIcon(cur.icon || 'folder')
    }
  }, [opened, projectKey, appearance])

  const handleSave = () => {
    onSave(projectKey, { color, icon })
    onClose()
  }

  const handleReset = () => {
    setColor('')
    setIcon('folder')
  }

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={<Text fw={600}>自定义项目外观 - {projectLabel}</Text>}
      centered
      size="md"
    >
      <Stack gap="md">
        {/* 实时预览 */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '10px',
            padding: '10px 14px',
            borderRadius: '8px',
            border: '1px solid var(--mantine-color-default-border)',
            background: 'var(--mantine-color-default-hover)',
          }}
        >
          <ProjectGlyph iconName={icon} colorName={color} size={22} />
          <Text fw={600} size="sm">
            {projectLabel}
          </Text>
          <Text size="xs" c="dimmed" style={{ marginLeft: 'auto' }}>
            效果实时预览
          </Text>
        </div>

        {/* 颜色选择 */}
        <Stack gap="xs">
          <Text size="xs" fw={500} c="dimmed">
            选择颜色
          </Text>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '8px' }}>
            <button
              type="button"
              title="默认颜色"
              onClick={() => setColor('')}
              style={{
                width: '26px',
                height: '26px',
                borderRadius: '50%',
                border: !color ? '2px solid #228be6' : '1px solid #ccc',
                background: '#868e96',
                cursor: 'pointer',
                outline: 'none',
              }}
            />
            {Object.entries(PROJECT_APPEARANCE_COLORS).map(([cName, hex]) => (
              <button
                key={cName}
                type="button"
                title={cName}
                onClick={() => setColor(cName)}
                style={{
                  width: '26px',
                  height: '26px',
                  borderRadius: '50%',
                  border: color === cName ? '2px solid #fff' : '1px solid rgba(0,0,0,0.1)',
                  boxShadow: color === cName ? '0 0 0 2px #228be6' : 'none',
                  background: hex,
                  cursor: 'pointer',
                  outline: 'none',
                }}
              />
            ))}
          </div>
        </Stack>

        {/* 图标选择 */}
        <Stack gap="xs">
          <Text size="xs" fw={500} c="dimmed">
            选择图标
          </Text>
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(7, 1fr)',
              gap: '6px',
            }}
          >
            {PROJECT_APPEARANCE_ICONS.map((iName) => {
              const Comp = ICON_COMPONENT_MAP[iName] || IconFolder
              const isSelected = icon === iName
              return (
                <UnstyledButton
                  key={iName}
                  onClick={() => setIcon(iName)}
                  title={iName}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    height: '36px',
                    borderRadius: '6px',
                    border: isSelected ? '1px solid #228be6' : '1px solid var(--mantine-color-default-border)',
                    background: isSelected ? 'rgba(34, 139, 230, 0.12)' : 'transparent',
                    color: isSelected ? '#228be6' : 'inherit',
                    cursor: 'pointer',
                  }}
                >
                  <Comp size={18} />
                </UnstyledButton>
              )
            })}
          </div>
        </Stack>

        {/* 底部按钮 */}
        <Group justify="space-between" mt="sm">
          <Button variant="subtle" size="xs" color="gray" onClick={handleReset}>
            重置默认
          </Button>
          <Group gap="xs">
            <Button variant="default" size="xs" onClick={onClose}>
              取消
            </Button>
            <Button size="xs" onClick={handleSave}>
              保存
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  )
}
