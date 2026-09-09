/// <reference types="node" />

import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, it } from 'vitest'

it('installs at the mobile root, never at a selected native session URL', () => {
  const file = resolve('public/manifest.webmanifest')
  expect(existsSync(file)).toBe(true)
  const manifest = JSON.parse(readFileSync(file, 'utf8'))
  expect(manifest.start_url).toBe('/mobile')
  expect(manifest.scope).toBe('/mobile')
  expect(manifest.display).toBe('standalone')
  expect(manifest.icons.map((icon: { sizes: string }) => icon.sizes)).toContain('192x192')
  expect(manifest.icons.map((icon: { sizes: string }) => icon.sizes)).toContain('512x512')
  for (const icon of manifest.icons) expect(existsSync(resolve('public', icon.src.slice(1)))).toBe(true)
  const html = readFileSync(resolve('index.html'), 'utf8')
  expect(html).toContain('rel="manifest"')
  expect(html).toContain('viewport-fit=cover')
  expect(html).toContain('apple-touch-icon')
})
