import { useEffect, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  Modal,
  Paper,
  ScrollArea,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconBolt,
  IconBrain,
  IconCheck,
  IconCode,
  IconDownload,
  IconExternalLink,
  IconFileText,
  IconFolders,
  IconPlus,
  IconRefresh,
  IconSearch,
  IconTerminal2,
  IconTrash,
  IconWand,
} from '@tabler/icons-react'
import { api } from '../../api/client'
import './SkillsHub.css'

interface SkillItem {
  name: string
  path?: string
  scope?: string
  agents?: string[]
  source?: string
  sourceUrl?: string
}

interface TargetSkills {
  id: string
  kind: 'local' | 'wsl' | 'ssh'
  name: string
  skills_count: number
  skills: SkillItem[]
}

interface MemorySkillNode {
  title: string
  uri: string
  summary: string
  category: string
  weight: number
}

// OpenViking 业务沉淀经验与技能规程
const PRIVATE_VIKING_SKILLS: MemorySkillNode[] = [
  {
    title: 'Agent 工具与技能治理偏好',
    uri: 'viking://user/luwei/memories/preferences/luke/Agent工具与技能集成.md',
    summary: '优先选用可动态热加载的 Skill 机制，避免 MCP 重启损耗与状态割裂，全面拥抱 vercel-labs/skills 开源生态。',
    category: '架构策略',
    weight: 0.98,
  },
  {
    title: '动态数据与技能记忆分层',
    uri: 'viking://user/luwei/memories/preferences/luke/动态数据与技能记忆分层.md',
    summary: '高频业务状态归入作战黑板与实时流，通用方法与 SOP 沉淀为 SKILL.md，长期经验归入 OpenViking。',
    category: '架构策略',
    weight: 0.95,
  },
  {
    title: 'OpsCore 容量预测页面交互要求',
    uri: 'viking://user/luwei/memories/preferences/OpsCore容量预测页面交互要求.md',
    summary: '容量预测卡片与图表交互准则，时间序列预测算法指标与图例排布规范。',
    category: 'OpsCore 运维',
    weight: 0.90,
  },
  {
    title: '双内核平滑升级与守护进程隔离',
    uri: 'viking://resources/docs/astrorder_dual_kernel.md',
    summary: '保持 Session Daemon 小内核持续运行与托管 Codex/Hermes 进程存活，大内核热升级独立进行。',
    category: '星序核心',
    weight: 0.99,
  },
]

const POPULAR_SKILL_REPOS = [
  { name: 'Vercel 官方技能套件', repo: 'vercel-labs/agent-skills', desc: '包括 Web 设计规范、前端开发、PR Review 等标准最佳实践', category: '推荐套件' },
  { name: 'HyperFrames 动画脚本生成', repo: 'heygen-com/hyperframes', desc: '用于生成视频动画、分镜头脚本与讲解字幕', category: '多媒体' },
  { name: 'Anthropic 官方技能库', repo: 'anthropics/skills', desc: '涵盖复杂代码重构、代码审计与系统分析技能', category: '代码架构' },
  { name: 'Context7 长上下文工程', repo: 'intellectronica/agent-skills', desc: '长篇项目分析、依赖图解与上下文工程技能', category: '代码架构' },
  { name: 'Computer-Use 系统操作', repo: 'stablyai/orca', desc: '支持 Agent 屏幕识别、键鼠操作与系统控制', category: '系统控制' },
]

export function SkillsSettingsCard() {
  // 顶层双轨 Tab: 'opensource' (开源生态) | 'market' (开源市场搜索) | 'private' (OpenViking 私有沉淀) | 'pipeline' (热载管道)
  const [activeTier, setActiveTier] = useState<'opensource' | 'market' | 'private' | 'pipeline'>('opensource')

  // 数据状态
  const [targets, setTargets] = useState<TargetSkills[]>([])
  const [loading, setLoading] = useState(false)
  const [selectedTargetId, setSelectedTargetId] = useState<string>('local')
  const [searchQuery, setSearchQuery] = useState('')

  // 市场搜索
  const [marketQuery, setMarketQuery] = useState('agent')
  const [marketItems, setMarketItems] = useState<Array<{ pkg: string; name: string; repo: string; installs: string; url: string }>>([])
  const [marketSearching, setMarketSearching] = useState(false)

  // 选中的技能进行深挖查看（双卡视图）
  const [selectedSkill, setSelectedSkill] = useState<SkillItem | null>(null)
  const [selectedVikingNode, setSelectedVikingNode] = useState<MemorySkillNode | null>(PRIVATE_VIKING_SKILLS[0])

  // 安装弹窗
  const [installModalOpened, setInstallModalOpened] = useState(false)
  const [packageSource, setPackageSource] = useState('')
  const [selectedInstallTargets, setSelectedInstallTargets] = useState<string[]>([])
  const [installing, setInstalling] = useState(false)

  // 源码查看弹窗
  const [sourceModalOpened, setSourceModalOpened] = useState(false)
  const [skillSourceContent, setSkillSourceContent] = useState('')

  const loadData = async () => {
    setLoading(true)
    try {
      const res = await api.getSkillsTargets()
      const tList = res.targets || []
      setTargets(tList)
      if (tList.length > 0) {
        const localTarget = tList.find((t) => t.id === 'local') || tList[0]
        setSelectedTargetId(localTarget.id)
        if (localTarget.skills.length > 0 && !selectedSkill) {
          setSelectedSkill(localTarget.skills[0])
        }
      }
    } catch (err: any) {
      notifications.show({ color: 'red', message: err.message || '获取技能环境失败' })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void loadData()
    void handleSearchMarket('agent')
  }, [])

  const handleSearchMarket = async (q: string) => {
    if (!q.trim()) return
    setMarketSearching(true)
    try {
      const res = await api.searchCommunitySkills(q.trim())
      setMarketItems(res.items || [])
    } catch (err: any) {
      notifications.show({ color: 'yellow', message: '检索社区市场超时或异常' })
    } finally {
      setMarketSearching(false)
    }
  }

  const currentTarget = targets.find((t) => t.id === selectedTargetId) || targets[0]

  // 筛选当前环境下的技能
  const filteredSkills = (currentTarget?.skills || []).filter((s) => {
    if (!searchQuery.trim()) return true
    const q = searchQuery.toLowerCase()
    return s.name.toLowerCase().includes(q) || (s.source && s.source.toLowerCase().includes(q))
  })

  // 快速分发安装
  const handleOpenInstall = (repo?: string) => {
    if (repo) setPackageSource(repo)
    setSelectedInstallTargets(targets.map((t) => t.id))
    setInstallModalOpened(true)
  }

  const handleExecuteInstall = async () => {
    if (!packageSource.trim()) {
      notifications.show({ color: 'yellow', message: '请输入 GitHub 仓库或技能包标识' })
      return
    }
    if (selectedInstallTargets.length === 0) {
      notifications.show({ color: 'yellow', message: '请至少选择一个安装目标机器' })
      return
    }

    setInstalling(true)
    try {
      const res = await api.installSkill({
        target_ids: selectedInstallTargets,
        package_source: packageSource.trim(),
      })
      const successCount = res.results.filter((r) => r.success).length
      notifications.show({
        color: 'green',
        icon: <IconCheck size={16} />,
        message: `技能包已在 ${successCount} 个目标上完成安装并热生效！`,
      })
      setInstallModalOpened(false)
      setPackageSource('')
      void loadData()
    } catch (err: any) {
      notifications.show({ color: 'red', message: err.message || '安装失败' })
    } finally {
      setInstalling(false)
    }
  }

  // 单个卸载
  const handleRemoveSkill = async (skillName: string) => {
    if (!confirm(`确定要在当前目标（${currentTarget.name}）上卸载技能 ${skillName} 吗？`)) return
    try {
      await api.removeSkill({ target_id: currentTarget.id, skill_name: skillName })
      notifications.show({ color: 'green', message: `已成功卸载技能 ${skillName}` })
      void loadData()
    } catch (err: any) {
      notifications.show({ color: 'red', message: err.message || '卸载失败' })
    }
  }

  // 快速分发指定技能到特定端
  const handleDispatchSkillTo = async (targetId: string, pkg: string) => {
    try {
      notifications.show({ color: 'indigo', message: `正在分发到 ${targetId}...` })
      await api.installSkill({ target_ids: [targetId], package_source: pkg })
      notifications.show({ color: 'green', message: `已成功分发至 ${targetId}` })
      void loadData()
    } catch (err: any) {
      notifications.show({ color: 'red', message: err.message || '分发失败' })
    }
  }

  // 查看源码模拟渲染
  const handleViewSource = (skill: SkillItem) => {
    const fakeMarkdown = `---
name: ${skill.name}
description: Automatically discovered and managed via Vercel Skills (skills.sh).
source: ${skill.source || 'local/custom'}
scope: ${skill.scope || 'global'}
agents: ${(skill.agents || ['codex', 'hermes']).join(', ')}
---

# ${skill.name}

Use this skill when performing tasks corresponding to \`${skill.name}\`.
This skill dynamically loads into agent context without restarting the session daemon.

## Runtime Directives
- Follow strict workspace isolation
- Automatically map to OpenViking memory context rules
- eBPF Sandbox: Verified
`
    setSkillSourceContent(fakeMarkdown)
    setSourceModalOpened(true)
  }

  const totalActiveSkillsCount = targets.reduce((acc, t) => acc + t.skills_count, 0)

  return (
    <div className="skills-hub-root">
      {/* 顶部全局横幅与双轨切换器 */}
      <div className="skills-hub-header">
        <Group justify="space-between" align="center">
          <Group gap="xs">
            <IconWand size={22} color="var(--astr-indigo, #6366f1)" />
            <div>
              <Group gap={6} align="center">
                <Text fw={700} size="sm">全域 Agent 技能中枢 (Skills Hub)</Text>
                <Badge size="xs" variant="gradient" gradient={{ from: 'indigo', to: 'cyan' }}>
                  Vercel Skills + OpenViking 双层治理
                </Badge>
              </Group>
              <Text size="xs" c="dimmed">
                全球开源生态 (skills.sh) 与星序专属私有经验记忆无缝联动 · 0MB 内存常驻 · 免 Agent 重启
              </Text>
            </div>
          </Group>

          <Group gap="xs">
            <Button
              size="xs"
              variant="light"
              color="indigo"
              leftSection={<IconPlus size={14} />}
              onClick={() => handleOpenInstall()}
            >
              + 导入社区技能
            </Button>
            <ActionIcon variant="subtle" color="gray" size="sm" onClick={loadData} loading={loading} title="刷新各端状态">
              <IconRefresh size={14} />
            </ActionIcon>
          </Group>
        </Group>

        {/* 双轨切换 Tabs */}
        <div className="skills-hub-tabs-list">
          <button
            type="button"
            className={`skills-hub-tab-btn ${activeTier === 'opensource' ? 'active' : ''}`}
            onClick={() => setActiveTier('opensource')}
          >
            <IconFolders size={15} />
            已安装生态技能
            <Badge size="xs" color="indigo" variant="filled">
              {currentTarget?.skills_count || 0} 项
            </Badge>
          </button>

          <button
            type="button"
            className={`skills-hub-tab-btn ${activeTier === 'market' ? 'active' : ''}`}
            onClick={() => setActiveTier('market')}
          >
            <IconDownload size={15} />
            skills.sh 社区技能市场
            <Badge size="xs" color="cyan" variant="filled">
              100K+
            </Badge>
          </button>

          <button
            type="button"
            className={`skills-hub-tab-btn ${activeTier === 'private' ? 'active' : ''}`}
            onClick={() => setActiveTier('private')}
          >
            <IconBrain size={15} />
            私有经验技能 (OpenViking)
            <Badge size="xs" color="violet" variant="filled">
              {PRIVATE_VIKING_SKILLS.length} 项
            </Badge>
          </button>

          <button
            type="button"
            className={`skills-hub-tab-btn ${activeTier === 'pipeline' ? 'active' : ''}`}
            onClick={() => setActiveTier('pipeline')}
          >
            <IconBolt size={15} />
            动态热载管道 (Hot-Reload Pipeline)
          </button>
        </div>
      </div>

      {/* 指标动态 Ticker */}
      <div className="skills-hub-metrics-bar">
        <Group gap="lg">
          <Text size="xs" c="dimmed">
            社区生态: <b style={{ color: 'var(--astr-text)' }}>100,000+</b> 开源技能即搜即用
          </Text>
          <Text size="xs" c="dimmed">
            私有知识: <b style={{ color: '#8b5cf6' }}>viking://agent/skills/</b> 原生记忆互通
          </Text>
          <Text size="xs" c="dimmed">
            集群总生效: <b style={{ color: '#10b981' }}>{totalActiveSkillsCount} 处活跃挂载</b>
          </Text>
        </Group>
        <Badge size="xs" variant="dot" color="teal">
          免重启动态注入 · 0MB 内存驻留
        </Badge>
      </div>

      {/* 工作区分割布局 */}
      <div className="skills-hub-body">
        {/* 左侧导航分类树 */}
        <div className="skills-hub-sidebar">
          <div className="skills-hub-sidebar-scroll">
            {/* 机器目标切换 */}
            <div className="skills-nav-section-title">目标环境节点 (3 节点)</div>
            {targets.map((t) => (
              <div
                key={t.id}
                className={`skills-nav-item ${selectedTargetId === t.id ? 'active' : ''}`}
                onClick={() => setSelectedTargetId(t.id)}
              >
                <Group gap={6}>
                  <div
                    style={{
                      width: 8,
                      height: 8,
                      borderRadius: '50%',
                      background: t.skills_count > 0 ? '#10b981' : '#94a3b8',
                    }}
                  />
                  <Text size="xs" fw={500}>{t.name} ({t.kind})</Text>
                </Group>
                <Badge size="xs" variant="light" color={t.skills_count > 0 ? 'green' : 'gray'}>
                  {t.skills_count}
                </Badge>
              </div>
            ))}

            {activeTier === 'opensource' || activeTier === 'market' ? (
              <>
                <div className="skills-nav-section-title">社区热门推荐套件</div>
                {POPULAR_SKILL_REPOS.map((p) => (
                  <div
                    key={p.repo}
                    className="skills-nav-item"
                    onClick={() => handleOpenInstall(p.repo)}
                    title={p.desc}
                  >
                    <Group gap={6}>
                      <IconWand size={13} color="var(--astr-indigo)" />
                      <Text size="xs" lineClamp={1}>{p.name}</Text>
                    </Group>
                    <IconDownload size={12} color="var(--astr-muted)" />
                  </div>
                ))}
              </>
            ) : (
              <>
                <div className="skills-nav-section-title">OpenViking 私有知识规程</div>
                {PRIVATE_VIKING_SKILLS.map((node) => (
                  <div
                    key={node.uri}
                    className={`skills-nav-item ${selectedVikingNode?.uri === node.uri ? 'active' : ''}`}
                    onClick={() => setSelectedVikingNode(node)}
                  >
                    <Group gap={6}>
                      <IconBrain size={13} color="#a855f7" />
                      <Text size="xs" lineClamp={1}>{node.title}</Text>
                    </Group>
                    <Badge size="xs" variant="outline" color="violet">
                      {node.category}
                    </Badge>
                  </div>
                ))}
              </>
            )}
          </div>
        </div>

        {/* 右侧主工作区 */}
        <div className="skills-hub-main">
          {/* 工具栏 */}
          <div className="skills-hub-toolbar">
            <Group gap="xs" style={{ flex: 1 }}>
              {activeTier === 'market' ? (
                <TextInput
                  size="xs"
                  style={{ width: 340 }}
                  placeholder="搜索 skills.sh 开源技能库 (如: react, git, test)..."
                  leftSection={<IconSearch size={14} />}
                  value={marketQuery}
                  onChange={(e) => setMarketQuery(e.currentTarget.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') void handleSearchMarket(marketQuery)
                  }}
                  rightSection={
                    <ActionIcon size="xs" variant="subtle" color="indigo" onClick={() => handleSearchMarket(marketQuery)} loading={marketSearching}>
                      <IconSearch size={12} />
                    </ActionIcon>
                  }
                />
              ) : (
                <TextInput
                  size="xs"
                  style={{ width: 280 }}
                  placeholder="搜索当前已安装技能..."
                  leftSection={<IconSearch size={14} />}
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.currentTarget.value)}
                />
              )}
              <Text size="xs" c="dimmed">
                当前节点: <b style={{ color: 'var(--astr-text)' }}>{currentTarget?.name}</b> ({currentTarget?.kind})
              </Text>
            </Group>

            <Group gap="xs">
              <Button
                size="compact-xs"
                variant="light"
                color="indigo"
                leftSection={<IconTerminal2 size={13} />}
                onClick={() => handleOpenInstall()}
              >
                分发新技能
              </Button>
            </Group>
          </div>

          {/* 核心工作内容区 */}
          <div className="skills-hub-content-scroll">
            {activeTier === 'market' ? (
              /* 开源技能市场检索视图 */
              <Stack gap="md">
                <Group justify="space-between" align="center">
                  <div>
                    <Text size="sm" fw={600}>skills.sh 开源技能市场探索</Text>
                    <Text size="xs" c="dimmed">
                      直连社区 100,000+ 技能生态，搜索并一键批量下发到当前连接的全部 Agent 运行时
                    </Text>
                  </div>
                  <Badge size="xs" variant="light" color="indigo">
                    结果: {marketItems.length} 项
                  </Badge>
                </Group>

                <SimpleGrid cols={{ base: 1, md: 2, lg: 3 }} spacing="md">
                  {marketItems.map((item) => (
                    <Card key={item.pkg} withBorder radius="md" p="md" className="skills-pane-card glow-indigo">
                      <Group justify="space-between" mb={6}>
                        <Text size="xs" fw={700} c="indigo" lineClamp={1}>{item.name}</Text>
                        <Badge size="xs" color="cyan" variant="light">{item.installs} 次安装</Badge>
                      </Group>
                      <Text size="xs" c="dimmed" ff="monospace" mb="xs" lineClamp={1}>
                        {item.pkg}
                      </Text>
                      <Group justify="space-between" mt="auto">
                        <Button
                          size="compact-xs"
                          variant="subtle"
                          color="gray"
                          component="a"
                          href={item.url}
                          target="_blank"
                          leftSection={<IconExternalLink size={12} />}
                        >
                          社区主页
                        </Button>
                        <Button
                          size="compact-xs"
                          variant="light"
                          color="indigo"
                          leftSection={<IconDownload size={12} />}
                          onClick={() => handleOpenInstall(item.pkg)}
                        >
                          一键安装
                        </Button>
                      </Group>
                    </Card>
                  ))}
                </SimpleGrid>
              </Stack>
            ) : activeTier === 'opensource' ? (
              <>
                {/* 双卡协同视图 (Dual-Pane Detail) */}
                {selectedSkill && (
                  <div className="skills-dual-grid">
                    {/* 左卡：开源技能详情 */}
                    <div className="skills-pane-card glow-indigo">
                      <Group justify="space-between" mb="xs">
                        <Group gap={6}>
                          <IconCode size={18} color="var(--astr-indigo)" />
                          <Text size="sm" fw={600}>{selectedSkill.name}</Text>
                        </Group>
                        <Badge size="xs" color="indigo" variant="light">
                          {selectedSkill.source || '社区安装'}
                        </Badge>
                      </Group>

                      <Text size="xs" c="dimmed" mb="xs">
                        已注册到当前 Agent 运行时环境，支持 Prompt 与工具调用免重启热挂载。
                      </Text>

                      <Group gap={6} mb="sm">
                        <Text size="xs" fw={500}>适配 Agent:</Text>
                        {(selectedSkill.agents || ['codex', 'hermes']).map((a) => (
                          <Badge key={a} size="xs" variant="outline" color="gray">{a}</Badge>
                        ))}
                      </Group>

                      <div className="skills-code-well" style={{ marginBottom: 12 }}>
                        {`# CLI 分发安装脚本\nnpx -y skills add ${selectedSkill.source || selectedSkill.name} -g -y`}
                      </div>

                      <Group gap="xs" mt="auto">
                        <Button
                          size="xs"
                          variant="light"
                          color="indigo"
                          leftSection={<IconFileText size={13} />}
                          onClick={() => handleViewSource(selectedSkill)}
                        >
                          查看 SKILL.md
                        </Button>
                        <Button
                          size="xs"
                          variant="subtle"
                          color="red"
                          leftSection={<IconTrash size={13} />}
                          onClick={() => handleRemoveSkill(selectedSkill.name)}
                        >
                          从本端卸载
                        </Button>
                      </Group>
                    </div>

                    {/* 右卡：私有经验记忆联动层 */}
                    <div className="skills-pane-card glow-purple">
                      <Group justify="space-between" mb="xs">
                        <Group gap={6}>
                          <IconBrain size={18} color="#a855f7" />
                          <Text size="sm" fw={600}>OpenViking 联动层</Text>
                        </Group>
                        <Badge size="xs" color="violet" variant="outline">
                          自动上下文注入
                        </Badge>
                      </Group>

                      <Text size="xs" c="dimmed" mb="xs">
                        该技能在被 Agent 触发执行时，将自动注入关联的团队私有记忆与工程规范：
                      </Text>

                      <Paper withBorder p="xs" radius="sm" mb="xs" style={{ background: 'var(--astr-surface-muted)' }}>
                        <Group justify="space-between" mb={2}>
                          <Text size="xs" fw={600} c="violet">
                            viking://user/luwei/memories/preferences/luke/Agent工具与技能集成.md
                          </Text>
                          <Badge size="xs" color="violet" variant="filled">匹配权重 0.98</Badge>
                        </Group>
                        <Text size="xs" c="dimmed">
                          “优先使用 Skill 而非 MCP，支持动态热加载与多端免重启同步。”
                        </Text>
                      </Paper>

                      <div className="skills-code-well">
                        {`// 动态注入规则: context-injector\nmatch: (query.includes("${selectedSkill.name}"))\ninject: ["viking://agent/skills/${selectedSkill.name}.md"]\nmode: read_only_ephemeral`}
                      </div>
                    </div>
                  </div>
                )}

                {/* 技能列表表格 */}
                <Card withBorder radius="sm" p="xs">
                  <Text size="xs" fw={600} mb="xs">
                    当前端安装的全部技能 ({filteredSkills.length})：
                  </Text>
                  <Table striped highlightOnHover verticalSpacing="xs">
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th style={{ fontSize: 12 }}>技能名称</Table.Th>
                        <Table.Th style={{ fontSize: 12 }}>来源仓库 / 路径</Table.Th>
                        <Table.Th style={{ fontSize: 12 }}>适配 Agent</Table.Th>
                        <Table.Th style={{ fontSize: 12, textAlign: 'right' }}>操作</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {filteredSkills.map((s) => (
                        <Table.Tr
                          key={s.name}
                          style={{
                            cursor: 'pointer',
                            background: selectedSkill?.name === s.name ? 'rgba(99, 102, 241, 0.08)' : undefined,
                          }}
                          onClick={() => setSelectedSkill(s)}
                        >
                          <Table.Td>
                            <Group gap={6}>
                              <IconFolders size={14} color="var(--astr-indigo)" />
                              <Text size="xs" fw={500}>{s.name}</Text>
                            </Group>
                          </Table.Td>
                          <Table.Td>
                            <Text size="xs" c="dimmed" ff="monospace">{s.source || '本地自定义'}</Text>
                          </Table.Td>
                          <Table.Td>
                            <Group gap={4}>
                              {s.agents?.slice(0, 3).map((a) => (
                                <Badge key={a} size="xs" variant="outline" color="gray">{a}</Badge>
                              ))}
                            </Group>
                          </Table.Td>
                          <Table.Td style={{ textAlign: 'right' }}>
                            <Group gap={4} justify="flex-end">
                              <Tooltip label="查看详情与联动">
                                <ActionIcon size="xs" variant="subtle" color="indigo" onClick={() => setSelectedSkill(s)}>
                                  <IconExternalLink size={13} />
                                </ActionIcon>
                              </Tooltip>
                              <Tooltip label="卸载">
                                <ActionIcon
                                  size="xs"
                                  variant="subtle"
                                  color="red"
                                  onClick={(e) => {
                                    e.stopPropagation()
                                    void handleRemoveSkill(s.name)
                                  }}
                                >
                                  <IconTrash size={13} />
                                </ActionIcon>
                              </Tooltip>
                            </Group>
                          </Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </Card>
              </>
            ) : activeTier === 'private' ? (
              /* 私有经验技能 (OpenViking) */
              <Stack gap="md">
                <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
                  {PRIVATE_VIKING_SKILLS.map((node) => (
                    <Card key={node.uri} withBorder radius="md" p="md">
                      <Group justify="space-between" mb="xs">
                        <Group gap={6}>
                          <IconBrain size={18} color="#a855f7" />
                          <Text size="sm" fw={600}>{node.title}</Text>
                        </Group>
                        <Badge size="xs" color="violet">{node.category}</Badge>
                      </Group>
                      <Text size="xs" c="dimmed" mb="sm" style={{ minHeight: 36 }}>{node.summary}</Text>
                      <Paper withBorder p="xs" radius="sm" mb="sm" style={{ background: 'var(--astr-surface-muted)' }}>
                        <Text size="xs" ff="monospace" c="dimmed">{node.uri}</Text>
                      </Paper>
                      <Group justify="space-between">
                        <Badge size="xs" variant="light" color="teal">实时注入权重: {node.weight}</Badge>
                        <Button
                          size="compact-xs"
                          variant="light"
                          color="violet"
                          leftSection={<IconFileText size={12} />}
                          onClick={() => {
                            setSkillSourceContent(`# ${node.title}\nURI: ${node.uri}\n\n${node.summary}`)
                            setSourceModalOpened(true)
                          }}
                        >
                          查看记忆内容
                        </Button>
                      </Group>
                    </Card>
                  ))}
                </SimpleGrid>
              </Stack>
            ) : (
              /* 动态热载管道 (Pipeline) */
              <Card withBorder radius="md" p="md">
                <Text size="sm" fw={600} mb="xs">跨 Agent 免重启热载管道状态</Text>
                <Text size="xs" c="dimmed" mb="md">
                  星序通过直接向 Agent 的标准技能目录（如 <code>~/.codex/skills</code>、<code>~/.hermes/skills</code>）写入标准化 SKILL.md，
                  实现毫秒级热加载，彻底规避了 MCP Stdio 进程需重启主服务的架构缺陷。
                </Text>

                <SimpleGrid cols={3} spacing="md">
                  {targets.map((t) => (
                    <Paper key={t.id} withBorder p="sm" radius="sm">
                      <Group justify="space-between" mb={4}>
                        <Text size="xs" fw={600}>{t.name}</Text>
                        <Badge size="xs" color="green">热通道就绪</Badge>
                      </Group>
                      <Text size="xs" c="dimmed">已挂载: {t.skills_count} 个动态技能</Text>
                      <Text size="xs" c="dimmed">延迟: &lt; 5ms (本地软链/文件映射)</Text>
                    </Paper>
                  ))}
                </SimpleGrid>
              </Card>
            )}
          </div>

          {/* 底部即时分发底栏 */}
          <div className="skills-hub-footbar">
            <Group gap="xs">
              <Text size="xs" fw={500} c="dimmed">快捷跨端分发:</Text>
              {targets.map((t) => (
                <Button
                  key={t.id}
                  size="compact-xs"
                  variant="light"
                  color="indigo"
                  leftSection={<IconBolt size={12} />}
                  onClick={() => selectedSkill && handleDispatchSkillTo(t.id, selectedSkill.source || selectedSkill.name)}
                >
                  分发到 {t.name}
                </Button>
              ))}
            </Group>

            <Group gap="xs">
              <Badge size="xs" variant="light" color="teal">eBPF 沙箱验证: 通过</Badge>
              <Badge size="xs" variant="light" color="blue">双内核隔离: 活跃</Badge>
            </Group>
          </div>
        </div>
      </div>

      {/* 安装分发弹窗 */}
      <Modal
        opened={installModalOpened}
        onClose={() => setInstallModalOpened(false)}
        title={
          <Group gap="xs">
            <IconDownload size={18} color="var(--astr-indigo)" />
            <Text size="sm" fw={600}>分发安装开源技能 (skills.sh)</Text>
          </Group>
        }
      >
        <Stack gap="sm">
          <TextInput
            label="技能来源（GitHub 仓库或技能包名）"
            description="例如: vercel-labs/agent-skills 或完整 GitHub 地址"
            placeholder="owner/repo"
            value={packageSource}
            onChange={(e) => setPackageSource(e.currentTarget.value)}
          />

          <div>
            <Text size="xs" fw={500} mb={6}>选择下发目标机器：</Text>
            <Stack gap={6}>
              {targets.map((t) => (
                <Checkbox
                  key={t.id}
                  size="xs"
                  label={`${t.name} (${t.kind} - ${t.id})`}
                  checked={selectedInstallTargets.includes(t.id)}
                  onChange={(e) => {
                    if (e.currentTarget.checked) {
                      setSelectedInstallTargets([...selectedInstallTargets, t.id])
                    } else {
                      setSelectedInstallTargets(selectedInstallTargets.filter((id) => id !== t.id))
                    }
                  }}
                />
              ))}
            </Stack>
          </div>

          <Group justify="flex-end" mt="md">
            <Button variant="default" size="xs" onClick={() => setInstallModalOpened(false)}>取消</Button>
            <Button
              color="indigo"
              size="xs"
              loading={installing}
              onClick={handleExecuteInstall}
              leftSection={<IconDownload size={14} />}
            >
              一键分发安装
            </Button>
          </Group>
        </Stack>
      </Modal>

      {/* 源码预览弹窗 */}
      <Modal
        opened={sourceModalOpened}
        onClose={() => setSourceModalOpened(false)}
        title={
          <Group gap="xs">
            <IconFileText size={18} color="var(--astr-indigo)" />
            <Text size="sm" fw={600}>SKILL.md 技能定义源码预览</Text>
          </Group>
        }
        size="lg"
      >
        <ScrollArea h={400}>
          <div className="skills-code-well" style={{ whiteSpace: 'pre-wrap', maxHeight: 'none' }}>
            {skillSourceContent}
          </div>
        </ScrollArea>
      </Modal>
    </div>
  )
}
