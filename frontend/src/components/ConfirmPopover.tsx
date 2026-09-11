import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Button, Group, Paper, Stack, Text } from '@mantine/core'
import { IconAlertTriangle } from '@tabler/icons-react'
import type { ConfirmationCoordinates } from './confirmationPosition'

const CONFIRM_POPOVER_Z_INDEX = 2_147_483_000
const CONFIRM_POPOVER_OVERLAY_Z_INDEX = CONFIRM_POPOVER_Z_INDEX - 1

export interface ConfirmPopoverProps {
  opened: boolean
  coords?: ConfirmationCoordinates
  title?: string
  message: string
  confirmLabel?: string
  cancelLabel?: string
  confirmColor?: string
  loading?: boolean
  onConfirm: () => void | Promise<void>
  onCancel: () => void
}

export function ConfirmPopover({
  opened,
  coords,
  title = '确认删除',
  message,
  confirmLabel = '确认删除',
  cancelLabel = '取消',
  confirmColor = 'red',
  loading = false,
  onConfirm,
  onCancel,
}: ConfirmPopoverProps) {
  const popoverRef = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState<{ top: number; left: number }>({ top: 0, left: 0 })

  useEffect(() => {
    if (!opened) return

    const calculatePosition = () => {
      const width = 290
      const estimatedHeight = 150
      const margin = 12

      if (coords && typeof coords.x === 'number' && typeof coords.y === 'number' && coords.x >= 0 && coords.y >= 0) {
        let left = coords.x + 8
        let top = coords.y + 8

        const vw = typeof window !== 'undefined' ? window.innerWidth : 1024
        const vh = typeof window !== 'undefined' ? window.innerHeight : 768

        // If overflowing right edge, flip to left of cursor
        if (left + width > vw - margin) {
          left = Math.max(margin, coords.x - width - 8)
        }

        // If overflowing bottom edge, flip above cursor
        if (top + estimatedHeight > vh - margin) {
          top = Math.max(margin, coords.y - estimatedHeight - 8)
        }

        setPosition({ left, top })
      } else {
        // Center fallback when no coordinates provided (e.g. keyboard navigation or mobile)
        const vw = typeof window !== 'undefined' ? window.innerWidth : 1024
        const vh = typeof window !== 'undefined' ? window.innerHeight : 768
        const left = Math.max(margin, (vw - width) / 2)
        const top = Math.max(margin, (vh - estimatedHeight) / 2)
        setPosition({ left, top })
      }
    }

    calculatePosition()

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !loading) {
        onCancel()
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [opened, coords, loading, onCancel])

  if (!opened) return null

  return createPortal(
    <div
      className="confirm-popover-overlay"
      role="presentation"
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: CONFIRM_POPOVER_OVERLAY_Z_INDEX,
        background: 'transparent',
      }}
      onClick={(e) => {
        e.stopPropagation()
        if (!loading) onCancel()
      }}
      onContextMenu={(e) => {
        e.preventDefault()
        if (!loading) onCancel()
      }}
    >
      <Paper
        ref={popoverRef}
        className="confirm-popover-card"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        shadow="xl"
        p="sm"
        radius="md"
        withBorder
        style={{
          position: 'fixed',
          top: position.top,
          left: position.left,
          width: 290,
          zIndex: CONFIRM_POPOVER_Z_INDEX,
          boxShadow: '0 8px 30px rgba(0, 0, 0, 0.18), 0 2px 8px rgba(0, 0, 0, 0.08)',
          pointerEvents: 'auto',
          animation: 'confirmPopoverFadeIn 0.12s ease-out',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <Stack gap="xs">
          <Group gap={8} wrap="nowrap" align="flex-start">
            <IconAlertTriangle
              size={18}
              color="var(--mantine-color-red-6)"
              style={{ flexShrink: 0, marginTop: 2 }}
            />
            <Text fw={600} size="sm" style={{ lineHeight: 1.3 }}>
              {title}
            </Text>
          </Group>
          <Text size="xs" c="dimmed" style={{ whiteSpace: 'pre-line', lineHeight: 1.45 }}>
            {message}
          </Text>
          <Group justify="flex-end" gap={8} mt={4}>
            <Button
              variant="default"
              size="compact-xs"
              onClick={onCancel}
              disabled={loading}
            >
              {cancelLabel}
            </Button>
            <Button
              color={confirmColor}
              size="compact-xs"
              onClick={() => void onConfirm()}
              loading={loading}
            >
              {confirmLabel}
            </Button>
          </Group>
        </Stack>
      </Paper>
    </div>,
    document.body,
  )
}
