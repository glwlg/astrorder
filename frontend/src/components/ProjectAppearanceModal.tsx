import { useEffect, useRef, useState, type CSSProperties } from 'react'
import { Button, Group, Modal, Stack, Text } from '@mantine/core'
import { IconCheck, IconColorSwatch } from '@tabler/icons-react'
import { ProjectGlyph } from './ProjectGlyph'
import {
  PROJECT_APPEARANCE_COLORS, PROJECT_APPEARANCE_ICONS, PROJECT_ART_ICONS,
  PROJECT_COLOR_LABELS, PROJECT_ICON_LABELS,
  type ProjectAppearanceEntry, type ProjectAppearanceMap,
} from './projectAppearanceCatalog'
import './projectAppearance.css'

type AppearanceModalProps = {
  opened: boolean
  onClose: () => void
  projectKey: string
  projectLabel: string
  appearance: ProjectAppearanceMap
  onSave: (key: string, entry: ProjectAppearanceEntry) => Promise<boolean>
}

export function ProjectAppearanceModal(props: AppearanceModalProps) {
  return props.opened ? <AppearanceEditor key={props.projectKey} {...props} /> : null
}

function AppearanceEditor({ opened, onClose, projectKey, projectLabel, appearance, onSave }: AppearanceModalProps) {
  const [draft, setDraft] = useState<ProjectAppearanceEntry>(() => appearance[projectKey] || { color: '', icon: 'folder' })
  const [saving, setSaving] = useState(false)
  const dirty = useRef(false)
  const icon = draft.icon || 'folder'
  const color = draft.color || ''
  const art = PROJECT_ART_ICONS[icon]

  useEffect(() => {
    // Initial loading may fill untouched fields; polling must not replace edits.
    if (!dirty.current) setDraft(appearance[projectKey] || { color: '', icon: 'folder' })
  }, [appearance, projectKey])

  const edit = (patch: ProjectAppearanceEntry) => {
    dirty.current = true
    setDraft(current => ({ ...current, ...patch }))
  }
  const handleSave = async () => {
    if (saving) return
    setSaving(true)
    try {
      if (await onSave(projectKey, { color: art?.color || color, icon })) onClose()
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      opened={opened}
      onClose={() => { if (!saving) onClose() }}
      title={<Text fw={600}>项目外观</Text>}
      centered size="md"
      closeOnClickOutside={!saving}
      closeOnEscape={!saving}
      withCloseButton={!saving}
    >
      <form onSubmit={event => { event.preventDefault(); void handleSave() }}>
        <Stack gap="md">
          <div className="project-appearance-preview">
            <ProjectGlyph iconName={icon} colorName={color} size={32} />
            <span title={projectLabel}>{projectLabel}</span>
            {art && <small className="project-appearance-badge">自带配色</small>}
          </div>
          <fieldset disabled={saving} className="project-appearance-fields">
            <section className="project-appearance-section" aria-label="拟态图标">
              <h3 className="project-appearance-label">拟态图标</h3>
              <div className="project-appearance-icons project-appearance-art">
                {Object.entries(PROJECT_ART_ICONS).map(([id, item]) => (
                  <button
                    key={id} type="button" className="project-appearance-option"
                    aria-label={`图标：${item.label}`} title={item.label}
                    aria-pressed={icon === id}
                    data-autofocus={icon === id || undefined}
                    onClick={() => edit({ icon: id })}
                  >
                    <ProjectGlyph iconName={id} size={36} />
                    <span>{item.label}</span>
                  </button>
                ))}
              </div>
            </section>
            <section className="project-appearance-section" aria-label="线性图标">
              <h3 className="project-appearance-label">线性图标</h3>
              <div className="project-appearance-icons">
                {PROJECT_APPEARANCE_ICONS.map(id => (
                  <button
                    key={id} type="button" className="project-appearance-option"
                    aria-label={`图标：${PROJECT_ICON_LABELS[id]}`} title={PROJECT_ICON_LABELS[id]}
                    aria-pressed={icon === id}
                    data-autofocus={icon === id || undefined}
                    onClick={() => edit({ icon: id })}
                  >
                    <ProjectGlyph iconName={id} colorName={color} size={19} />
                  </button>
                ))}
              </div>
            </section>
            {!art && (
              <section className="project-appearance-section" aria-label="图标颜色">
                <h3 className="project-appearance-label">图标颜色</h3>
                <div className="project-appearance-colors">
                  <button
                    type="button" className="project-appearance-swatch is-default"
                    aria-label="颜色：默认" title="默认颜色" aria-pressed={!color}
                    onClick={() => edit({ color: '' })}
                  >
                    {color ? <IconColorSwatch size={14} /> : <IconCheck size={14} />}
                  </button>
                  {Object.entries(PROJECT_APPEARANCE_COLORS).map(([id, hex]) => (
                    <button
                      key={id} type="button" className="project-appearance-swatch"
                      aria-label={`颜色：${PROJECT_COLOR_LABELS[id]}`} title={PROJECT_COLOR_LABELS[id]}
                      aria-pressed={color === id}
                      onClick={() => edit({ color: id })}
                      style={{ '--project-swatch-color': hex } as CSSProperties}
                    >
                      {color === id && <IconCheck size={14} />}
                    </button>
                  ))}
                </div>
              </section>
            )}
          </fieldset>
          <Group justify="space-between" mt="xs">
            <Button type="button" variant="subtle" size="xs" color="gray" disabled={saving} onClick={() => edit({ icon: 'folder', color: '' })}>
              重置默认
            </Button>
            <Group gap="xs">
              <Button type="button" variant="default" size="xs" disabled={saving} onClick={onClose}>取消</Button>
              <Button type="submit" size="xs" loading={saving}>保存</Button>
            </Group>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
