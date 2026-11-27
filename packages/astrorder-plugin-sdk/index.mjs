export const SDK_VERSION = 1

/**
 * Identity helper providing strict TypeScript inference for Astrorder ESM plugins.
 * At runtime in Astrorder, plugins import this from `@astrorder/sdk` (mapped via Import Map).
 */
export function defineAstrorderPlugin(mod) {
  return mod
}

export function getHostRuntime() {
  return globalThis.__ASTRORDER_PLUGIN_RUNTIME_V1__
}
