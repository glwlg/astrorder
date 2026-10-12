const assert = require('node:assert/strict')
const { test } = require('node:test')
const { startServices } = require('./service-startup')

test('first launch starts both services without a login task', async () => {
  const calls = []
  const state = await startServices(async (target, action) => {
    calls.push([target, action])
    return { ok: true, state: 'running', startup_task: 'not_installed' }
  })
  assert.deepEqual(calls, [['daemon', 'start'], ['app', 'start']])
  assert.equal(state.app.state, 'running')
  assert.equal(state.daemon.startup_task, 'not_installed')
})

test('daemon refusal prevents App startup', async () => {
  const calls = []
  await assert.rejects(startServices(async target => {
    calls.push(target)
    return { ok: false, state: 'conflict', message: 'daemon conflict' }
  }), /daemon conflict/)
  assert.deepEqual(calls, ['daemon'])
})

test('App failure leaves the independently running daemon alone', async () => {
  const calls = []
  await assert.rejects(startServices(async (target, action) => {
    calls.push([target, action])
    if (target === 'app') throw new Error('App startup failed')
    return { ok: true, state: 'running' }
  }), /App startup failed/)
  assert.deepEqual(calls, [['daemon', 'start'], ['app', 'start']])
})
