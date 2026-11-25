import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { notifications } from '@mantine/notifications'
import { ApiError, api, type ProjectDatabaseTarget } from '../../api/client'
import type { Session } from '../../domain/types'
import { scopeKey } from '../../domain/semantics'
import { useAstrorderStore } from '../../state/store'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { DBX_PLUGIN_ID } from '../sidecar/viewers/dbx/DbxViewer'
import { readSqlText } from '../sidecar/viewers/dbx/dbxStore'
import { extractSqlFileName, stripSqlLocationSuffix } from '../sidecar/viewers/dbx/sqlFormats'
import { classifySql, requiresExecutionConfirmation } from './sqlSafety'
import {
  resolveProjectDatabaseIdentity,
  selectActiveProjectTarget,
  useProjectDbStore,
  type ProjectDatabaseIdentity,
} from './projectDbStore'
import type { SqlConfirmRequest } from './SqlExecuteConfirmModal'
import type { InlineSqlRunRecord } from './InlineSqlResultCard'

function newQueryId(): string {
  return `dbxq-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
}

export function useDbxDirectExecEnabled(): boolean {
  return usePluginSettingsStore((s) => {
    if (!s.isPluginEnabled(DBX_PLUGIN_ID)) return false
    const provider = s.getPluginConfig<string>(DBX_PLUGIN_ID, 'provider', 'dbx')
    return provider !== 'custom-web'
  })
}

export function useProjectDatabaseContext(session?: Session | null, shouldHydrate = false) {
  const enabled = useDbxDirectExecEnabled()
  const projects = useAstrorderStore((s) => s.projects)
  const agents = useAstrorderStore((s) => s.agents)
  const bindings = useProjectDbStore((s) => s.bindings)
  const hydrated = useProjectDbStore((s) => s.hydrated)
  const hydrateBindings = useProjectDbStore((s) => s.hydrateBindings)
  const sessionKey = session ? scopeKey(session.agent_id, session.id) : ''
  const overrideTargetId = useProjectDbStore((s) => (sessionKey ? s.sessionTargetOverride[sessionKey] : undefined))
  const setSessionTargetOverride = useProjectDbStore((s) => s.setSessionTargetOverride)

  useEffect(() => {
    if (enabled && shouldHydrate && session && !hydrated) {
      void hydrateBindings()
    }
  }, [enabled, shouldHydrate, session, hydrated, hydrateBindings])

  const identity: ProjectDatabaseIdentity | null = useMemo(
    () => resolveProjectDatabaseIdentity(session, projects, agents),
    [session, projects, agents],
  )

  const binding = useMemo(() => {
    if (!identity) return null
    const exact = bindings.find((b) => b.project_key === identity.projectKey)
    if (exact) return exact
    if (identity.workspaceKey) {
      const lower = identity.workspaceKey.toLowerCase()
      return bindings.find((b) => b.workspace_key && b.workspace_key.toLowerCase() === lower) || null
    }
    return null
  }, [bindings, identity])

  const targets = binding?.targets || []
  const activeTarget = useMemo(
    () => selectActiveProjectTarget(binding, overrideTargetId),
    [binding, overrideTargetId],
  )

  const selectTarget = useCallback(
    (targetId: string) => {
      if (sessionKey) setSessionTargetOverride(sessionKey, targetId)
    },
    [sessionKey, setSessionTargetOverride],
  )

  return {
    enabled,
    identity,
    binding,
    targets,
    activeTarget,
    selectTarget,
  }
}

export interface PendingRunInput {
  runKey: string
  sql: string
  sourceKind: 'snippet' | 'file'
  sourceLabel?: string
  sourcePath?: string
  target?: ProjectDatabaseTarget
}

export function useInlineSqlExecutor(session?: Session | null, shouldHydrate = false) {
  const ctx = useProjectDatabaseContext(session, shouldHydrate)
  const [runs, setRuns] = useState<Record<string, InlineSqlRunRecord>>({})
  const [confirmState, setConfirmState] = useState<{
    runKey: string
    request: SqlConfirmRequest
  } | null>(null)
  const [bindModalOpened, setBindModalOpened] = useState(false)
  const [pendingAfterBind, setPendingAfterBind] = useState<PendingRunInput | null>(null)
  const [loadingFileKey, setLoadingFileKey] = useState<string | null>(null)
  const activeControllers = useRef<Map<string, { controller: AbortController; queryId: string }>>(new Map())

  useEffect(() => {
    const map = activeControllers.current
    return () => {
      for (const entry of map.values()) {
        entry.controller.abort()
      }
      map.clear()
    }
  }, [])

  const executeConfirmed = useCallback(
    async (
      runKey: string,
      input: {
        sql: string
        sourceKind: 'snippet' | 'file'
        sourceLabel?: string
        sourcePath?: string
        target: ProjectDatabaseTarget
        confirmed: boolean
      },
    ) => {
      const prev = activeControllers.current.get(runKey)
      if (prev) {
        prev.controller.abort()
        activeControllers.current.delete(runKey)
      }
      const controller = new AbortController()
      const queryId = newQueryId()
      activeControllers.current.set(runKey, { controller, queryId })

      const startedAt = Date.now()
      setRuns((curr) => ({
        ...curr,
        [runKey]: {
          id: runKey,
          sourceKind: input.sourceKind,
          sourceLabel: input.sourceLabel,
          sql: input.sql,
          target: input.target,
          running: true,
          startedAt,
        },
      }))

      try {
        const result = await api.executeDbxSql(
          {
            connection_id: input.target.connection_id,
            database: input.target.database || undefined,
            schema: input.target.schema || undefined,
            sql: input.sql,
            source_kind: input.sourceKind,
            source_name: input.sourceLabel || input.sourcePath,
            project_key: ctx.identity?.projectKey,
            workspace_key: ctx.identity?.workspaceKey,
            session_id: session?.id,
            confirmed: input.confirmed,
            max_rows: 200,
            timeout_secs: 30,
            query_id: queryId,
          },
          controller.signal,
        )
        if (controller.signal.aborted) return
        setRuns((curr) => ({
          ...curr,
          [runKey]: {
            id: runKey,
            sourceKind: input.sourceKind,
            sourceLabel: input.sourceLabel,
            sql: input.sql,
            target: input.target,
            running: false,
            result,
            startedAt,
          },
        }))
      } catch (err) {
        if (controller.signal.aborted) return
        // Server enforced confirmation (HTTP 428): open confirmation modal with server's assessment.
        if (err instanceof ApiError && err.status === 428) {
          setRuns((curr) => {
            const next = { ...curr }
            delete next[runKey]
            return next
          })
          setConfirmState({
            runKey,
            request: {
              sql: input.sql,
              sourceKind: input.sourceKind,
              sourceName: input.sourceLabel || 'SQL',
              sourcePath: input.sourcePath,
              target: { ...input.target, is_prod: true },
              classification: classifySql(input.sql),
            },
          })
          return
        }
        const message = err instanceof Error ? err.message : '执行 SQL 失败'
        setRuns((curr) => ({
          ...curr,
          [runKey]: {
            id: runKey,
            sourceKind: input.sourceKind,
            sourceLabel: input.sourceLabel,
            sql: input.sql,
            target: input.target,
            running: false,
            error: message,
            startedAt,
          },
        }))
      } finally {
        const active = activeControllers.current.get(runKey)
        if (active?.queryId === queryId) {
          activeControllers.current.delete(runKey)
        }
      }
    },
    [ctx.identity?.projectKey, ctx.identity?.workspaceKey, session?.id],
  )

  const triggerRun = useCallback(
    (input: PendingRunInput) => {
      const sql = input.sql.replace(/\r\n/g, '\n').trim()
      if (!sql) {
        notifications.show({ color: 'yellow', message: 'SQL 内容为空' })
        return
      }
      const target = input.target || ctx.activeTarget
      if (!target) {
        setPendingAfterBind({ ...input, sql })
        setBindModalOpened(true)
        return
      }
      const classification = classifySql(sql)
      if (requiresExecutionConfirmation(sql, target.is_prod)) {
        setConfirmState({
          runKey: input.runKey,
          request: {
            sql,
            sourceKind: input.sourceKind,
            sourceName: input.sourceLabel || 'SQL',
            sourcePath: input.sourcePath,
            target,
            classification,
          },
        })
        return
      }
      void executeConfirmed(input.runKey, {
        sql,
        sourceKind: input.sourceKind,
        sourceLabel: input.sourceLabel,
        sourcePath: input.sourcePath,
        target,
        confirmed: false,
      })
    },
    [ctx.activeTarget, executeConfirmed],
  )

  const triggerFileRun = useCallback(
    async (rawPath: string) => {
      const cleanPath = stripSqlLocationSuffix(rawPath)
      if (!cleanPath) {
        notifications.show({ color: 'red', message: '无法识别有效的 SQL 文件路径' })
        return
      }
      const filename = extractSqlFileName(cleanPath)
      const runKey = `file:${cleanPath}`
      setLoadingFileKey(runKey)
      try {
        const sqlText = await readSqlText(cleanPath, session?.id, session?.connection_id || undefined)
        triggerRun({
          runKey,
          sql: sqlText,
          sourceKind: 'file',
          sourceLabel: filename,
          sourcePath: cleanPath,
        })
      } catch (err) {
        notifications.show({
          color: 'red',
          title: `读取 ${filename} 失败`,
          message: err instanceof Error ? err.message : '请确认文件存在且有权限读取',
        })
      } finally {
        setLoadingFileKey((curr) => (curr === runKey ? null : curr))
      }
    },
    [session?.connection_id, session?.id, triggerRun],
  )

  const cancelRun = useCallback((runKey: string) => {
    const active = activeControllers.current.get(runKey)
    if (active) {
      active.controller.abort()
      activeControllers.current.delete(runKey)
      void api.cancelDbxSql(active.queryId).catch(() => {})
    }
    setRuns((curr) => {
      const existing = curr[runKey]
      if (!existing) return curr
      return {
        ...curr,
        [runKey]: { ...existing, running: false, error: '已取消执行' },
      }
    })
  }, [])

  const closeRun = useCallback((runKey: string) => {
    const active = activeControllers.current.get(runKey)
    if (active) {
      active.controller.abort()
      activeControllers.current.delete(runKey)
    }
    setRuns((curr) => {
      const next = { ...curr }
      delete next[runKey]
      return next
    })
  }, [])

  const confirmAndRun = useCallback(() => {
    if (!confirmState) return
    const { runKey, request } = confirmState
    setConfirmState(null)
    void executeConfirmed(runKey, {
      sql: request.sql,
      sourceKind: request.sourceKind,
      sourceLabel: request.sourceKind === 'file' ? request.sourceName : undefined,
      sourcePath: request.sourcePath,
      target: request.target,
      confirmed: true,
    })
  }, [confirmState, executeConfirmed])

  const changeConfirmTarget = useCallback(
    (targetId: string) => {
      const nextTarget = ctx.targets.find((t) => t.id === targetId)
      if (!nextTarget) return
      ctx.selectTarget(targetId)
      setConfirmState((curr) =>
        curr ? { ...curr, request: { ...curr.request, target: nextTarget } } : null,
      )
    },
    [ctx],
  )

  const handleBindModalSaved = useCallback(
    (savedTargets: ProjectDatabaseTarget[]) => {
      const pending = pendingAfterBind
      setPendingAfterBind(null)
      if (!pending || savedTargets.length === 0) return
      const chosen = savedTargets.find((t) => t.is_default) || savedTargets[0]
      if (chosen) {
        triggerRun({ ...pending, target: chosen })
      }
    },
    [pendingAfterBind, triggerRun],
  )

  return {
    ...ctx,
    runs,
    loadingFileKey,
    confirmState,
    setConfirmState,
    bindModalOpened,
    setBindModalOpened,
    triggerRun,
    triggerFileRun,
    cancelRun,
    closeRun,
    confirmAndRun,
    changeConfirmTarget,
    handleBindModalSaved,
  }
}
