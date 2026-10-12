const { createReadStream } = require('node:fs')
const fs = require('node:fs/promises')
const path = require('node:path')
const { createHash } = require('node:crypto')
const { spawn } = require('node:child_process')

const requiredArtifacts = Object.freeze([
  'astrorder-sessiond.exe', 'astrorder-app.exe', 'astrorderctl.exe',
  'astrorder-agent.exe', 'astrorder-mcp.exe', 'astrorder-helper.exe',
  'astrorder-model-helper.exe', 'astrorder-memory-helper.exe',
  'helpers/linux-amd64/astrorder-model-helper',
  'helpers/linux-arm64/astrorder-model-helper',
  'helpers/linux-amd64/astrorder-helper',
  'helpers/linux-arm64/astrorder-helper',
])

async function hashFile(file) {
  const hash = createHash('sha256')
  for await (const chunk of createReadStream(file)) hash.update(chunk)
  return hash.digest('hex')
}

async function regularFile(root, relative) {
  let current = root
  for (const part of relative.split('/')) {
    current = path.join(current, part)
    const info = await fs.lstat(current)
    if (info.isSymbolicLink()) throw new Error(`artifact must not traverse a link: ${relative}`)
  }
  if (!(await fs.stat(current)).isFile()) throw new Error(`artifact is not a regular file: ${relative}`)
  return current
}

async function verifyArtifacts(directory) {
  const root = path.resolve(directory)
  const info = await fs.lstat(root)
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('artifact directory must be a regular directory')
  const manifestFile = await regularFile(root, 'SHA256SUMS')
  const manifest = await fs.readFile(manifestFile, 'utf8')
  const expected = new Map()
  for (const line of manifest.trim().split(/\r?\n/)) {
    const match = /^([a-f0-9]{64}) [ *]([A-Za-z0-9_./-]+)$/.exec(line)
    if (!match || !requiredArtifacts.includes(match[2]) || expected.has(match[2])) throw new Error('invalid artifact manifest entry')
    expected.set(match[2], match[1])
  }
  if (expected.size !== requiredArtifacts.length) throw new Error('artifact manifest is incomplete')
  const artifacts = []
  for (const name of requiredArtifacts) {
    const file = await regularFile(root, name)
    const sha256 = await hashFile(file)
    if (sha256 !== expected.get(name)) throw new Error(`artifact checksum mismatch: ${name}`)
    if (name.endsWith('/astrorder-model-helper')) {
      const helperManifest = await regularFile(root, path.posix.join(path.posix.dirname(name), 'SHA256SUMS'))
      if ((await fs.readFile(helperManifest, 'utf8')).trim() !== `${sha256}  astrorder-model-helper`) throw new Error(`helper checksum mismatch: ${name}`)
    }
    artifacts.push({ name, sha256, bytes: (await fs.stat(file)).size })
  }
  return artifacts
}

async function stageArtifacts(source, destination) {
  source = path.resolve(source)
  destination = path.resolve(destination)
  for (const relative of [path.relative(source, destination), path.relative(destination, source)]) {
    if (!relative || (relative !== '..' && !relative.startsWith('..' + path.sep) && !path.isAbsolute(relative))) throw new Error('staging destination overlaps source')
  }
  const artifacts = await verifyArtifacts(source)
  const parent = path.dirname(destination)
  await fs.mkdir(parent, { recursive: true })
  const temporary = await fs.mkdtemp(path.join(parent, '.go-stage-'))
  const backup = temporary + '.previous'
  let movedPrevious = false
  try {
    for (const name of [...requiredArtifacts, 'SHA256SUMS', 'helpers/linux-amd64/SHA256SUMS', 'helpers/linux-arm64/SHA256SUMS']) {
      const target = path.join(temporary, name)
      await fs.mkdir(path.dirname(target), { recursive: true })
      await fs.copyFile(await regularFile(source, name), target)
    }
    await verifyArtifacts(temporary)
    await fs.writeFile(path.join(temporary, 'build-manifest.json'), JSON.stringify({ source, artifacts }, null, 2) + '\n')
    try {
      await fs.rename(destination, backup)
      movedPrevious = true
    } catch (error) {
      if (error.code !== 'ENOENT') throw error
    }
    try {
      await fs.rename(temporary, destination)
    } catch (error) {
      if (movedPrevious) await fs.rename(backup, destination)
      throw error
    }
    await fs.rm(backup, { recursive: true, force: true })
    return { source, destination, artifacts }
  } finally {
    await fs.rm(temporary, { recursive: true, force: true })
  }
}

function run(command, args, options) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { ...options, stdio: 'inherit', windowsHide: true, shell: false })
    child.on('error', reject)
    child.on('exit', (code, signal) => code === 0 ? resolve() : reject(new Error(`Go build failed: ${signal || code}`)))
  })
}

async function main(args) {
  const root = path.resolve(__dirname, '..')
  let source = process.env.ASTRORDER_GO_CANDIDATE_DIR
  let destination = path.join(__dirname, '.runtime', 'go-stage')
  for (let index = 0; index < args.length; index += 2) {
    if (!args[index + 1] || !['--candidate', '--output'].includes(args[index])) throw new Error('usage: stage-go-artifacts [--candidate DIR] [--output DIR]')
    if (args[index] === '--candidate') source = args[index + 1]
    else destination = args[index + 1]
  }
  if (!source) {
    const runtime = path.join(root, '.runtime')
    await fs.mkdir(runtime, { recursive: true })
    const buildOutput = await fs.mkdtemp(path.join(runtime, 'desktop-build-'))
    const env = { ...process.env, ASTRORDER_GO_ONLY: '1', ASTRORDER_GO_OUTPUT_DIR: buildOutput }
    if (process.platform === 'win32') await run('powershell.exe', ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', path.join(root, 'scripts', 'build-go.ps1')], { cwd: root, env })
    else await run('bash', [path.join(root, 'scripts', 'build-go.sh')], { cwd: root, env })
    const candidates = (await fs.readdir(buildOutput, { withFileTypes: true })).filter(entry => entry.isDirectory() && entry.name.startsWith('candidate-'))
    if (candidates.length !== 1) throw new Error('canonical Go build did not produce exactly one candidate')
    source = path.join(buildOutput, candidates[0].name)
  }
  console.log(JSON.stringify({ ok: true, ...await stageArtifacts(source, destination) }))
}

module.exports = { requiredArtifacts, stageArtifacts, verifyArtifacts }
if (require.main === module) main(process.argv.slice(2)).catch(error => { console.error(error.message); process.exitCode = 1 })
