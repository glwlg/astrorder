import { Alert, Badge, ThemeIcon, createTheme, defaultVariantColorsResolver } from '@mantine/core'

// Passive status uses typography and icons, never a colored surface.
export const theme = createTheme({
  primaryColor: 'indigo',
  defaultRadius: 'md',
  variantColorResolver(input) {
    const resolved = defaultVariantColorsResolver(input)
    if (input.variant !== 'light') return resolved
    return {
      ...resolved,
      background: 'var(--astr-surface-muted)',
      hover: 'var(--astr-border)',
      color: input.color === 'red' ? 'var(--astr-red)' : 'var(--astr-text)',
      hoverColor: input.color === 'red' ? 'var(--astr-red)' : 'var(--astr-text)',
      border: '1px solid transparent',
    }
  },
  components: {
    Badge: Badge.extend({
      defaultProps: { variant: 'transparent', radius: 'sm' },
      vars: () => ({ root: { '--badge-dot-color': 'currentColor' } }),
      styles: (_theme, props) => ({
        root: {
          background: 'transparent',
          color: props.color === 'red' ? 'var(--astr-red)' : 'var(--astr-muted)',
          borderColor: 'transparent',
          fontWeight: 500,
          textTransform: 'none',

        },
      }),
    }),
    Alert: Alert.extend({
      styles: (_theme, props) => ({
        root: { background: 'var(--astr-surface)', color: 'var(--astr-text)', border: '1px solid var(--astr-border)', borderRadius: 12, padding: '12px 14px' },
        title: { color: 'var(--astr-text)' },
        message: { color: 'var(--astr-muted)' },
        icon: { color: props.color === 'red' ? 'var(--astr-red)' : props.color === 'yellow' || props.color === 'orange' ? 'var(--astr-yellow)' : 'var(--astr-muted)' },
      }),
    }),
    ThemeIcon: ThemeIcon.extend({
      defaultProps: { variant: 'transparent' },
      styles: { root: { background: 'transparent', color: 'var(--astr-muted)', borderColor: 'transparent' } },
    }),
  },
})
