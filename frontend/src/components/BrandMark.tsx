import { useComputedColorScheme } from '@mantine/core'

export function brandMarkSrc(scheme: 'light' | 'dark'): string {
  return scheme === 'dark' ? '/logo-dark.png' : '/logo.png'
}

export function BrandMark({
  className,
  size,
  alt = '星序',
}: {
  className?: string
  size?: number
  alt?: string
}) {
  const scheme = useComputedColorScheme('light', { getInitialValueInEffect: true })
  return (
    <img
      className={className}
      src={brandMarkSrc(scheme)}
      width={size}
      height={size}
      alt={alt}
    />
  )
}
