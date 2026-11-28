import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * Regression guard for Chromium 158+ / Edge WebView2 renderer crash
 * (STATUS_BREAKPOINT 0x80000003 in Blink AXBlockFlowData::ComputeNeighborOnLine,
 * CHECK_EQ(it->Type(), FragmentItem::kGeneratedText) / crbug.com/461193182).
 *
 * When Windows UIA enables kInlineTextBoxes in WebView2 and async markdown
 * auto-linking mutates text inside display-locked (content-visibility: auto)
 * off-screen transcript turns, Blink aborts the renderer process and WebView2
 * reloads the SPA from scratch.
 */
describe('content-visibility: auto regression guard', () => {
  const checkedStylesheets = [
    resolve(__dirname, '../index.css'),
    resolve(__dirname, '../features/mobile/mobile.css'),
  ]

  for (const filePath of checkedStylesheets) {
    it(`does not use content-visibility: auto in ${filePath.split('/').slice(-2).join('/')}`, () => {
      const css = readFileSync(filePath, 'utf8')
      const withoutComments = css.replace(/\/\*[\s\S]*?\*\//g, '')
      expect(withoutComments).not.toMatch(/content-visibility\s*:\s*auto\b/i)
    })
  }
})
