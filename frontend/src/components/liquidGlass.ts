let rendererModule: Promise<typeof import('liquid-gl')> | undefined

/** All surfaces share the same lazy module load, including simultaneous portals. */
export const loadLiquidGlass = () => rendererModule ??= import('liquid-gl')
