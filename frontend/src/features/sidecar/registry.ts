import type { ArtifactRef } from '../../domain/artifact'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import type { ArtifactViewer } from './types'
import { diffViewer } from './viewers/diff'
import { drawioViewer } from './viewers/drawio'
import { excalidrawViewer } from './viewers/excalidraw'
import { fileTreeViewer } from './viewers/filetree'
import { gitDiffViewer } from './viewers/gitdiff'
import { sideChatViewer } from './viewers/sidechat'
import { agentGraphViewer } from './viewers/agentgraph'
import { htmlViewer } from './viewers/html'
import { mermaidViewer } from './viewers/mermaid'
import { monacoViewer } from './viewers/monaco'
import { xtermViewer } from './viewers/terminal'
import { threeViewer } from './viewers/three'

class ArtifactViewerRegistry {
  private viewers: ArtifactViewer[] = []
  private cachedExtensionRegex: RegExp | null = null

  constructor() {
    // 插件自声明式注册
    this.register(drawioViewer)
    this.register(mermaidViewer)
    this.register(excalidrawViewer)
    this.register(diffViewer)
    this.register(threeViewer)
    this.register(htmlViewer)
    this.register(xtermViewer)
    this.register(monacoViewer)
    this.register(fileTreeViewer)
    this.register(gitDiffViewer)
    this.register(sideChatViewer)
    this.register(agentGraphViewer)
  }

  register(viewer: ArtifactViewer) {
    this.viewers.push(viewer)
    this.cachedExtensionRegex = null // 缓存失效，动态重构
  }

  getAllViewers(): ArtifactViewer[] {
    return [...this.viewers]
  }

  /**
   * 收集所有已注册插件声明的扩展名集合（包括内置图片后缀）
   */
  getAllRegisteredExtensions(): Set<string> {
    const exts = new Set<string>([
      '.png', '.jpg', '.jpeg', '.webp', '.svg', '.gif', '.bmp', '.ico',
    ])
    for (const v of this.viewers) {
      if (v.extensions) {
        for (const ext of v.extensions) {
          exts.add(ext.toLowerCase())
        }
      }
    }
    return exts
  }

  /**
   * 基于所有插件自声明扩展名，动态编译生成精准识别正则
   */
  getExtensionPattern(): RegExp {
    if (this.cachedExtensionRegex) return this.cachedExtensionRegex

    const exts = Array.from(this.getAllRegisteredExtensions())
      .map((ext) => ext.replace(/^\./, '').replace(/\./g, '\\.'))
      .sort((a, b) => b.length - a.length) // 优先匹配较长后缀（如 drawio.xml 优先于 xml）

    if (exts.length === 0) {
      this.cachedExtensionRegex = /$.^/ // 永远不匹配
    } else {
      const pattern = `([\\w\\u4e00-\\u9fa5\\-_./\\\\]+\\.(?:${exts.join('|')}))(?=[\\s,，。！!？?\\)）\\]】"\'<>]|\\b|$)`
      this.cachedExtensionRegex = new RegExp(pattern, 'gi')
    }

    return this.cachedExtensionRegex
  }

  /**
   * 判断某个文件名/路径是否能被注册表中的插件处理
   */
  isSupportedExtension(fileNameOrPath: string): boolean {
    const lower = fileNameOrPath.toLowerCase().split(/[?#]/)[0]
    for (const v of this.viewers) {
      if (v.extensions?.some((ext) => lower.endsWith(ext.toLowerCase()))) {
        return true
      }
    }
    return false
  }

  findViewer(artifact: ArtifactRef): ArtifactViewer | null {
    const isEnabled = usePluginSettingsStore.getState().isPluginEnabled
    let bestViewer: ArtifactViewer | null = null
    let maxPriority = 0

    const lowerName = (artifact.name || '').toLowerCase()
    const mime = (artifact.mediaType || '').toLowerCase()

    for (const viewer of this.viewers) {
      // 用户关闭开关则跳过
      if (!isEnabled(viewer.id)) continue

      let priority = 0

      // 1. 优先调用自定义打分支持
      if (viewer.supports) {
        priority = viewer.supports(artifact)
      } else {
        // 2. 默认自声明匹配：扩展名严格匹配 > MIME 匹配
        if (viewer.extensions?.some((ext) => lowerName.endsWith(ext.toLowerCase()))) {
          priority = 80
        } else if (viewer.mimeTypes?.some((m) => m.toLowerCase() === mime && mime !== 'text/plain')) {
          priority = 40
        }
      }

      if (priority > maxPriority) {
        maxPriority = priority
        bestViewer = viewer
      }
    }

    return bestViewer
  }

  getViewerById(id: string): ArtifactViewer | null {
    return this.viewers.find((v) => v.id === id) || null
  }
}

export const artifactViewerRegistry = new ArtifactViewerRegistry()
