import { beforeEach, describe, expect, it, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import type { BrowserDesignCommand, BrowserDesignState } from './browserDesign'

const runtime = readFileSync(resolve(process.cwd(), '../app-server-go/internal/browser/designer_runtime.js'), 'utf8')
const design = new Function(`return (${runtime})`)() as (command: BrowserDesignCommand) => BrowserDesignState & { error?: string }
const stateKey = Symbol.for('astrorder.browserDesigner.v1')

beforeEach(() => {
  delete (window as unknown as Record<symbol, unknown>)[stateKey]
  document.body.innerHTML = '<main id="workspace"><button id="save" style="font-size: 16px; color: rgb(10, 20, 30) !important; opacity: .8">保存</button></main>'
  Object.defineProperty(document, 'elementFromPoint', { configurable: true, value: vi.fn(() => document.querySelector('#save')) })
  vi.stubGlobal('CSS', { escape: (value: string) => value.replace(/[^a-zA-Z0-9_-]/g, character => '\\' + character), supports: () => true })
})

function select() {
  const initial = design({ action: 'state' })
  return design({ action: 'select', document_id: initial.document_id, ratio_x: .5, ratio_y: .5 })
}

function apply(state: BrowserDesignState, property: 'font-size' | 'width' | 'color', value: string) {
  return design({ action: 'apply', document_id: state.document_id, element_id: state.selected!.id, property, value })
}

describe('browser design DOM editing', () => {
  it('selects without clicking or navigating and retains element context', () => {
    const click = vi.fn()
    document.querySelector('button')!.addEventListener('click', click)
    const state = select()
    expect(state.selected).toMatchObject({ selector: '#save', tag: 'button', text: '保存' })
    expect(click).not.toHaveBeenCalled()
  })

  it('records aggregate changes, undoes in order, and restores inline priority', () => {
    let state = select()
    state = apply(state, 'font-size', '20px')
    state = apply(state, 'font-size', '24px')
    state = apply(state, 'color', '#ff0000')
    expect(state.changes[0].properties).toEqual([
      { property: 'font-size', before: '16px', after: '24px' },
      { property: 'color', before: 'rgb(10, 20, 30)', after: 'rgb(255, 0, 0)' },
    ])
    state = design({ action: 'undo', document_id: state.document_id })
    const node = document.querySelector<HTMLElement>('#save')!
    expect(node.style.color).toBe('rgb(10, 20, 30)')
    expect(node.style.getPropertyPriority('color')).toBe('important')
    state = design({ action: 'undo', document_id: state.document_id })
    expect(node.style.fontSize).toBe('20px')
    state = design({ action: 'reset', document_id: state.document_id })
    expect(node.style.fontSize).toBe('16px')
    expect(node.style.opacity).toBe('0.8')
    expect(state.changes).toEqual([])
    expect(state.can_undo).toBe(false)
  })

  it('removes a change when the original computed value is restored', () => {
    let state = select()
    state = apply(state, 'font-size', '24px')
    state = apply(state, 'font-size', '16px')
    expect(state.changes).toEqual([])
    expect(document.querySelector<HTMLElement>('#save')!.style.getPropertyPriority('font-size')).toBe('')
    state = apply(state, 'color', '#ff0000')
    state = apply(state, 'color', '#0a141e')
    expect(state.changes).toEqual([])
    expect(document.querySelector<HTMLElement>('#save')!.style.getPropertyPriority('color')).toBe('important')
  })

  it('refuses stale documents, stale selections and detached nodes', () => {
    const state = select()
    expect(design({ action: 'apply', document_id: 'old-document', element_id: state.selected!.id, property: 'width', value: '200px' }).error).toContain('页面已变化')
    design({ action: 'parent', document_id: state.document_id })
    expect(apply(state, 'width', '200px').error).toContain('选取的元素已变化')
    const selected = select()
    document.querySelector('#save')!.remove()
    expect(apply(selected, 'width', '200px').error).toContain('页面元素已变化')
  })

  it('rejects exporting disconnected edited elements and does not undo page-owned modifications', () => {
    const state = apply(select(), 'font-size', '24px')
    document.querySelector<HTMLElement>('#save')!.style.fontSize = '30px'
    expect(design({ action: 'undo', document_id: state.document_id }).error).toContain('页面已修改该样式')
    document.querySelector('#save')!.remove()
    expect(design({ action: 'export', document_id: state.document_id }).error).toContain('页面元素已变化')
  })
})
