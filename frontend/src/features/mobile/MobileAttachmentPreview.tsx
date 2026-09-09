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
  return <span className="m-attachment-preview">{url ? <button aria-label={`预览 ${file.name}`} onClick={() => onOpen(url)}><img src={url} alt={file.name} style={{ width: 54, height: 54, objectFit: 'cover', borderRadius: 7 }} /></button> : file.name}<button aria-label={`移除 ${file.name}`} onClick={onRemove}><IconX size={15} /></button></span>
}
