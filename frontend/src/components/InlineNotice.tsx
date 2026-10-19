import type { ReactNode } from 'react'
import { IconInfoCircle, IconX } from '@tabler/icons-react'
import './InlineNotice.css'

export function InlineNotice({ title, children, icon, actions, onClose, className = '', alert = false }: {
  title?: string
  children: ReactNode
  icon?: ReactNode
  actions?: ReactNode
  onClose?: () => void
  className?: string
  alert?: boolean
}) {
  return <div className={`inline-notice ${className}`} role={alert ? 'alert' : 'status'}>
    <span className="inline-notice-icon" aria-hidden="true">{icon || <IconInfoCircle size={18} />}</span>
    <div className="inline-notice-copy">{title && <strong>{title}</strong>}<div>{children}</div></div>
    {actions && <div className="inline-notice-actions">{actions}</div>}
    {onClose && <button type="button" className="inline-notice-close" aria-label="关闭提示" onClick={onClose}><IconX size={16} /></button>}
  </div>
}
