const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const { createHash } = require('node:crypto')
const asar = require('@electron/asar')

// ASAR headers record entry names with the separator style of the machine that
// wrote the archive. Resolve the exact spelling the real archive API accepts
// instead of assuming one platform convention, and keep a stable logical name
// for messages and layout checks.
function resolveArchiveEntry(archivePath, entry) {
  const stripped = String(entry).replace(/^[\\/]+/, '')
  const candidates = []
  for (const candidate of [stripped, stripped.replaceAll('\\', '/'), stripped.replaceAll('/', '\\')]) {
    if (candidate !== '' && !candidates.includes(candidate)) candidates.push(candidate)
  }
  let lastError
  for (const candidate of candidates) {
    try {
      return {
        name: candidate,
        logical: candidate.replaceAll('\\', '/'),
        node: asar.statFile(archivePath, candidate, true),
        raw: asar.statFile(archivePath, candidate, false),
      }
    } catch (error) {
      lastError = error
    }
  }
  throw lastError
}

function align4(value) {
  return value + ((4 - (value % 4)) % 4)
}

// verifyDesktopArchive walks every archive entry through the real ASAR APIs and
// refuses an archive Electron could not load: a header that misstates its own
// size, entries whose payload bytes disagree with their recorded length and
// integrity, a payload layout that does not tile the file exactly, or startup
// JavaScript that cannot be parsed.
function verifyDesktopArchive(archive) {
  const archivePath = path.resolve(archive)
  if (!fs.existsSync(archivePath)) throw new Error(`desktop archive is missing: ${archivePath}`)
  asar.uncacheAll()

  let header
  let entries
  try {
    // Reads the pickle size field and parses the header JSON. A header whose
    // recorded size disagrees with its real payload fails here.
    header = asar.getRawHeader(archivePath)
    entries = asar.listPackage(archivePath)
  } catch (error) {
    throw new Error(`desktop archive header is invalid: ${archivePath}: ${error.message}`)
  }
  const jsonBytes = Buffer.byteLength(header.headerString, 'utf8')
  const expectedHeaderSize = 4 + align4(4 + jsonBytes)
  if (header.headerSize !== expectedHeaderSize) {
    throw new Error(`desktop archive header is invalid: ${archivePath}: recorded header size ${header.headerSize} does not match its payload ${expectedHeaderSize}`)
  }
  const payloadLength = fs.statSync(archivePath).size - 8 - header.headerSize
  if (payloadLength < 0) throw new Error(`desktop archive header is invalid: ${archivePath}: header extends past the end of the file`)

  let files = 0
  const names = new Set()
  const packed = []
  for (const entry of entries) {
    let resolved
    try {
      resolved = resolveArchiveEntry(archivePath, entry)
    } catch (error) {
      throw new Error(`desktop archive entry is unreadable: ${entry}: ${error.message}`)
    }
    const { name, logical, node, raw: rawNode } = resolved
    if (node.files) continue
    let bytes
    try {
      // Handles packed payloads from the archive and unpacked payloads from
      // the adjacent app.asar.unpacked directory; a missing payload fails here.
      bytes = asar.extractFile(archivePath, name)
    } catch (error) {
      throw new Error(`desktop archive entry is unreadable: ${logical}: ${error.message}`)
    }
    const hash = createHash('sha256').update(bytes).digest('hex')
    if (bytes.length !== node.size || node.integrity?.algorithm !== 'SHA256' || hash !== node.integrity.hash) {
      throw new Error(`desktop archive integrity mismatch: ${logical}`)
    }
    if (logical.endsWith('.js')) {
      try {
        new vm.Script(bytes.toString('utf8'), { filename: logical })
      } catch (error) {
        throw new Error(`desktop startup syntax invalid: ${logical}: ${error.message}`)
      }
    }
    names.add(logical)
    files += 1
    if (!node.unpacked && !rawNode.link) packed.push({ logical, offset: Number(node.offset), size: node.size })
  }

  // Packed payloads must tile the payload region exactly. The recorded
  // size/offset skew that made the installed archive unloadable surfaces here
  // as an extent that cannot be satisfied by the bytes actually present.
  packed.sort((left, right) => left.offset - right.offset)
  let cursor = 0
  for (const file of packed) {
    if (file.offset !== cursor) {
      throw new Error(`desktop archive layout mismatch: ${file.logical} starts at ${file.offset} but the previous payload ends at ${cursor}`)
    }
    cursor += file.size
  }
  if (cursor !== payloadLength) {
    throw new Error(`desktop archive layout mismatch: recorded payload extent ${cursor} does not match the ${payloadLength} bytes present`)
  }

  if (!names.has('package.json')) throw new Error('desktop archive entry package.json is missing')
  let metadata
  try {
    metadata = JSON.parse(asar.extractFile(archivePath, 'package.json').toString('utf8'))
  } catch (error) {
    throw new Error(`desktop archive entry package.json is invalid: ${error.message}`)
  }
  const main = typeof metadata.main === 'string' ? metadata.main.replaceAll('\\', '/').replace(/^\.\//, '') : ''
  if (main === '' || !names.has(main)) throw new Error(`desktop archive entry is missing: ${metadata.main}`)
  return { archive: archivePath, files, main }
}

function afterPack(context) {
  if (context.electronPlatformName !== 'win32') return
  const result = verifyDesktopArchive(path.join(context.appOutDir, 'resources', 'app.asar'))
  console.log(JSON.stringify({ desktopArchiveVerified: true, ...result }))
}

module.exports = afterPack
module.exports.afterPack = afterPack
module.exports.verifyDesktopArchive = verifyDesktopArchive
if (require.main === module) {
  try { console.log(JSON.stringify(verifyDesktopArchive(process.argv[2]))) } catch (error) {
    console.error(error.message)
    process.exitCode = 1
  }
}
