export const PROJECT_ORDER_KEY = 'astrorder:project_order'

export function readProjectOrder(): string[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(PROJECT_ORDER_KEY) || '[]')
    return Array.isArray(value) ? [...new Set(value.filter((key): key is string => typeof key === 'string'))] : []
  } catch { return [] }
}

// Incoming keys are alphabetic only for first placement. Missing sources keep their slots.
export function reconcileProjectOrder(saved: string[], incoming: string[]): string[] {
  return [...new Set([...saved, ...incoming])]
}

export function moveProject(order: string[], source: string, target: string): string[] {
  if (source === target || !order.includes(source) || !order.includes(target)) return order
  const next = order.filter(key => key !== source)
  next.splice(order.indexOf(target), 0, source)
  return next
}
