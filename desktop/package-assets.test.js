const assert = require('node:assert/strict')
const { test } = require('node:test')
const { existsSync, readdirSync, readFileSync } = require('node:fs')
const path = require('node:path')
const config = require('./package.json')

test('desktop control plane packages no legacy Python App runtime', () => {
  for (const asset of config.build.extraFiles) {
    assert.doesNotMatch(asset.from, /backend|\.venv|scripts\/run_production/)
    assert.doesNotMatch(asset.to, /backend|\.venv/)
  }
  for (const file of ['main.js', 'service-startup.js']) {
    assert.doesNotMatch(readFileSync(path.join(__dirname, file), 'utf8'), /uvicorn|run_production\.py|pythonw\.exe/)
  }
  const packaged = path.join(__dirname, 'release', 'win-unpacked', 'server')
  if (existsSync(packaged)) {
    for (const entry of readdirSync(packaged)) {
      assert.doesNotMatch(entry, /^(backend|\.venv|python(?:w)?\.exe)$/i)
    }
  }
})

test('packaged Hermes loader includes its standalone connector modules', () => {
  const entry = config.build.extraFiles.find(asset => asset.to === 'server/connectors/hermes/astrorder_hermes_plugin')
  assert.ok(entry, 'Hermes loader dependency is absent from the package')
  for (const file of ['__init__.py', 'config.py', 'transport.py']) {
    assert.ok(existsSync(path.resolve(__dirname, entry.from, file)), `missing connector module: ${file}`)
  }
  assert.deepEqual(entry.filter, ['*.py', 'py.typed'])
})
