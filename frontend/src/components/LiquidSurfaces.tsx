import { useEffect } from 'react'
import { useComputedColorScheme } from '@mantine/core'
import { useReducedMotion } from '@mantine/hooks'
import { installLiquidSurfaces } from './liquidSurfaces'

export function LiquidSurfaces() {
  const scheme = useComputedColorScheme('light')
  const reducedMotion = useReducedMotion()
  useEffect(
    () => installLiquidSurfaces({ dark: scheme === 'dark', reducedMotion: !!reducedMotion, engine: 'css' }),
    [scheme, reducedMotion],
  )
  return null
}
