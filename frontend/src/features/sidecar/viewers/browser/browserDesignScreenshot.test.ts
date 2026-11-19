import { expect, it } from 'vitest'
import { designSelectionBox } from './browserDesignScreenshot'

it('maps CSS selection coordinates to screenshot pixels for scaled and high density captures', () => {
  const rect = { x: 100, y: 80, width: 160, height: 40 }
  const viewport = { width: 1280, height: 800 }
  expect(designSelectionBox(rect, viewport, viewport)).toEqual({ x: 96, y: 76, width: 168, height: 48, lineWidth: 3 })
  expect(designSelectionBox(rect, viewport, { width: 2560, height: 1600 })).toEqual({ x: 192, y: 152, width: 336, height: 96, lineWidth: 6 })
  expect(designSelectionBox(rect, viewport, { width: 640, height: 400 })).toEqual({ x: 48, y: 38, width: 84, height: 24, lineWidth: 3 })
})

it('keeps the entire red stroke visible for a selection partly outside the viewport', () => {
  const box = designSelectionBox({ x: -10, y: 790, width: 1300, height: 50 }, { width: 1280, height: 800 }, { width: 1280, height: 800 })
  expect(box).toEqual({ x: 1.5, y: 786, width: 1277, height: 12.5, lineWidth: 3 })
})

it('reports stale or invalid selections instead of silently exporting an unmarked screenshot', () => {
  const viewport = { width: 1280, height: 800 }
  expect(() => designSelectionBox({ x: 1281, y: 20, width: 80, height: 40 }, viewport, viewport)).toThrow('离开预览范围')
  expect(() => designSelectionBox({ x: 20, y: 20, width: NaN, height: 40 }, viewport, viewport)).toThrow('尺寸无效')
  expect(() => designSelectionBox({ x: 20, y: 20, width: 80, height: 0 }, viewport, viewport)).toThrow('尺寸无效')
})
