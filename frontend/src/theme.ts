import { Alert, Badge, Notification, ThemeIcon, createTheme, defaultVariantColorsResolver } from '@mantine/core'

// Passive status uses typography and icons, never a colored surface.
export const theme = createTheme({
  primaryColor: 'violet',
  colors: {
    violet: ['#f7f2ff', '#eee4ff', '#ddc9ff', '#c8a5ff', '#b181fb', '#9b61f2', '#8a3ffc', '#7630db', '#6424bd', '#501b99'],
  },
  fontFamily: '"Segoe UI", "PingFang SC", "Microsoft YaHei UI", "Microsoft YaHei", "Noto Sans SC", "Noto Sans CJK SC", system-ui, sans-serif',
  fontFamilyMonospace: '"Astrorder JetBrains Mono", "Cascadia Mono", Consolas, monospace',
  headings: { fontFamily: 'inherit' },
  fontSizes: { xs: '0.75rem', sm: '0.875rem', md: '1rem', lg: '1.125rem', xl: '1.25rem' },
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
    Notification: Notification.extend({
      defaultProps: { withBorder: true, radius: 'lg' },
      styles: {
        root: {
          background: 'color-mix(in srgb, var(--astr-surface) 97%, var(--astr-blue))',
          border: '1px solid var(--astr-overlay-border)',
          boxShadow: 'var(--astr-overlay-shadow)',
          padding: '.875rem .75rem .875rem 1.375rem',
        },
        body: { minWidth: 0 },
        title: { color: 'var(--astr-text)', fontSize: '.875rem', fontWeight: 650, lineHeight: 1.45 },
        description: { color: 'var(--astr-text)', fontSize: '.8125rem', lineHeight: 1.6, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
        closeButton: { color: 'var(--astr-muted)', alignSelf: 'flex-start', width: '1.75rem', height: '1.75rem' },
      },
    }),
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
