import { useEffect, useState } from 'react'
import { IconX } from '@tabler/icons-react'

export function MobileAttachmentPreview({ file, onOpen, onRemove }: { file: File; onOpen: (url: string) => void; onRemove: () => void }) {
  const [url, setUrl] = useState('')
  useEffect(() => {
    if (!file.type.startsWith('image/')) return
    const next = URL.createObjectURL(file)
    setUrl(next)
    return () => URL.revokeObjectURL(next)
  }, [file])
  return (
    <span className="m-attachment-preview">
      {url ? (
        <button className="m-attachment-thumb-btn" aria-label={`预览 ${file.name}`} onClick={() => onOpen(url)}>
          <img src={url} alt={file.name} />
        </button>
      ) : (
        <span className="m-attachment-file-pill">{file.name}</span>
      )}
      <button className="m-attachment-remove-btn" aria-label={`移除 ${file.name}`} onClick={onRemove}>
        <IconX size={11} stroke={2.6} />
      </button>
    </span>
  )
}
