import { GITLAB_CI_PLUGIN_ID, registerGitLabExtension } from '../gitlab/gitlabExtension'
import { extensionRegistry } from './extensionRegistry'

export function bootstrapBuiltinExtensions(): void {
  if (!extensionRegistry.getBuiltin(GITLAB_CI_PLUGIN_ID)) {
    registerGitLabExtension()
  }
}
