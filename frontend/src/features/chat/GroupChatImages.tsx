import { useEffect, useRef, useState } from "react";
import { ActionIcon, Modal } from "@mantine/core";
import { IconX } from "@tabler/icons-react";
import type { Attachment } from "../../domain/types";
import { attachmentHref } from "../../domain/attachmentHref";

export function GroupChatImageDraft({ file, onRemove, disabled }: { file: File; onRemove: () => void; disabled: boolean }) {
  const imageRef = useRef<HTMLImageElement>(null);
  useEffect(() => {
    const preview = URL.createObjectURL(file);
    if (imageRef.current) imageRef.current.src = preview;
    return () => URL.revokeObjectURL(preview);
  }, [file]);
  return <div className="group-chat-image-draft">
    <img ref={imageRef} alt={`待发送：${file.name}`} />
    <ActionIcon size="xs" color="gray" variant="filled" radius="xl" aria-label={`移除 ${file.name}`} onClick={onRemove} disabled={disabled}>
      <IconX size={12} />
    </ActionIcon>
  </div>;
}

export function GroupChatImages({ attachments }: { attachments: Attachment[] }) {
  const [preview, setPreview] = useState<Attachment | null>(null);
  return <>
    <div className="message-attachments group-chat-message-images">
      {attachments.map((attachment) => {
        const url = attachmentHref(attachment);
        if (!url) return null;
        return <button key={attachment.id} type="button" className="attachment-image-link" aria-label={`查看图片：${attachment.name}`} onClick={() => setPreview(attachment)}>
          <img className="attachment-image" src={url} alt={attachment.name} loading="lazy" />
        </button>;
      })}
    </div>
    <Modal opened={Boolean(preview)} onClose={() => setPreview(null)} title={preview?.name} size="xl" centered>
      {preview && <img src={attachmentHref(preview)} alt={preview.name} style={{ display: "block", maxWidth: "100%", maxHeight: "75dvh", margin: "auto", objectFit: "contain" }} />}
    </Modal>
  </>;
}
