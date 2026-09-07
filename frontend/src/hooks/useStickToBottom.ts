import { useCallback, useLayoutEffect, useRef, useState } from 'react'
import { isNearBottom } from '../domain/semantics'

interface StickOptions {
  contentVersion: string | number
}

export function useStickToBottom<T extends HTMLElement>({ contentVersion }: StickOptions) {
  const containerRef = useRef<T | null>(null)
  const followRef = useRef(true)
  const programmaticScrollRef = useRef(false)
  const prependHeightRef = useRef<number | null>(null)
  const [following, setFollowing] = useState(true)

  const setContainerRef = useCallback((element: T | null) => {
    containerRef.current = element
  }, [])

  const finishProgrammaticScroll = useCallback(() => {
    const callback = () => {
      programmaticScrollRef.current = false
    }
    if (typeof window.requestAnimationFrame === 'function') window.requestAnimationFrame(callback)
    else window.setTimeout(callback, 0)
  }, [])

  const scrollToBottom = useCallback(
    () => {
      const element = containerRef.current
      if (!element) return
      followRef.current = true
      setFollowing(true)
      programmaticScrollRef.current = true
      if (typeof element.scrollTo === 'function') {
        element.scrollTo({ top: element.scrollHeight, behavior: 'auto' })
      } else {
        element.scrollTop = element.scrollHeight
      }
      finishProgrammaticScroll()
    },
    [finishProgrammaticScroll],
  )

  useLayoutEffect(() => {
    const element = containerRef.current
    if (!element) return

    if (prependHeightRef.current !== null) {
      const delta = element.scrollHeight - prependHeightRef.current
      element.scrollTop += delta
      prependHeightRef.current = null
      return
    }

    if (followRef.current) scrollToBottom()
  }, [contentVersion, scrollToBottom])

  const onScroll = useCallback(() => {
    const element = containerRef.current
    if (!element || programmaticScrollRef.current) return
    const nextFollowing = isNearBottom({
      scrollTop: element.scrollTop,
      clientHeight: element.clientHeight,
      scrollHeight: element.scrollHeight,
    })
    followRef.current = nextFollowing
    setFollowing(nextFollowing)
  }, [])

  const capturePrependAnchor = useCallback(() => {
    if (containerRef.current) prependHeightRef.current = containerRef.current.scrollHeight
  }, [])

  return {
    setContainerRef,
    following,
    onScroll,
    scrollToBottom,
    capturePrependAnchor,
  }
}
