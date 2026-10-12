const assert = require('node:assert/strict')
const { test } = require('node:test')
const fs = require('node:fs/promises')
const path = require('node:path')
const os = require('node:os')
const asar = require('@electron/asar')
const verifyModule = require('./verify-desktop-archive')
const { verifyDesktopArchive } = verifyModule

async function archiveFixture(t, main = 'module.exports = {}\n') {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'astrorder-asar-test-'))
  t.after(() => fs.rm(root, { recursive: true, force: true }))
  const source = path.join(root, 'source')
  await fs.mkdir(source)
  await fs.writeFile(path.join(source, 'package.json'), JSON.stringify({ name: 'private-fixture', version: '1.0.0', main: 'main.js' }))
  await fs.writeFile(path.join(source, 'main.js'), main)
  const archive = path.join(root, 'app.asar')
  await asar.createPackage(source, archive)
  return archive
}

test('desktop archive verifies Electron entry JSON and payload integrity', async t => {
  const archive = await archiveFixture(t)
  const result = verifyDesktopArchive(archive)
  assert.equal(result.main, 'main.js')
  assert.equal(result.files, 2)
})

test('desktop archive verifies nested entries with archive-native separators', async t => {
  const archive = await archiveFixture(t)
  const source = path.join(path.dirname(archive), 'nested')
  await fs.mkdir(path.join(source, 'lib', 'deep'), { recursive: true })
  await fs.writeFile(path.join(source, 'package.json'), JSON.stringify({ name: 'private-fixture', version: '1.0.0', main: 'main.js' }))
  await fs.writeFile(path.join(source, 'main.js'), 'module.exports = {}\n')
  await fs.writeFile(path.join(source, 'lib', 'deep', 'inner.js'), 'module.exports = {}\n')
  await asar.createPackage(source, archive)
  asar.uncacheAll()
  const result = verifyDesktopArchive(archive)
  assert.equal(result.files, 3)
})

test('desktop archive verifies unpacked payloads from the adjacent directory', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'astrorder-asar-unpacked-'))
  t.after(() => fs.rm(root, { recursive: true, force: true }))
  const source = path.join(root, 'source')
  await fs.mkdir(path.join(source, 'lib'), { recursive: true })
  await fs.writeFile(path.join(source, 'package.json'), JSON.stringify({ name: 'private-fixture', version: '1.0.0', main: 'main.js' }))
  await fs.writeFile(path.join(source, 'main.js'), 'module.exports = {}\n')
  await fs.writeFile(path.join(source, 'lib', 'native.node'), Buffer.alloc(4096, 7))
  const archive = path.join(root, 'app.asar')
  await asar.createPackageWithOptions(source, archive, { unpack: '*.node' })
  asar.uncacheAll()
  const result = verifyDesktopArchive(archive)
  assert.equal(result.files, 3)

  await fs.rm(path.join(`${archive}.unpacked`, 'lib', 'native.node'))
  asar.uncacheAll()
  assert.throws(() => verifyDesktopArchive(archive), /unreadable.*lib[/\\]native\.node/)
})

test('desktop archive rejects payload corruption despite a readable header', async t => {
  const archive = await archiveFixture(t)
  const file = asar.statFile(archive, 'main.js')
  const raw = await fs.readFile(archive)
  raw[8 + asar.getRawHeader(archive).headerSize + Number(file.offset)] ^= 1
  await fs.writeFile(archive, raw)
  asar.uncacheAll()
  assert.throws(() => verifyDesktopArchive(archive), /integrity.*main\.js/)
})

test('desktop archive rejects the size and offset skew that broke the installed build', async t => {
  const archive = await archiveFixture(t)
  const source = path.join(path.dirname(archive), 'skew')
  await fs.mkdir(source)
  await fs.writeFile(path.join(source, 'package.json'), JSON.stringify({ name: 'private-fixture', version: '1.0.0', main: 'main.js' }))
  await fs.writeFile(path.join(source, 'main.js'), 'm'.repeat(2222))
  await fs.writeFile(path.join(source, 'zz.txt'), 'z'.repeat(1500))
  await asar.createPackage(source, archive)
  asar.uncacheAll()

  // Record main.js as two bytes longer and shift every later entry to match,
  // the exact skew observed in the broken installed archive.
  const info = asar.getRawHeader(archive)
  const header = JSON.parse(JSON.stringify(info.header))
  const main = header.files['main.js']
  const originalSize = main.size
  main.size = originalSize + 2
  const shiftFrom = Number(main.offset) + originalSize
  for (const node of Object.values(header.files)) {
    if (node.files || node.offset === undefined) continue
    if (Number(node.offset) >= shiftFrom) node.offset = String(Number(node.offset) + 2)
  }
  const json = JSON.stringify(header)
  assert.equal(Buffer.byteLength(json, 'utf8'), Buffer.byteLength(info.headerString, 'utf8'))
  const raw = await fs.readFile(archive)
  await fs.writeFile(archive, Buffer.concat([raw.subarray(0, 16), Buffer.from(json, 'utf8'), raw.subarray(16 + Buffer.byteLength(info.headerString, 'utf8'))]))
  asar.uncacheAll()
  assert.throws(() => verifyDesktopArchive(archive), /integrity|layout/)
})

test('desktop archive rejects trailing bytes that break the recorded payload extent', async t => {
  const archive = await archiveFixture(t)
  await fs.appendFile(archive, Buffer.alloc(2, 1))
  asar.uncacheAll()
  assert.throws(() => verifyDesktopArchive(archive), /layout mismatch.*extent/)
})

test('desktop archive rejects a header whose size field disagrees with its payload', async t => {
  const archive = await archiveFixture(t)
  const raw = await fs.readFile(archive)
  raw.writeUInt32LE(raw.readUInt32LE(4) + 2, 4)
  await fs.writeFile(archive, raw)
  asar.uncacheAll()
  assert.throws(() => verifyDesktopArchive(archive), /header/)
})

test('desktop archive rejects syntactically invalid startup code', async t => {
  const archive = await archiveFixture(t, 'const incomplete = {\n')
  assert.throws(() => verifyDesktopArchive(archive), /startup syntax.*main\.js/)
})

test('desktop archive rejects missing declared Electron entry', async t => {
  const archive = await archiveFixture(t)
  const info = asar.getRawHeader(archive)
  const entry = info.header.files['package.json']
  const raw = await fs.readFile(archive)
  const offset = 8 + info.headerSize + Number(entry.offset)
  const text = raw.subarray(offset, offset + entry.size).toString().replace('main.js', 'lost.js')

  // Rebuild from files so the fixture has matching metadata but no entry.
  const source = path.join(path.dirname(archive), 'missing-entry')
  await fs.mkdir(source)
  await fs.writeFile(path.join(source, 'package.json'), text)
  await fs.writeFile(path.join(source, 'main.js'), 'module.exports = {}\n')
  await asar.createPackage(source, archive)
  asar.uncacheAll()
  assert.throws(() => verifyDesktopArchive(archive), /entry.*lost\.js/)
})

test('afterPack hook verifies the packed archive and fails the win32 build on corruption', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'astrorder-asar-hook-'))
  t.after(() => fs.rm(root, { recursive: true, force: true }))
  const appOutDir = path.join(root, 'win-unpacked')
  await fs.mkdir(path.join(appOutDir, 'resources'), { recursive: true })

  // electron-builder resolves the hook by its named export.
  assert.equal(typeof verifyModule.afterPack, 'function')
  assert.equal(verifyModule.afterPack, verifyModule)

  const source = path.join(root, 'source')
  await fs.mkdir(source)
  await fs.writeFile(path.join(source, 'package.json'), JSON.stringify({ name: 'private-fixture', version: '1.0.0', main: 'main.js' }))
  await fs.writeFile(path.join(source, 'main.js'), 'module.exports = {}\n')
  const archive = path.join(appOutDir, 'resources', 'app.asar')
  await asar.createPackage(source, archive)
  asar.uncacheAll()
  assert.doesNotThrow(() => verifyModule.afterPack({ electronPlatformName: 'win32', appOutDir }))

  // Non-Windows targets are untouched.
  assert.doesNotThrow(() => verifyModule.afterPack({ electronPlatformName: 'darwin', appOutDir: path.join(root, 'missing') }))

  const file = asar.statFile(archive, 'main.js')
  const raw = await fs.readFile(archive)
  raw[8 + asar.getRawHeader(archive).headerSize + Number(file.offset)] ^= 1
  await fs.writeFile(archive, raw)
  asar.uncacheAll()
  assert.throws(() => verifyModule.afterPack({ electronPlatformName: 'win32', appOutDir }), /integrity/)
})
