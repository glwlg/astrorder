import { useCallback, useEffect, useRef, useState } from 'react'
import { ActionIcon, Button, Group, LoadingOverlay, Paper, Text } from '@mantine/core'
import { IconDownload, IconRefresh } from '@tabler/icons-react'
import * as THREE from 'three'
import type { ViewerContext } from '../../types'

export function ThreeViewer({ artifact, toolbarAction }: ViewerContext) {
  const mountRef = useRef<HTMLDivElement>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const initScene = useCallback(() => {
    if (!mountRef.current) return
    mountRef.current.innerHTML = ''

    const width = mountRef.current.clientWidth || 400
    const height = mountRef.current.clientHeight || 400

    const scene = new THREE.Scene()
    scene.background = new THREE.Color(0x1a1a1a)

    const camera = new THREE.PerspectiveCamera(45, width / height, 0.1, 1000)
    camera.position.set(0, 0, 5)

    const renderer = new THREE.WebGLRenderer({ antialias: true })
    renderer.setSize(width, height)
    mountRef.current.appendChild(renderer.domElement)

    // 添加光照
    const ambientLight = new THREE.AmbientLight(0xffffff, 0.6)
    scene.add(ambientLight)

    const dirLight = new THREE.DirectionalLight(0xffffff, 0.8)
    dirLight.position.set(5, 10, 7)
    scene.add(dirLight)

    // 添加基础立方体/网格占位，并支持鼠标拖拽旋转
    const geometry = new THREE.BoxGeometry(1.5, 1.5, 1.5)
    const material = new THREE.MeshStandardMaterial({
      color: 0x4f46e5,
      roughness: 0.3,
      metalness: 0.2,
    })
    const cube = new THREE.Mesh(geometry, material)
    scene.add(cube)

    const gridHelper = new THREE.GridHelper(10, 10, 0x444444, 0x222222)
    gridHelper.position.y = -1
    scene.add(gridHelper)

    let isDragging = false
    let prevMouseX = 0
    let prevMouseY = 0

    const onMouseDown = (e: MouseEvent) => {
      isDragging = true
      prevMouseX = e.clientX
      prevMouseY = e.clientY
    }

    const onMouseMove = (e: MouseEvent) => {
      if (!isDragging) return
      const deltaX = e.clientX - prevMouseX
      const deltaY = e.clientY - prevMouseY
      cube.rotation.y += deltaX * 0.01
      cube.rotation.x += deltaY * 0.01
      prevMouseX = e.clientX
      prevMouseY = e.clientY
    }

    const onMouseUp = () => {
      isDragging = false
    }

    const dom = renderer.domElement
    dom.addEventListener('mousedown', onMouseDown)
    window.addEventListener('mousemove', onMouseMove)
    window.addEventListener('mouseup', onMouseUp)

    let animationFrameId: number
    const animate = () => {
      animationFrameId = requestAnimationFrame(animate)
      if (!isDragging) {
        cube.rotation.y += 0.005
      }
      renderer.render(scene, camera)
    }
    animate()
    setLoading(false)

    return () => {
      cancelAnimationFrame(animationFrameId)
      dom.removeEventListener('mousedown', onMouseDown)
      window.removeEventListener('mousemove', onMouseMove)
      window.removeEventListener('mouseup', onMouseUp)
      renderer.dispose()
    }
  }, [])

  useEffect(() => {
    setLoading(true)
    setError(null)
    const timer = setTimeout(() => {
      try {
        initScene()
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : '3D 渲染器初始化失败')
        setLoading(false)
      }
    }, 50)
    return () => clearTimeout(timer)
  }, [initScene])

  return (
    <div className="three-viewer-pane" style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <Paper p="xs" withBorder style={{ borderBottom: '1px solid var(--astr-border)', borderRadius: 0 }}>
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
            <Text size="xs" fw={600} truncate title={artifact.name}>
              {artifact.name}
            </Text>
            <Text size="xs" c="dimmed">
              3D 场景视口 (按住左键旋转)
            </Text>
          </Group>
          <Group gap={6} wrap="nowrap">
            {toolbarAction}
            <ActionIcon
              variant="subtle"
              size="sm"
              title="重置视角"
              onClick={initScene}
            >
              <IconRefresh size={14} />
            </ActionIcon>
            <ActionIcon
              variant="subtle"
              size="sm"
              title="下载源文件"
              onClick={() => window.open(`${artifact.readUrl}&download=1`, '_blank')}
            >
              <IconDownload size={14} />
            </ActionIcon>
          </Group>
        </Group>
      </Paper>

      <div style={{ flex: 1, position: 'relative', minHeight: 0, overflow: 'hidden' }}>
        <LoadingOverlay visible={loading} />
        {error && (
          <div style={{ padding: 24, textAlign: 'center', color: 'var(--astr-muted)' }}>
            <Text size="sm">{error}</Text>
            <Button size="xs" mt="md" variant="light" onClick={initScene}>
              重试
            </Button>
          </div>
        )}
        <div ref={mountRef} style={{ width: '100%', height: '100%' }} />
      </div>
    </div>
  )
}
