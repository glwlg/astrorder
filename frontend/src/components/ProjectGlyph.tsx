import type { CSSProperties } from 'react'
import {
  ICON_COMPONENT_MAP, PROJECT_ART_ICONS, projectAppearanceColor,
} from './projectAppearanceCatalog'

export function ProjectGlyph({ iconName, colorName, size = 15, style }: {
  iconName?: string
  colorName?: string
  size?: number
  style?: CSSProperties
}) {
  const art = iconName && PROJECT_ART_ICONS[iconName]
  if (art) {
    return <img className="project-art-icon" src={art.src} alt="" aria-hidden="true" width={size} height={size} draggable={false} style={{ flexShrink: 0, ...style }} />
  }
  const Comp = (iconName && ICON_COMPONENT_MAP[iconName]) || ICON_COMPONENT_MAP.folder
  return <Comp size={size} color={projectAppearanceColor({ icon: iconName, color: colorName })} style={{ flexShrink: 0, ...style }} />
}
