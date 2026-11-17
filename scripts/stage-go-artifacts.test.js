const assert = require('node:assert/strict')
const { test } = require('node:test')
const { mkdtemp, mkdir, writeFile, readFile, rm } = require('node:fs/promises')
const { createHash } = require('node:crypto')
const os = require('node:os')
const path = require('node:path')
const { requiredArtifacts, stageArtifacts, verifyArtifacts } = require('./stage-go-artifacts')

async function candidateFixture(t) {
  const root = await mkdtemp(path.join(process.env.TMPDIR || os.tmpdir(), 'astrorder-stage-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  const source = path.join(root, 'candidate')
  const lines = []
  for (const name of requiredArtifacts) {
    const file = path.join(source, name)
    const bytes = Buffer.from(`fixture artifact ${name}`)
    await mkdir(path.dirname(file), { recursive: true })
    await writeFile(file, bytes)
    const digest = createHash('sha256').update(bytes).digest('hex')
    lines.push(`${digest}  ${name}`)
    if (name.endsWith('/astrorder-model-helper')) {
      await writeFile(path.join(path.dirname(file), 'SHA256SUMS'), `${digest}  astrorder-model-helper\n`)
    }
  }
  await writeFile(path.join(source, 'SHA256SUMS'), lines.join('\n') + '\n')
  return { source, destination: path.join(root, 'stage') }
}

test('stages complete candidate with target helper checksums', async t => {
  const { source, destination } = await candidateFixture(t)
  const result = await stageArtifacts(source, destination)
  assert.equal(result.artifacts.length, requiredArtifacts.length)
  assert.equal((await verifyArtifacts(destination)).length, requiredArtifacts.length)
  for (const name of requiredArtifacts) {
    assert.deepEqual(await readFile(path.join(destination, name)), await readFile(path.join(source, name)))
  }
})

test('damaged candidate cannot replace the prior staged binaries', async t => {
  const { source, destination } = await candidateFixture(t)
  await stageArtifacts(source, destination)
  const previous = await readFile(path.join(destination, 'astrorder-app.exe'))
  await writeFile(path.join(source, 'astrorder-app.exe'), 'damaged')
  await assert.rejects(stageArtifacts(source, destination), /checksum/)
  assert.deepEqual(await readFile(path.join(destination, 'astrorder-app.exe')), previous)
})

test('missing binary and escaping manifest paths are rejected', async t => {
  const { source, destination } = await candidateFixture(t)
  await rm(path.join(source, 'astrorder-sessiond.exe'))
  await assert.rejects(stageArtifacts(source, destination), /astrorder-sessiond/)
  await writeFile(path.join(source, 'SHA256SUMS'), `${'0'.repeat(64)}  ../outside.exe\n`)
  await assert.rejects(stageArtifacts(source, destination), /manifest|artifact/)
})

test('staging cannot replace an ancestor containing its source', async t => {
  const { source } = await candidateFixture(t)
  await assert.rejects(stageArtifacts(source, path.dirname(source)), /overlap/)
  assert.equal((await verifyArtifacts(source)).length, requiredArtifacts.length)
})
