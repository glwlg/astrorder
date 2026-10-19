import { Alert, Button, Center, FileInput, Group, Paper, Stack, Text, Title } from '@mantine/core'
import { IconAlertCircle, IconPhoto, IconScan, IconX } from '@tabler/icons-react'
import jsQR from 'jsqr'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { parseAndValidateQrContent } from './qrUtils'

export function QrScanPage({
  onNavigate,
  onDone,
}: {
  onNavigate?: (path: string) => void
  onDone?: () => void
}) {
  const routerNavigate = useNavigate()
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const animFrameRef = useRef<number | null>(null)

  const [cameraActive, setCameraActive] = useState(false)
  const [cameraError, setCameraError] = useState<string | null>(null)
  const [scanError, setScanError] = useState<string | null>(null)
  const [decodingFile, setDecodingFile] = useState(false)

  const stopCamera = useCallback(() => {
    if (animFrameRef.current !== null) {
      cancelAnimationFrame(animFrameRef.current)
      animFrameRef.current = null
    }
    if (streamRef.current) {
      streamRef.current.getTracks().forEach((track) => track.stop())
      streamRef.current = null
    }
    setCameraActive(false)
  }, [])

  const handleValidResult = useCallback((urlStr: string) => {
    const result = parseAndValidateQrContent(urlStr, window.location.origin)
    if (!result.valid) {
      setScanError(result.reason)
      return
    }

    stopCamera()
    const target = `/auth/qr#id=${result.id}&scan_secret=${result.scan_secret}`
    if (onNavigate) {
      onNavigate(target)
    } else {
      routerNavigate(target)
    }
  }, [stopCamera, onNavigate, routerNavigate])

  const scanFrame = useCallback(() => {
    const video = videoRef.current
    if (!video || video.readyState !== video.HAVE_ENOUGH_DATA) {
      animFrameRef.current = requestAnimationFrame(scanFrame)
      return
    }

    let canvas = canvasRef.current
    if (!canvas) {
      canvas = document.createElement('canvas')
      canvasRef.current = canvas
    }

    canvas.width = video.videoWidth
    canvas.height = video.videoHeight
    const ctx = canvas.getContext('2d')
    if (ctx) {
      ctx.drawImage(video, 0, 0, canvas.width, canvas.height)
      try {
        const imageData = ctx.getImageData(0, 0, canvas.width, canvas.height)
        const code = jsQR(imageData.data, imageData.width, imageData.height)
        if (code && code.data) {
          handleValidResult(code.data)
          return
        }
      } catch {
        // Continue scanning on frame error
      }
    }

    animFrameRef.current = requestAnimationFrame(scanFrame)
  }, [handleValidResult])

  useEffect(() => {
    let active = true

    const startCamera = async () => {
      setCameraError(null)
      setScanError(null)

      if (typeof navigator === 'undefined' || !navigator.mediaDevices?.getUserMedia) {
        if (active) setCameraError('当前网络环境（HTTP局域网）或浏览器不支持直接调用摄像头，请使用下方图片上传。')
        return
      }

      try {
        const stream = await navigator.mediaDevices.getUserMedia({
          video: { facingMode: 'environment' },
        })
        if (!active) {
          stream.getTracks().forEach((track) => track.stop())
          return
        }
        streamRef.current = stream
        if (videoRef.current) {
          videoRef.current.srcObject = stream
          videoRef.current.setAttribute('playsinline', 'true')
          await videoRef.current.play().catch(() => {})
        }
        if (!active) return
        setCameraActive(true)
        animFrameRef.current = requestAnimationFrame(scanFrame)
      } catch {
        if (active) setCameraError('无法打开摄像头，请确认已授予相机权限或使用下方图片上传。')
      }
    }

    void startCamera()

    return () => {
      active = false
      stopCamera()
    }
  }, [scanFrame, stopCamera])

  const handleFileUpload = (file: File | null) => {
    if (!file) return
    setScanError(null)
    setDecodingFile(true)

    const reader = new FileReader()
    reader.onload = (e) => {
      const dataUrl = e.target?.result as string
      if (!dataUrl) {
        setDecodingFile(false)
        setScanError('读取图片失败')
        return
      }

      const img = new Image()
      img.onload = () => {
        let canvas = canvasRef.current
        if (!canvas) {
          canvas = document.createElement('canvas')
          canvasRef.current = canvas
        }
        canvas.width = img.width || 300
        canvas.height = img.height || 300
        const ctx = canvas.getContext('2d')
        if (!ctx) {
          setDecodingFile(false)
          setScanError('无法解析图片数据')
          return
        }
        ctx.drawImage(img, 0, 0, canvas.width, canvas.height)
        let code = null
        try {
          const imageData = ctx.getImageData(0, 0, canvas.width, canvas.height)
          code = jsQR(imageData.data, imageData.width, imageData.height)
        } catch {
          // ignore
        }
        setDecodingFile(false)
        if (code && code.data) {
          handleValidResult(code.data)
        } else {
          setScanError('图片中未识别到有效的二维码，请换一张重试')
        }
      }
      img.onerror = () => {
        setDecodingFile(false)
        setScanError('加载图片失败')
      }
      img.src = dataUrl
    }
    reader.onerror = () => {
      setDecodingFile(false)
      setScanError('读取文件失败')
    }
    reader.readAsDataURL(file)
  }

  const handleBack = () => {
    stopCamera()
    if (onDone) {
      onDone()
    } else {
      routerNavigate('/chat')
    }
  }

  return (
    <Center style={{ minHeight: '100vh', padding: 16 }}>
      <Paper withBorder radius="lg" p="xl" maw={460} w="100%">
        <Stack gap="md">
          <Group justify="space-between" align="center">
            <Group gap="xs">
              <IconScan size={24} color="#228be6" />
              <Title order={2} size="h3">扫码登录电脑</Title>
            </Group>
            <Button variant="subtle" size="xs" onClick={handleBack} leftSection={<IconX size={14} />}>
              返回
            </Button>
          </Group>

          {scanError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />}>
              {scanError}
            </Alert>
          )}

          {cameraError && (
            <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
              {cameraError}
            </Alert>
          )}

          <Paper
            withBorder
            radius="md"
            style={{
              display: cameraActive ? 'flex' : 'none',
              position: 'relative',
              overflow: 'hidden',
              background: '#000000',
              aspectRatio: '1',
              width: '100%',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            <video
              ref={videoRef}
              style={{ width: '100%', height: '100%', objectFit: 'cover' }}
              playsInline
              muted
            />
          </Paper>

          <Stack gap="xs">
            <Text size="xs" c="dimmed">
              支持直接调用摄像头或选择二维码截图：
            </Text>
            <FileInput
              data-testid="qr-file-input"
              placeholder="选择二维码截图或相册照片"
              accept="image/*"
              leftSection={<IconPhoto size={16} />}
              onChange={handleFileUpload}
              disabled={decodingFile}
            />
          </Stack>
        </Stack>
      </Paper>
    </Center>
  )
}