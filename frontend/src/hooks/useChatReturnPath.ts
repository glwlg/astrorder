import { useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'

export function useChatReturnPath(): string {
  const { pathname, search, hash } = useLocation()
  const [lastPath, setLastPath] = useState('/chat')
  const isChat = pathname === '/chat' || (pathname.startsWith('/chat/') && !pathname.startsWith('/chat/group-'))
  const currentPath = `${pathname}${search}${hash}`

  useEffect(() => {
    if (isChat) setLastPath(currentPath)
  }, [currentPath, isChat])

  return isChat ? currentPath : lastPath
}
