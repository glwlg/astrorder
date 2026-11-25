import { useEffect, useState } from 'react'
import { Modal, Text } from '@mantine/core'
import { IconBriefcase, IconCheck, IconCode } from '@tabler/icons-react'
import { BrandMark } from '../../components/BrandMark'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { hasExplicitAppProfile, setAppProfile, type AppProfile } from './appProfile'

interface FirstLaunchProfileModalProps {
  /** Whether backend connection/bootstrap has settled enough to know existing projects/sessions. */
  ready: boolean
  hasExistingData: boolean
  /** Override for tests or explicit preview */
  forceOpen?: boolean
  onSelected?: (profile: AppProfile) => void
}

export function FirstLaunchProfileModal({
  ready,
  hasExistingData,
  forceOpen = false,
  onSelected,
}: FirstLaunchProfileModalProps) {
  const hydrated = usePluginSettingsStore((s) => s.hydrated)
  const [opened, setOpened] = useState(forceOpen)
  const [selected, setSelected] = useState<AppProfile>('office')

  useEffect(() => {
    if (forceOpen) {
      setOpened(true)
      return
    }
    if (!hydrated || !ready) return
    if (hasExplicitAppProfile()) {
      setOpened(false)
      return
    }
    if (hasExistingData) {
      // Existing local environments silently remain in developer mode without interrupting the user.
      setAppProfile('developer')
      setOpened(false)
      return
    }
    // Fresh environment: skip blocking modal during automated Vitest runs unless forceOpen.
    if (import.meta.env.MODE === 'test') {
      return
    }
    setOpened(true)
  }, [forceOpen, hasExistingData, hydrated, ready])

  const handleConfirm = () => {
    setAppProfile(selected)
    setOpened(false)
    onSelected?.(selected)
  }

  return (
    <Modal
      opened={opened}
      onClose={handleConfirm}
      closeOnClickOutside={false}
      closeOnEscape={false}
      withCloseButton={false}
      centered
      size={560}
      radius="lg"
      padding={28}
      overlayProps={{ opacity: 0.35, blur: 4 }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', textAlign: 'center', gap: 8 }}>
          <BrandMark size={36} />
          <div style={{ fontSize: 20, fontWeight: 650, letterSpacing: '-0.02em', color: 'var(--astr-text, #111827)' }}>
            欢迎使用星序 · 选择你的工作模式
          </div>
          <Text size="xs" c="dimmed" maw={420}>
            根据你的日常工作场景定制界面。之后随时可在「设置 → 常规与外观」或侧栏底部一键切换。
          </Text>
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
          <button
            type="button"
            onClick={() => setSelected('office')}
            aria-pressed={selected === 'office'}
            style={{
              textAlign: 'left',
              padding: 16,
              borderRadius: 12,
              cursor: 'pointer',
              background: selected === 'office' ? 'var(--astr-surface, #ffffff)' : 'var(--astr-surface-muted, #f8f8fa)',
              border: selected === 'office' ? '1.5px solid #18181b' : '1px solid var(--astr-border, #e5e7eb)',
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
              transition: 'border-color 140ms ease, background 140ms ease',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <span
                style={{
                  width: 34,
                  height: 34,
                  borderRadius: 8,
                  background: selected === 'office' ? '#18181b' : '#e4e4e7',
                  color: selected === 'office' ? '#ffffff' : '#3f3f46',
                  display: 'inline-flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}
              >
                <IconBriefcase size={18} />
              </span>
              {selected === 'office' && (
                <span
                  style={{
                    width: 20,
                    height: 20,
                    borderRadius: '50%',
                    background: '#18181b',
                    color: '#ffffff',
                    display: 'inline-flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                  }}
                >
                  <IconCheck size={12} />
                </span>
              )}
            </div>
            <div>
              <div style={{ fontSize: 15, fontWeight: 650, color: 'var(--astr-text, #111827)' }}>
                办公模式
              </div>
              <div style={{ fontSize: 12, color: 'var(--astr-muted, #6b7280)', marginTop: 4, lineHeight: 1.55 }}>
                专注文档编写与润色、会议录音纪要、周报复盘与待办清单，自动隐藏终端、Git 与数据库等开发工具。
              </div>
            </div>
          </button>

          <button
            type="button"
            onClick={() => setSelected('developer')}
            aria-pressed={selected === 'developer'}
            style={{
              textAlign: 'left',
              padding: 16,
              borderRadius: 12,
              cursor: 'pointer',
              background: selected === 'developer' ? 'var(--astr-surface, #ffffff)' : 'var(--astr-surface-muted, #f8f8fa)',
              border: selected === 'developer' ? '1.5px solid #18181b' : '1px solid var(--astr-border, #e5e7eb)',
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
              transition: 'border-color 140ms ease, background 140ms ease',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <span
                style={{
                  width: 34,
                  height: 34,
                  borderRadius: 8,
                  background: selected === 'developer' ? '#18181b' : '#e4e4e7',
                  color: selected === 'developer' ? '#ffffff' : '#3f3f46',
                  display: 'inline-flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}
              >
                <IconCode size={18} />
              </span>
              {selected === 'developer' && (
                <span
                  style={{
                    width: 20,
                    height: 20,
                    borderRadius: '50%',
                    background: '#18181b',
                    color: '#ffffff',
                    display: 'inline-flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                  }}
                >
                  <IconCheck size={12} />
                </span>
              )}
            </div>
            <div>
              <div style={{ fontSize: 15, fontWeight: 650, color: 'var(--astr-text, #111827)' }}>
                研发模式
              </div>
              <div style={{ fontSize: 12, color: 'var(--astr-muted, #6b7280)', marginTop: 4, lineHeight: 1.55 }}>
                保留完整工程能力：多会话矩阵、Git 分支与 Worktree、终端、SQL 直连、远端 SSH 与用量统计。
              </div>
            </div>
          </button>
        </div>

        <button
          type="button"
          onClick={handleConfirm}
          style={{
            width: '100%',
            height: 40,
            borderRadius: 10,
            border: 'none',
            background: '#18181b',
            color: '#ffffff',
            fontSize: 13.5,
            fontWeight: 600,
            cursor: 'pointer',
          }}
        >
          进入{selected === 'office' ? '办公模式' : '研发模式'}
        </button>
      </div>
    </Modal>
  )
}
