import { MantineProvider } from '@mantine/core'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { BrandMark, brandMarkSrc } from './BrandMark'

afterEach(() => cleanup())

describe('BrandMark', () => {
  it('defaults to the light logo', () => {
    expect(brandMarkSrc('light')).toBe('/logo.png')
    expect(brandMarkSrc('dark')).toBe('/logo-dark.png')
    render(
      <MantineProvider defaultColorScheme="light">
        <BrandMark size={20} />
      </MantineProvider>,
    )
    expect(screen.getByAltText('星序')).toHaveAttribute('src', '/logo.png')
  })

  it('uses the dark logo in dark mode', () => {
    render(
      <MantineProvider forceColorScheme="dark">
        <BrandMark size={20} />
      </MantineProvider>,
    )
    expect(screen.getByAltText('星序')).toHaveAttribute('src', '/logo-dark.png')
  })
})
