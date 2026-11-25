import { useMemo } from 'react'
import { Alert, Badge, Button, Group, Modal, Select, Stack, Text } from '@mantine/core'
import {
  IconAlertTriangle,
  IconDatabase,
  IconFileCode,
  IconPlayerPlay,
  IconShieldLock,
} from '@tabler/icons-react'
import type { DbxSqlClassification, ProjectDatabaseTarget } from '../../api/client'
import { highlightCodeLines } from '../../components/markdownHighlight'
import { classifySql } from './sqlSafety'
import { formatTargetLabel } from './projectDbStore'

export interface SqlConfirmRequest {
  sql: string
  sourceKind: 'snippet' | 'file'
  sourceName: string
  sourcePath?: string
  target: ProjectDatabaseTarget
  classification?: DbxSqlClassification
}

export function SqlExecuteConfirmModal({
  request,
  targets,
  executing,
  onChangeTarget,
  onOpenInDbx,
  onCancel,
  onConfirm,
}: {
  request: SqlConfirmRequest | null
  targets: ProjectDatabaseTarget[]
  executing?: boolean
  onChangeTarget: (targetId: string) => void
  onOpenInDbx?: () => void
  onCancel: () => void
  onConfirm: () => void
}) {
  const classification = useMemo(
    () => (request ? request.classification || classifySql(request.sql) : null),
    [request],
  )
  const highlightedLines = useMemo(() => {
    if (!request) return []
    const displaySql = request.sql.replace(/\r\n/g, '\n').replace(/\n$/, '')
    return highlightCodeLines(displaySql, 'sql', { maxChars: 64_000, maxLines: 1500 })
  }, [request])

  if (!request || !classification) return null

  const isProd = request.target.is_prod
  const isDestructive = classification.risk_level === 'destructive'
  const isWrite = classification.risk_level === 'write'
  const riskBadge = isDestructive
    ? { color: 'red', label: '高危 DDL / 无条件变更' }
    : isWrite
      ? { color: 'orange', label: classification.statement_count > 1 ? `多语句脚本 (${classification.statement_count} 条)` : '写入 / 变更语句' }
      : { color: 'yellow', label: '生产环境只读查询' }

  const targetOptions = targets.map((t) => ({
    value: t.id,
    label: `${formatTargetLabel(t)}${t.is_prod ? ' [PROD]' : ''}`,
  }))

  return (
    <Modal
      opened
      onClose={() => {
        if (!executing) onCancel()
      }}
      title={
        <Group gap="xs">
          <IconShieldLock size={18} style={{ color: isProd || isDestructive ? 'var(--astr-red)' : 'var(--astr-yellow)' }} />
          <Text fw={650} size="sm">
            确认执行 SQL
          </Text>
          {isProd && (
            <Badge size="xs" color="red" variant="filled">
              PROD
            </Badge>
          )}
        </Group>
      }
      centered
      size="min(960px, 92vw)"
      classNames={{ content: 'sql-confirm-modal-content' }}
    >
      <Stack gap="sm">
        {isProd && (
          <div className="sql-confirm-prod-banner" role="alert">
            <IconAlertTriangle size={16} style={{ flexShrink: 0 }} />
            <span>生产环境保护：当前目标标记为 PROD，请仔细核对库名与完整 SQL 后再执行。</span>
          </div>
        )}

        <Group justify="space-between" align="flex-end" wrap="wrap" gap="xs">
          {targetOptions.length > 1 ? (
            <Select
              label="目标数据库"
              size="xs"
              data={targetOptions}
              value={request.target.id}
              onChange={(val) => {
                if (val) onChangeTarget(val)
              }}
              style={{ flex: 1, minWidth: 240 }}
              comboboxProps={{ withinPortal: true, zIndex: 4100 }}
            />
          ) : (
            <Stack gap={2} style={{ flex: 1 }}>
              <Text size="xs" c="dimmed">
                目标数据库
              </Text>
              <Group gap={6}>
                <IconDatabase size={14} style={{ color: isProd ? 'var(--astr-red)' : 'var(--astr-indigo)' }} />
                <Text size="xs" fw={650}>
                  {request.target.connection_name}
                  {request.target.database ? ` / ${request.target.database}` : ''}
                  {request.target.schema ? ` · ${request.target.schema}` : ''}
                </Text>
                {isProd && (
                  <Badge size="xs" color="red" variant="light">
                    PROD
                  </Badge>
                )}
              </Group>
            </Stack>
          )}

          <Group gap={6}>
            <Badge size="sm" variant="light" color={riskBadge.color}>
              {riskBadge.label}
            </Badge>
            <Badge size="sm" variant="outline" color="gray">
              {classification.statement_count} 条语句
            </Badge>
          </Group>
        </Group>

        {classification.warnings && classification.warnings.length > 0 && (
          <Alert
            color={isDestructive || isProd ? 'red' : 'orange'}
            variant="light"
            py={6}
            px="sm"
            icon={<IconAlertTriangle size={15} />}
          >
            <Text size="xs">{classification.warnings.join('；')}</Text>
          </Alert>
        )}

        <Stack gap={6}>
          <Group justify="space-between" align="center" wrap="nowrap" gap="sm">
            <Group gap={6} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
              <IconFileCode size={14} style={{ color: 'var(--astr-muted)', flexShrink: 0 }} />
              <Text
                size="xs"
                c="dimmed"
                fw={550}
                className="sql-confirm-file-path"
                title={request.sourceKind === 'file' ? (request.sourcePath || request.sourceName) : undefined}
              >
                {request.sourceKind === 'file' ? `SQL 文件：${request.sourcePath || request.sourceName}` : 'SQL 内容预览'}
              </Text>
            </Group>
            <Text size="xs" c="dimmed" style={{ flexShrink: 0, fontVariantNumeric: 'tabular-nums' }}>
              {highlightedLines.length} 行
            </Text>
          </Group>
          <div className="sql-confirm-preview" role="region" aria-label="SQL 内容预览">
            <pre className="sql-confirm-code-pre">
              <code className="sql-confirm-code">
                {highlightedLines.map((lineNodes, idx) => (
                  <span className="sql-confirm-line" key={idx}>
                    <span className="sql-confirm-line-no" aria-hidden="true">
                      {idx + 1}
                    </span>
                    <span className="sql-confirm-line-code">
                      {lineNodes.length > 0 ? lineNodes : ' '}
                      {idx < highlightedLines.length - 1 ? '\n' : ''}
                    </span>
                  </span>
                ))}
              </code>
            </pre>
          </div>
        </Stack>

        <Group justify="space-between" mt={4}>
          <div>
            {onOpenInDbx && (
              <Button
                size="xs"
                variant="subtle"
                color="gray"
                leftSection={<IconDatabase size={14} />}
                disabled={executing}
                onClick={() => {
                  onCancel()
                  onOpenInDbx()
                }}
              >
                改在 DBX 打开
              </Button>
            )}
          </div>
          <Group gap="xs">
            <Button size="xs" variant="subtle" color="gray" data-autofocus disabled={executing} onClick={onCancel}>
              取消
            </Button>
            <Button
              size="xs"
              color={isProd || isDestructive ? 'red' : 'indigo'}
              leftSection={<IconPlayerPlay size={14} />}
              loading={executing}
              onClick={onConfirm}
            >
              {isProd ? '确认在 PROD 执行' : '确认执行'}
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  )
}
