import { IconExternalLink } from '@tabler/icons-react'
import { Anchor } from '@mantine/core'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

function safeUrl(value: string | undefined): string | undefined {
  if (!value) return undefined
  if (/^(?:https?:|mailto:)/i.test(value)) return value
  if (value.startsWith('//')) return undefined
  if (value.startsWith('/') || value.startsWith('#')) return value
  return undefined
}

export function MarkdownContent({ value }: { value: string }) {
  return (
    <div className="markdown-content">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        components={{
          a: (props) => {
            const { href, children, ...rest } = props
            const safe = safeUrl(href)
            if (!safe) return <>{children}</>
            const external = /^(?:https?:|mailto:)/i.test(safe)
            return (
              <Anchor
                {...rest}
                href={safe}
                target={external ? '_blank' : undefined}
                rel={external ? 'noreferrer' : undefined}
              >
                {children}
                {external && <IconExternalLink className="markdown-external-icon" size={13} aria-hidden="true" />}
              </Anchor>
            )
          },
          img: (props) => {
            const safe = safeUrl(props.src)
            return safe ? <img src={safe} alt={props.alt || ''} loading="lazy" /> : null
          },
          table: (props) => <div className="markdown-table-wrap"><table>{props.children}</table></div>,
          pre: (props) => <pre className="markdown-code-block">{props.children}</pre>,
        }}
      >
        {value}
      </ReactMarkdown>
    </div>
  )
}
