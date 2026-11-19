import type { ProjectAppearanceMap } from './projectAppearanceCatalog'

export * from './projectAppearanceCatalog'
export { ProjectGlyph } from './ProjectGlyph'
export { ProjectAppearanceModal } from './ProjectAppearanceModal'

const APPEARANCE_STORAGE_KEY = 'astrorder:project_appearance'
const PINNED_PROJECTS_KEY = 'astrorder:pinned_projects'

export function loadProjectAppearance(): ProjectAppearanceMap {
  try {
    const raw = localStorage.getItem(APPEARANCE_STORAGE_KEY)
    if (raw) return JSON.parse(raw)
  } catch {}
  return {}
}

export function loadPinnedProjects(): string[] {
  try {
    const raw = localStorage.getItem(PINNED_PROJECTS_KEY)
    if (raw) return JSON.parse(raw)
  } catch {}
  return []
}
