/// <reference types="node" />

import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, it } from 'vitest'

it('installs at the application root, never at a selected native session URL', () => {
  const file = resolve('public/manifest.webmanifest')
  expect(existsSync(file)).toBe(true)
  const manifest = JSON.parse(readFileSync(file, 'utf8'))
  expect(manifest.start_url).toBe('/')
  expect(manifest.scope).toBe('/')
  expect(manifest.display).toBe('standalone')
  expect(manifest.display_override).toEqual(['window-controls-overlay'])
  expect(manifest.icons.map((icon: { sizes: string }) => icon.sizes)).toContain('192x192')
  expect(manifest.icons.map((icon: { sizes: string }) => icon.sizes)).toContain('512x512')
  for (const icon of manifest.icons) expect(existsSync(resolve('public', icon.src.slice(1)))).toBe(true)
  const html = readFileSync(resolve('index.html'), 'utf8')
  expect(html).toContain('rel="manifest"')
  expect(html).toContain('viewport-fit=cover')
  expect(html).toContain('apple-touch-icon')
  expect(html).toContain('/favicon.png')
  expect(existsSync(resolve('public/favicon.png'))).toBe(true)
  expect(existsSync(resolve('public/favicon.ico'))).toBe(true)
  expect(existsSync(resolve('public/apple-touch-icon.png'))).toBe(true)
})
