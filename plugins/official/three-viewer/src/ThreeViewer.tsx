import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ActionIcon, Badge, Button, Group, Loader, Paper, Text, Tooltip } from '@mantine/core'
import type { AstrorderPluginContext, ViewerContext } from '@astrorder/sdk'
import * as THREE from 'three'
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js'
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js'
import { OBJLoader } from 'three/examples/jsm/loaders/OBJLoader.js'
import { STLLoader } from 'three/examples/jsm/loaders/STLLoader.js'
import { AutoRotateIcon, DownloadIcon, RefreshIcon, WireframeIcon } from './icons'

interface ThreeViewerProps extends ViewerContext {
  ctx: AstrorderPluginContext
}

interface ModelStats {
  format: string
  meshes: number
  triangles: number
  isFallback: boolean
}

type DesktopDownloadBridge = {
  saveDownload?: (url: string, filename: string) => Promise<boolean>
}

function detectModelFormat(name: string, mediaType?: string): 'stl' | 'obj' | 'gltf' | 'glb' | 'unknown' {
  const lower = name.toLowerCase()
  if (lower.endsWith('.stl') || mediaType === 'model/stl' || mediaType === 'application/sla') return 'stl'
  if (lower.endsWith('.obj') || mediaType === 'model/obj') return 'obj'
  if (lower.endsWith('.glb') || mediaType === 'model/gltf-binary') return 'glb'
  if (lower.endsWith('.gltf') || mediaType === 'model/gltf+json') return 'gltf'
  return 'unknown'
}

function buildDownloadUrl(readUrl: string): string {
  if (!readUrl) return ''
  return readUrl.includes('?') ? `${readUrl}&download=1` : `${readUrl}?download=1`
}

function disposeObject3D(root: THREE.Object3D): void {
  root.traverse((node) => {
    const mesh = node as THREE.Mesh
    if (mesh.geometry) {
      mesh.geometry.dispose()
    }
    const materials = Array.isArray(mesh.material) ? mesh.material : mesh.material ? [mesh.material] : []
    for (const mat of materials) {
      if (!mat) continue
      for (const value of Object.values(mat)) {
        if (value && typeof value === 'object' && 'isTexture' in value && typeof (value as THREE.Texture).dispose === 'function') {
          ;(value as THREE.Texture).dispose()
        }
      }
      mat.dispose()
    }
  })
}

function createPlaceholderModel(): THREE.Group {
  const group = new THREE.Group()
  const geometry = new THREE.BoxGeometry(1.5, 1.5, 1.5)
  const material = new THREE.MeshStandardMaterial({
    color: 0x6366f1,
    roughness: 0.32,
    metalness: 0.24,
  })
  const cube = new THREE.Mesh(geometry, material)
  cube.position.y = 0.75
  group.add(cube)
  return group
}

function applyWireframe(root: THREE.Object3D | null, wireframe: boolean): void {
  if (!root) return
  root.traverse((node) => {
    const mesh = node as THREE.Mesh
    if (!mesh.isMesh) return
    const materials = Array.isArray(mesh.material) ? mesh.material : mesh.material ? [mesh.material] : []
    for (const mat of materials) {
      if ('wireframe' in mat) {
        ;(mat as THREE.MeshStandardMaterial).wireframe = wireframe
      }
    }
  })
}

function collectModelStats(root: THREE.Object3D, format: string, isFallback: boolean): ModelStats {
  let meshes = 0
  let triangles = 0
  root.traverse((node) => {
    const mesh = node as THREE.Mesh
    if (!mesh.isMesh || !mesh.geometry) return
    meshes += 1
    const index = mesh.geometry.getIndex()
    if (index) {
      triangles += Math.round(index.count / 3)
    } else {
      const pos = mesh.geometry.getAttribute('position')
      if (pos) {
        triangles += Math.round(pos.count / 3)
      }
    }
  })
  return {
    format: format.toUpperCase(),
    meshes,
    triangles,
    isFallback,
  }
}

function fitModelAndCamera(
  object: THREE.Object3D,
  camera: THREE.PerspectiveCamera,
  controls: OrbitControls,
): { target: THREE.Vector3; position: THREE.Vector3 } {
  const box = new THREE.Box3().setFromObject(object)
  if (box.isEmpty()) {
    const target = new THREE.Vector3(0, 0.5, 0)
    const position = new THREE.Vector3(3.2, 2.4, 4.2)
    camera.position.copy(position)
    controls.target.copy(target)
    controls.update()
    return { target, position }
  }

  const size = new THREE.Vector3()
  const center = new THREE.Vector3()
  box.getSize(size)
  box.getCenter(center)

  const maxDim = Math.max(size.x, size.y, size.z, 0.001)
  const scale = 3.2 / maxDim
  object.scale.multiplyScalar(scale)

  // Recompute bounds after scaling so the model sits cleanly on y = 0.
  const scaledBox = new THREE.Box3().setFromObject(object)
  const scaledCenter = new THREE.Vector3()
  const scaledSize = new THREE.Vector3()
  scaledBox.getCenter(scaledCenter)
  scaledBox.getSize(scaledSize)

  object.position.x -= scaledCenter.x
  object.position.z -= scaledCenter.z
  object.position.y -= scaledBox.min.y

  const target = new THREE.Vector3(0, scaledSize.y * 0.45, 0)
  const radius = Math.max(scaledSize.length() * 0.5, 0.8)
  const fovRad = THREE.MathUtils.degToRad(camera.fov)
  const distance = Math.max((radius / Math.sin(fovRad / 2)) * 1.15, 2.6)

  const position = new THREE.Vector3(distance * 0.72, distance * 0.52, distance * 0.82)
  camera.near = Math.max(distance / 500, 0.01)
  camera.far = Math.max(distance * 40, 200)
  camera.position.copy(position)
  camera.updateProjectionMatrix()

  controls.target.copy(target)
  controls.minDistance = Math.max(radius * 0.25, 0.2)
  controls.maxDistance = Math.max(distance * 8, 25)
  controls.update()

  return { target, position }
}

async function parseModelBuffer(
  format: 'stl' | 'obj' | 'gltf' | 'glb' | 'unknown',
  buffer: ArrayBuffer,
  readUrl: string,
): Promise<THREE.Object3D> {
  if (format === 'stl') {
    const loader = new STLLoader()
    const geometry = loader.parse(buffer)
    if (!geometry.getAttribute('normal')) {
      geometry.computeVertexNormals()
    }
    const material = new THREE.MeshStandardMaterial({
      color: 0x7c3aed,
      roughness: 0.36,
      metalness: 0.18,
    })
    return new THREE.Mesh(geometry, material)
  }

  if (format === 'obj') {
    const text = new TextDecoder().decode(buffer)
    const loader = new OBJLoader()
    const group = loader.parse(text)
    const fallbackMaterial = new THREE.MeshStandardMaterial({
      color: 0x6366f1,
      roughness: 0.4,
      metalness: 0.15,
    })
    group.traverse((node) => {
      const mesh = node as THREE.Mesh
      if (mesh.isMesh) {
        if (!mesh.geometry.getAttribute('normal')) {
          mesh.geometry.computeVertexNormals()
        }
        if (!mesh.material) {
          mesh.material = fallbackMaterial
        }
      }
    })
    return group
  }

  if (format === 'glb' || format === 'gltf') {
    const loader = new GLTFLoader()
    const basePath = readUrl.includes('/') ? readUrl.slice(0, readUrl.lastIndexOf('/') + 1) : ''
    const payload = format === 'gltf' ? new TextDecoder().decode(buffer) : buffer
    const gltf = await new Promise<{ scene: THREE.Group }>((resolve, reject) => {
      loader.parse(payload, basePath, resolve, reject)
    })
    return gltf.scene
  }

  throw new Error(`暂不支持的 3D 模型格式: ${format}`)
}

export function ThreeViewer({ artifact, toolbarAction, ctx }: ThreeViewerProps) {
  const mountRef = useRef<HTMLDivElement>(null)
  const modelRef = useRef<THREE.Object3D | null>(null)
  const controlsRef = useRef<OrbitControls | null>(null)
  const cameraRef = useRef<THREE.PerspectiveCamera | null>(null)
  const defaultViewRef = useRef<{ target: THREE.Vector3; position: THREE.Vector3 } | null>(null)

  const themeMode = ctx.api.useThemeMode()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [wireframe, setWireframe] = useState(false)
  const [autoRotate, setAutoRotate] = useState(true)
  const wireframeRef = useRef(wireframe)
  const autoRotateRef = useRef(autoRotate)
  const [reloadNonce, setReloadNonce] = useState(0)
  const [stats, setStats] = useState<ModelStats | null>(null)

  const format = useMemo(
    () => detectModelFormat(artifact.name, artifact.mediaType),
    [artifact.mediaType, artifact.name],
  )

  useEffect(() => {
    wireframeRef.current = wireframe
    applyWireframe(modelRef.current, wireframe)
  }, [wireframe])

  useEffect(() => {
    autoRotateRef.current = autoRotate
    if (controlsRef.current) {
      controlsRef.current.autoRotate = autoRotate
    }
  }, [autoRotate])

  const handleResetView = useCallback(() => {
    const camera = cameraRef.current
    const controls = controlsRef.current
    const defaults = defaultViewRef.current
    if (camera && controls && defaults) {
      camera.position.copy(defaults.position)
      controls.target.copy(defaults.target)
      controls.update()
      return
    }
    setReloadNonce((n) => n + 1)
  }, [])

  const handleReload = useCallback(() => {
    setReloadNonce((n) => n + 1)
  }, [])

  const handleDownload = useCallback(async () => {
    if (!artifact.readUrl) return
    const downloadUrl = buildDownloadUrl(artifact.readUrl)
    try {
      if (typeof ctx.api.downloadFile === 'function') {
        await ctx.api.downloadFile(downloadUrl, artifact.name)
        return
      }
      const desktop = (window as unknown as { astrorderDesktop?: DesktopDownloadBridge }).astrorderDesktop
      if (desktop?.saveDownload) {
        const saved = await desktop.saveDownload(downloadUrl, artifact.name)
        ctx.api.notify({
          title: saved ? '文件已保存' : '已取消保存',
          message: artifact.name,
          color: saved ? 'teal' : 'gray',
        })
        return
      }
      const response = await ctx.api.fetch(downloadUrl)
      if (!response.ok) {
        throw new Error(`下载失败 (${response.status})`)
      }
      const blob = await response.blob()
      const objectUrl = URL.createObjectURL(blob)
      try {
        const anchor = document.createElement('a')
        anchor.href = objectUrl
        anchor.download = artifact.name
        document.body.appendChild(anchor)
        anchor.click()
        anchor.remove()
      } finally {
        window.setTimeout(() => URL.revokeObjectURL(objectUrl), 60_000)
      }
      ctx.api.notify({
        title: '已开始保存文件',
        message: artifact.name,
        color: 'teal',
      })
    } catch (err: unknown) {
      ctx.api.notify({
        title: '下载文件失败',
        message: err instanceof Error ? err.message : '未知错误',
        color: 'red',
      })
    }
  }, [artifact.name, artifact.readUrl, ctx.api])

  useEffect(() => {
    const container = mountRef.current
    if (!container) return

    let disposed = false
    const abortController = new AbortController()
    let animationFrameId = 0
    let resizeObserver: ResizeObserver | null = null
    let renderer: THREE.WebGLRenderer | null = null
    let controls: OrbitControls | null = null
    let scene: THREE.Scene | null = null

    setLoading(true)
    setError(null)
    setStats(null)
    container.innerHTML = ''

    const init = async () => {
      try {
        const width = Math.max(container.clientWidth || 400, 1)
        const height = Math.max(container.clientHeight || 360, 1)

        scene = new THREE.Scene()
        const isLight = themeMode === 'light'
        scene.background = new THREE.Color(isLight ? 0xf4f6fa : 0x14161c)

        const camera = new THREE.PerspectiveCamera(45, width / height, 0.05, 500)
        camera.position.set(3.2, 2.4, 4.2)
        cameraRef.current = camera

        renderer = new THREE.WebGLRenderer({ antialias: true, powerPreference: 'high-performance' })
        renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2))
        renderer.setSize(width, height)
        renderer.toneMapping = THREE.ACESFilmicToneMapping
        renderer.toneMappingExposure = 1.05
        container.appendChild(renderer.domElement)

        controls = new OrbitControls(camera, renderer.domElement)
        controls.enableDamping = true
        controls.dampingFactor = 0.07
        controls.autoRotate = autoRotateRef.current
        controls.autoRotateSpeed = 1.4
        controlsRef.current = controls

        const hemiLight = new THREE.HemisphereLight(0xffffff, isLight ? 0xcbd5e1 : 0x1e293b, 0.9)
        scene.add(hemiLight)

        const keyLight = new THREE.DirectionalLight(0xffffff, 1.15)
        keyLight.position.set(6, 10, 8)
        scene.add(keyLight)

        const fillLight = new THREE.DirectionalLight(0xa5b4fc, 0.45)
        fillLight.position.set(-6, 4, -6)
        scene.add(fillLight)

        const gridHelper = new THREE.GridHelper(
          10,
          14,
          isLight ? 0x94a3b8 : 0x475569,
          isLight ? 0xe2e8f0 : 0x252c3d,
        )
        scene.add(gridHelper)

        let model: THREE.Object3D
        let isFallback = false
        if (artifact.readUrl) {
          const response = await ctx.api.fetch(artifact.readUrl, {
            signal: abortController.signal,
          })
          if (!response.ok) {
            throw new Error(`加载 3D 模型失败 (HTTP ${response.status})`)
          }
          const buffer = await response.arrayBuffer()
          if (disposed) return
          if (buffer.byteLength === 0) {
            throw new Error('3D 模型文件为空')
          }
          model = await parseModelBuffer(format, buffer, artifact.readUrl)
        } else {
          model = createPlaceholderModel()
          isFallback = true
        }

        if (disposed) {
          disposeObject3D(model)
          return
        }

        scene.add(model)
        modelRef.current = model
        applyWireframe(model, wireframeRef.current)
        defaultViewRef.current = fitModelAndCamera(model, camera, controls)
        setStats(collectModelStats(model, format === 'unknown' ? '3D' : format, isFallback))

        resizeObserver = new ResizeObserver((entries) => {
          if (disposed || !renderer) return
          const entry = entries[0]
          if (!entry) return
          const nextWidth = Math.max(Math.floor(entry.contentRect.width), 1)
          const nextHeight = Math.max(Math.floor(entry.contentRect.height), 1)
          camera.aspect = nextWidth / nextHeight
          camera.updateProjectionMatrix()
          renderer.setSize(nextWidth, nextHeight)
        })
        resizeObserver.observe(container)

        const renderFrame = () => {
          if (disposed || !renderer || !scene || !controls) return
          animationFrameId = window.requestAnimationFrame(renderFrame)
          controls.update()
          renderer.render(scene, camera)
        }
        renderFrame()
        setLoading(false)
      } catch (err: unknown) {
        if (disposed || (err instanceof DOMException && err.name === 'AbortError')) {
          return
        }
        setError(err instanceof Error ? err.message : '3D 渲染器初始化失败')
        setLoading(false)
      }
    }

    void init()

    return () => {
      disposed = true
      abortController.abort()
      if (animationFrameId) {
        window.cancelAnimationFrame(animationFrameId)
      }
      resizeObserver?.disconnect()
      controls?.dispose()
      controlsRef.current = null
      cameraRef.current = null
      defaultViewRef.current = null
      if (modelRef.current) {
        disposeObject3D(modelRef.current)
        modelRef.current = null
      }
      if (renderer) {
        renderer.dispose()
        renderer.domElement.remove()
      }
      scene = null
    }
  }, [artifact.readUrl, ctx.api, format, reloadNonce, themeMode])

  const rootClassName =
    themeMode === 'light'
      ? 'astr-three-viewer astr-three-viewer--light'
      : 'astr-three-viewer'

  return (
    <div className={rootClassName}>
      <Paper p="xs" withBorder className="astr-three-viewer__toolbar">
        <Group justify="space-between" wrap="nowrap" gap="xs">
          <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
            <Badge size="xs" variant="light" color="violet">
              {stats?.format || (format === 'unknown' ? '3D' : format.toUpperCase())}
            </Badge>
            <Text size="xs" fw={600} truncate title={artifact.name}>
              {artifact.name}
            </Text>
          </Group>

          <Group gap={6} wrap="nowrap">
            {toolbarAction}
            <Tooltip label={autoRotate ? '暂停自动旋转' : '开启自动旋转'} withArrow>
              <ActionIcon
                variant={autoRotate ? 'light' : 'subtle'}
                color={autoRotate ? 'violet' : 'gray'}
                size="sm"
                aria-label="切换自动旋转"
                onClick={() => setAutoRotate((prev) => !prev)}
              >
                <AutoRotateIcon size={14} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label={wireframe ? '关闭线框模式' : '开启线框模式'} withArrow>
              <ActionIcon
                variant={wireframe ? 'light' : 'subtle'}
                color={wireframe ? 'violet' : 'gray'}
                size="sm"
                aria-label="切换线框模式"
                onClick={() => setWireframe((prev) => !prev)}
              >
                <WireframeIcon size={14} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="重置视角" withArrow>
              <ActionIcon
                variant="subtle"
                size="sm"
                aria-label="重置视角"
                onClick={handleResetView}
              >
                <RefreshIcon size={14} />
              </ActionIcon>
            </Tooltip>
            {artifact.readUrl && (
              <Tooltip label="下载源文件" withArrow>
                <ActionIcon
                  variant="subtle"
                  size="sm"
                  aria-label="下载源文件"
                  onClick={() => void handleDownload()}
                >
                  <DownloadIcon size={14} />
                </ActionIcon>
              </Tooltip>
            )}
          </Group>
        </Group>
      </Paper>

      <div className="astr-three-viewer__stage">
        {loading && !error && (
          <div className="astr-three-viewer__overlay" role="status" aria-live="polite">
            <Loader size="sm" color="violet" />
            <Text size="xs" c="dimmed">
              正在解析 3D 模型…
            </Text>
          </div>
        )}

        {error && (
          <div className="astr-three-viewer__error" role="alert">
            <Text size="sm" fw={500}>
              {error}
            </Text>
            <Button size="xs" variant="light" color="violet" onClick={handleReload}>
              重试加载
            </Button>
          </div>
        )}

        <div ref={mountRef} className="astr-three-viewer__canvas-host" />

        {!loading && !error && stats && (
          <div className="astr-three-viewer__hud">
            <span>
              {stats.isFallback
                ? '预览占位模型'
                : `${stats.meshes} 网格 · ${stats.triangles.toLocaleString()} 三角面`}
            </span>
            <span className="astr-three-viewer__hud-sep">|</span>
            <span>左键旋转 · 滚轮缩放 · 右键平移</span>
          </div>
        )}
      </div>
    </div>
  )
}
