import { useMemo, useState } from 'react'
import { Button, Group, Stack, Text, TextInput } from '@mantine/core'
import { IconDownload, IconRefresh, IconSearch, IconShieldCheck } from '@tabler/icons-react'
import type { LearningTopic } from '../../api/learning'
import { formatShortDate } from './useLearningRadar'

interface MasteryArchivePanelProps {
  topics: LearningTopic[]
  compact?: boolean
  busyTopicId?: string | null
  onRestoreTopic: (topic: LearningTopic) => void
  onExport: () => void
}

export function MasteryArchivePanel({
  topics,
  compact = false,
  busyTopicId,
  onRestoreTopic,
  onExport,
}: MasteryArchivePanelProps) {
  const [filter, setFilter] = useState<'mastered' | 'dismissed' | 'snoozed'>('mastered')
  const [search, setSearch] = useState('')

  const counts = useMemo(() => {
    let mastered = 0
    let dismissed = 0
    let snoozed = 0
    for (const t of topics) {
      if (t.status === 'mastered') mastered++
      else if (t.status === 'dismissed') dismissed++
      else if (t.status === 'snoozed') snoozed++
    }
    return { mastered, dismissed, snoozed }
  }, [topics])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return topics.filter((t) => {
      if (t.status !== filter) return false
      if (!q) return true
      return (
        t.title.toLowerCase().includes(q) ||
        (t.project_name || '').toLowerCase().includes(q) ||
        (t.study_notes || '').toLowerCase().includes(q)
      )
    })
  }, [topics, filter, search])

  const visible = compact ? filtered.slice(0, 6) : filtered

  return (
    <div className="learning-panel-card">
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="xs">
        <div>
          <Group gap={6}>
            <IconShieldCheck size={16} />
            <Text fw={600} size="sm">个人技能掌握档案（防重复推荐）</Text>
          </Group>
          <Text size="xs" c="dimmed" mt={2}>
            标记为「已掌握」或「永久忽略」的主题会在后续导师 Bot 扫描时自动排除，避免打扰。
          </Text>
        </div>
        <span className="learning-mono-chip">{counts.mastered} 项已掌握</span>
      </Group>

      <Group gap="xs" wrap="wrap">
        <TextInput
          size="xs"
          style={{ flex: 1, minWidth: 160 }}
          leftSection={<IconSearch size={13} />}
          placeholder="搜索已掌握或忽略的主题…"
          value={search}
          onChange={(e) => setSearch(e.currentTarget.value)}
        />
        <Group gap={4}>
          <button
            type="button"
            className={`learning-filter-btn${filter === 'mastered' ? ' is-active' : ''}`}
            onClick={() => setFilter('mastered')}
          >
            已掌握 {counts.mastered}
          </button>
          <button
            type="button"
            className={`learning-filter-btn${filter === 'dismissed' ? ' is-active' : ''}`}
            onClick={() => setFilter('dismissed')}
          >
            已忽略 {counts.dismissed}
          </button>
          {counts.snoozed > 0 && (
            <button
              type="button"
              className={`learning-filter-btn${filter === 'snoozed' ? ' is-active' : ''}`}
              onClick={() => setFilter('snoozed')}
            >
              稍后 {counts.snoozed}
            </button>
          )}
        </Group>
      </Group>

      {visible.length === 0 ? (
        <Text size="xs" c="dimmed" ta="center" py="md">
          {filter === 'mastered'
            ? '暂无已掌握记录；完成自测或伴学后点「已掌握」即可沉淀在此。'
            : '当前筛选下没有记录。'}
        </Text>
      ) : (
        <Stack gap={8}>
          {visible.map((topic) => (
            <div key={topic.id} className="learning-evidence-row" style={{ alignItems: 'flex-start' }}>
              <div style={{ minWidth: 0, flex: 1 }}>
                <Text size="xs" fw={600} lineClamp={1}>
                  {topic.title}
                </Text>
                <Group gap={6} mt={3} wrap="wrap">
                  {topic.project_name && <span className="learning-mono-chip">{topic.project_name}</span>}
                  <Text size="xs" c="dimmed">
                    {topic.status === 'mastered'
                      ? `掌握于 ${formatShortDate(topic.mastered_at || topic.updated_at)}`
                      : topic.status === 'snoozed'
                        ? `将于 ${formatShortDate(topic.snooze_until)} 恢复`
                        : `忽略于 ${formatShortDate(topic.updated_at)}`}
                  </Text>
                  {topic.review.pass_count > 0 && (
                    <Text size="xs" c="dimmed">· 自测通过 {topic.review.pass_count} 次</Text>
                  )}
                </Group>
                {topic.study_notes && !compact && (
                  <Text size="xs" c="dimmed" mt={4} lineClamp={2}>
                    笔记：{topic.study_notes}
                  </Text>
                )}
              </div>
              <Button
                size="compact-xs"
                variant="subtle"
                color="gray"
                leftSection={<IconRefresh size={12} />}
                loading={busyTopicId === topic.id}
                onClick={() => onRestoreTopic(topic)}
              >
                移回复习
              </Button>
            </div>
          ))}
        </Stack>
      )}

      <Group justify="space-between" align="center" pt={4} style={{ borderTop: '1px solid var(--astr-border)' }}>
        <Text size="xs" c="dimmed">
          已排除 {counts.mastered + counts.dismissed} 个知识点，后续扫描将自动跳过
        </Text>
        <Button size="compact-xs" variant="default" leftSection={<IconDownload size={12} />} onClick={onExport}>
          导出 JSON
        </Button>
      </Group>
    </div>
  )
}
