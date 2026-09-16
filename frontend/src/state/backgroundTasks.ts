import { create } from 'zustand'

export type BackgroundTask = {
  id: string
  title: string
  detail: string
  status: 'running' | 'completed' | 'failed' | 'cancelled'
  createdAt: number
  actionLabel?: string
  action?: () => void
  cancel: () => void | Promise<void>
}

type BackgroundTaskStore = {
  tasks: Record<string, BackgroundTask>
  add: (task: Omit<BackgroundTask, 'id' | 'status' | 'createdAt'>) => string
  complete: (id: string, detail: string, action?: Pick<BackgroundTask, 'actionLabel' | 'action'>) => void
  fail: (id: string, detail: string) => void
  cancel: (id: string) => void
  dismiss: (id: string) => void
}

let nextTaskId = 0

export const useBackgroundTasks = create<BackgroundTaskStore>((set) => ({
  tasks: {},
  add: (task) => {
    const id = `background-task-${Date.now()}-${++nextTaskId}`
    set((state) => ({
      tasks: { ...state.tasks, [id]: { ...task, id, status: 'running', createdAt: Date.now() } },
    }))
    return id
  },
  complete: (id, detail, action) => set((state) => state.tasks[id]?.status === 'running' ? ({
    tasks: { ...state.tasks, [id]: { ...state.tasks[id], ...action, detail, status: 'completed' } },
  }) : state),
  fail: (id, detail) => set((state) => state.tasks[id]?.status === 'running' ? ({
    tasks: { ...state.tasks, [id]: { ...state.tasks[id], detail, status: 'failed' } },
  }) : state),
  cancel: (id) => set((state) => {
    const task = state.tasks[id]
    if (!task || task.status !== 'running') return state
    void Promise.resolve(task.cancel()).catch((error) => {
      set((latest) => latest.tasks[id]?.status === 'cancelled' ? ({
        tasks: { ...latest.tasks, [id]: { ...latest.tasks[id], status: 'failed', detail: error instanceof Error ? error.message : '取消任务失败' } },
      }) : latest)
    })
    return { tasks: { ...state.tasks, [id]: { ...task, status: 'cancelled', detail: '已取消' } } }
  }),
  dismiss: (id) => set((state) => {
    const tasks = { ...state.tasks }
    delete tasks[id]
    return { tasks }
  }),
}))
