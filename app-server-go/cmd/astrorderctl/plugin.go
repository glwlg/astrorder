package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"astrorder.dev/app-server/internal/pluginhost"
)

const pluginSignatureDomainPrefixV1 = "astrorder-plugin-sig-v1\n"

func runPlugin(args []string, output io.Writer) int {
	if len(args) < 1 {
		return cliFailure(output, errors.New("usage: astrorderctl plugin init|keygen|sign|verify|pack|dev ..."))
	}
	switch args[0] {
	case "init":
		return runPluginInit(args[1:], output)
	case "keygen":
		return runPluginKeygen(args[1:], output)
	case "sign":
		return runPluginSign(args[1:], output)
	case "verify":
		return runPluginVerify(args[1:], output)
	case "pack":
		return runPluginPack(args[1:], output)
	case "dev":
		return runPluginDev(args[1:], output)
	default:
		return cliFailure(output, fmt.Errorf("unknown plugin subcommand %q (expected init|keygen|sign|verify|pack|dev)", args[0]))
	}
}

// parseFlagsFlexible parses flags appearing either before or after positional arguments.
func parseFlagsFlexible(fs *flag.FlagSet, args []string) ([]string, error) {
	type boolFlagger interface{ IsBoolFlag() bool }
	var flagArgs []string
	var posArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			posArgs = append(posArgs, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			flagArgs = append(flagArgs, arg)
			if strings.Contains(arg, "=") {
				continue
			}
			name := strings.TrimLeft(arg, "-")
			f := fs.Lookup(name)
			if f == nil {
				continue
			}
			if bf, ok := f.Value.(boolFlagger); ok && bf.IsBoolFlag() {
				continue
			}
			if i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		} else {
			posArgs = append(posArgs, arg)
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}
	posArgs = append(posArgs, fs.Args()...)
	return posArgs, nil
}

func runPluginInit(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("plugin init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	pluginID := flags.String("id", "", "plugin identifier")
	pluginName := flags.String("name", "", "human-readable plugin name")
	tmpl := flags.String("template", "esm-viewer", "plugin template: esm-viewer|iframe-viewer|chat-hook")
	ext := flags.String("ext", ".myext", "target file extension for viewer templates")

	pos, err := parseFlagsFlexible(flags, args)
	if err != nil {
		return cliFailure(output, err)
	}
	if len(pos) != 1 {
		return cliFailure(output, errors.New("usage: astrorderctl plugin init <dir> --id <plugin-id> [--name <name>] [--template esm-viewer|iframe-viewer|chat-hook] [--ext .myext]"))
	}

	id := strings.TrimSpace(*pluginID)
	if id == "" {
		return cliFailure(output, errors.New("plugin init requires --id"))
	}
	if pluginhost.IsReservedPluginID(id) {
		return cliFailure(output, fmt.Errorf("plugin id %q is reserved", id))
	}

	name := strings.TrimSpace(*pluginName)
	if name == "" {
		name = humanizePluginName(id)
	}

	templateName := strings.ToLower(strings.TrimSpace(*tmpl))
	if templateName == "" {
		templateName = "esm-viewer"
	}

	normExt := normalizeSingleExt(*ext)
	if normExt == "" {
		normExt = ".myext"
	}

	absDir, err := filepath.Abs(pos[0])
	if err != nil {
		return cliFailure(output, fmt.Errorf("resolve plugin directory: %w", err))
	}
	if _, statErr := os.Stat(filepath.Join(absDir, "plugin.json")); statErr == nil {
		return cliFailure(output, fmt.Errorf("plugin.json already exists in %s", absDir))
	}

	scaffoldFiles, err := buildPluginScaffold(id, name, templateName, normExt)
	if err != nil {
		return cliFailure(output, err)
	}

	manifestRaw, ok := scaffoldFiles["plugin.json"]
	if !ok {
		return cliFailure(output, errors.New("internal error: scaffold missing plugin.json"))
	}
	manifest, err := pluginhost.ParseManifest(manifestRaw)
	if err != nil {
		return cliFailure(output, fmt.Errorf("manifest validation failed: %w", err))
	}

	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return cliFailure(output, fmt.Errorf("create plugin directory: %w", err))
	}

	relPaths := make([]string, 0, len(scaffoldFiles))
	for rel := range scaffoldFiles {
		relPaths = append(relPaths, rel)
	}
	sort.Strings(relPaths)

	for _, rel := range relPaths {
		dest := filepath.Join(absDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return cliFailure(output, fmt.Errorf("create directory for %s: %w", rel, err))
		}
		if err := os.WriteFile(dest, scaffoldFiles[rel], 0o644); err != nil {
			return cliFailure(output, fmt.Errorf("write %s: %w", rel, err))
		}
	}

	return cliSuccess(output, map[string]any{
		"ok":       true,
		"dir":      absDir,
		"id":       manifest.ID,
		"template": templateName,
		"files":    relPaths,
	})
}

func humanizePluginName(id string) string {
	parts := strings.FieldsFunc(id, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	if len(parts) == 0 {
		return id
	}
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}

func normalizeSingleExt(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" || v == "." {
		return ""
	}
	if !strings.HasPrefix(v, ".") {
		v = "." + v
	}
	return v
}

func buildPluginScaffold(id, name, templateName, ext string) (map[string][]byte, error) {
	switch templateName {
	case "esm-viewer":
		manifestDoc := map[string]any{
			"schema_version": 1,
			"id":             id,
			"name":           name,
			"version":        "0.1.0",
			"category":       "media",
			"description":    fmt.Sprintf("Astrorder ESM file viewer for %s files", ext),
			"author":         "Astrorder Plugin Developer",
			"permissions":    []string{pluginhost.PermUISlots},
			"backend": map[string]any{
				"type": pluginhost.BackendTypeNone,
			},
			"settings": map[string]any{
				"mode": pluginhost.SettingsModeNone,
			},
			"frontend": map[string]any{
				"mode":        pluginhost.FrontendModeESM,
				"entry":       "ui/entry.js",
				"sdk_version": "^1.0",
			},
			"contributes": map[string]any{
				"viewers": []map[string]any{
					{
						"id":                     id,
						"title":                  name,
						"extensions":             []string{ext},
						"priority":               90,
						"default_for_extensions": true,
					},
				},
			},
		}
		manifestBytes, err := marshalPrettyJSON(manifestDoc)
		if err != nil {
			return nil, err
		}
		pkgBytes, err := pluginPackageJSONTemplate(id, name)
		if err != nil {
			return nil, err
		}
		return map[string][]byte{
			"plugin.json":          manifestBytes,
			"package.json":         pkgBytes,
			"tsconfig.json":        []byte(pluginTSConfigTemplate()),
			"vite.config.ts":       []byte(pluginViteConfigTemplate()),
			"src/astrorder-sdk.d.ts": []byte(pluginSDKTypesTemplate()),
			"src/entry.tsx":        []byte(esmViewerEntryTemplate(id, name, ext)),
		}, nil

	case "iframe-viewer":
		manifestDoc := map[string]any{
			"schema_version": 1,
			"id":             id,
			"name":           name,
			"version":        "0.1.0",
			"category":       "media",
			"description":    fmt.Sprintf("Astrorder sandboxed iframe viewer for %s files", ext),
			"author":         "Astrorder Plugin Developer",
			"permissions":    []string{pluginhost.PermUISlots},
			"backend": map[string]any{
				"type": pluginhost.BackendTypeNone,
			},
			"settings": map[string]any{
				"mode": pluginhost.SettingsModeNone,
			},
			"frontend": map[string]any{
				"mode": pluginhost.FrontendModeIframe,
			},
			"contributes": map[string]any{
				"viewers": []map[string]any{
					{
						"id":                     id,
						"title":                  name,
						"extensions":             []string{ext},
						"ui_entry":               "ui/viewer.html",
						"priority":               90,
						"default_for_extensions": true,
					},
				},
			},
		}
		manifestBytes, err := marshalPrettyJSON(manifestDoc)
		if err != nil {
			return nil, err
		}
		return map[string][]byte{
			"plugin.json":    manifestBytes,
			"ui/viewer.html": []byte(iframeViewerHTMLTemplate(name, ext)),
		}, nil

	case "chat-hook":
		manifestDoc := map[string]any{
			"schema_version": 1,
			"id":             id,
			"name":           name,
			"version":        "0.1.0",
			"category":       "agent",
			"description":    fmt.Sprintf("Astrorder chat hook plugin (%s)", name),
			"author":         "Astrorder Plugin Developer",
			"permissions":    []string{pluginhost.PermUISlots},
			"backend": map[string]any{
				"type": pluginhost.BackendTypeNone,
			},
			"settings": map[string]any{
				"mode": pluginhost.SettingsModeNone,
			},
			"frontend": map[string]any{
				"mode":        pluginhost.FrontendModeESM,
				"entry":       "ui/entry.js",
				"sdk_version": "^1.0",
			},
			"contributes": map[string]any{},
		}
		manifestBytes, err := marshalPrettyJSON(manifestDoc)
		if err != nil {
			return nil, err
		}
		pkgBytes, err := pluginPackageJSONTemplate(id, name)
		if err != nil {
			return nil, err
		}
		return map[string][]byte{
			"plugin.json":          manifestBytes,
			"package.json":         pkgBytes,
			"tsconfig.json":        []byte(pluginTSConfigTemplate()),
			"vite.config.ts":       []byte(pluginViteConfigTemplate()),
			"src/astrorder-sdk.d.ts": []byte(pluginSDKTypesTemplate()),
			"src/entry.tsx":        []byte(chatHookEntryTemplate(id, name)),
		}, nil

	default:
		return nil, fmt.Errorf("unsupported template %q (expected esm-viewer, iframe-viewer, or chat-hook)", templateName)
	}
}

func pluginPackageJSONTemplate(id, name string) ([]byte, error) {
	doc := map[string]any{
		"name":        id,
		"version":     "0.1.0",
		"private":     true,
		"description": fmt.Sprintf("Astrorder ESM plugin (%s)", name),
		"type":        "module",
		"scripts": map[string]string{
			"build":     "vite build",
			"typecheck": "tsc --noEmit",
		},
		"devDependencies": map[string]string{
			"@types/node":  "^22.0.0",
			"@types/react": "^19.0.0",
			"react":        "^19.0.0",
			"typescript":   "^5.7.0",
			"vite":         "^6.0.0",
		},
	}
	return marshalPrettyJSON(doc)
}

func pluginTSConfigTemplate() string {
	return `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "jsx": "react-jsx",
    "strict": true,
    "noEmit": true,
    "skipLibCheck": true
  },
  "include": ["src"]
}
`
}

func pluginSDKTypesTemplate() string {
	return `// Self-contained ambient declarations for Astrorder's host-provided "@astrorder/sdk"
// runtime module (resolved via Astrorder's scoped Import Map at /api/v1/ext/_sdk/sdk.js).
// When developing inside the Astrorder repository, you may also reference "@astrorder/plugin-sdk/ambient".

declare module '@astrorder/sdk' {
  import type { ComponentType, ReactNode } from 'react'

  export interface Disposable {
    dispose(): void
  }

  export interface Session {
    id: string
    agent_id: string
    title: string
    workspace: string | null
    status: 'idle' | 'running' | 'waiting_approval' | 'error'
    updated_at: string
    connection_id?: string | null
    [key: string]: unknown
  }

  export interface ArtifactRef {
    id: string
    sessionId: string
    agentId: string
    connectionId?: string
    name: string
    kind: 'workspace_file' | 'attachment' | 'remote_url'
    path?: string
    readUrl: string
    mediaType: string
    writable: boolean
    metadata?: Record<string, unknown>
  }

  export interface ViewerContext {
    artifact: ArtifactRef
    onSave?: (content: string) => Promise<boolean>
    onDirtyChange?: (dirty: boolean) => void
    toolbarAction?: ReactNode
    onClose?: () => void
  }

  export interface PluginFileHandlerContribution {
    id?: string
    title: string
    description?: string
    extensions: string[]
    mimeTypes?: string[]
    priority?: number
    defaultForExtensions?: boolean
    canEdit?: boolean
    badgeLabel?: string
    tabColor?: string
    icon?: ComponentType<{ size?: number | string }>
    supports?: (artifact: ArtifactRef) => number
    component: ComponentType<ViewerContext>
  }

  export interface MessageSendContext {
    session: Session
    text: string
    attachments: File[]
    metadata: Record<string, unknown>
  }

  export interface MessageInterceptorResult {
    text?: string
    cancel?: boolean
    reason?: string
    metadata?: Record<string, unknown>
  }

  export interface PluginMessageInterceptorContribution {
    id: string
    order?: number
    beforeSend: (
      ctx: MessageSendContext,
    ) => void | MessageInterceptorResult | Promise<void | MessageInterceptorResult>
  }

  export interface MarkdownBlockRendererProps {
    code: string
    language: string
    session?: Session | null
  }

  export interface PluginMarkdownBlockRendererContribution {
    id: string
    title?: string
    languages: string[]
    priority?: number
    component: ComponentType<MarkdownBlockRendererProps>
  }

  export interface HostStreamEvent {
    type: string
    cursor?: number
    agent_id?: string
    session_id?: string
    data?: Record<string, unknown>
    timestamp?: string
    [key: string]: unknown
  }

  export interface PluginEventMeta {
    fromPluginId: string
    topic: string
    timestamp: number
  }

  export interface PluginNotifyOptions {
    message: string
    title?: string
    color?: string
    autoClose?: number | boolean
  }

  export interface WorkspaceFileReadResult {
    path: string
    content: string
    encoding: 'utf8' | 'base64'
    contentType: string
  }

  export interface AstrorderPluginApi {
    fetch(path: string, init?: RequestInit): Promise<Response>
    fetchExt<T = unknown>(subpath: string, init?: RequestInit): Promise<T>
    notify(opts: PluginNotifyOptions): void
    navigate(path: string): void
    openArtifact(artifact: ArtifactRef, viewerId?: string): void
    onHostEvent(pattern: string, handler: (event: HostStreamEvent) => void): Disposable
    onSessionEvent(sessionId: string, handler: (event: HostStreamEvent) => void): Disposable
    emitPluginEvent(topic: string, payload?: unknown): void
    onPluginEvent<T = unknown>(topic: string, handler: (payload: T, meta: PluginEventMeta) => void): Disposable
    getService<T = unknown>(serviceId: string): T | undefined
    useService<T = unknown>(serviceId: string): T | undefined
    readWorkspaceFile(
      filePath: string,
      opts?: { sessionId?: string; workspace?: string; connectionId?: string; encoding?: 'utf8' | 'base64' },
    ): Promise<WorkspaceFileReadResult>
    writeWorkspaceFile(
      filePath: string,
      content: string,
      opts?: { sessionId?: string; workspace?: string; connectionId?: string },
    ): Promise<void>
    listWorkspaceFiles(opts?: {
      path?: string
      depth?: number
      sessionId?: string
      workspace?: string
      connectionId?: string
    }): Promise<{ root: string; items: Array<{ name: string; path: string; is_dir: boolean; size?: number }> }>
  }

  export interface AstrorderPluginContext {
    readonly pluginId: string
    readonly version: string
    readonly subscriptions: Disposable[]
    registerFileHandler(spec: PluginFileHandlerContribution): Disposable
    registerMessageInterceptor(spec: PluginMessageInterceptorContribution): Disposable
    registerMarkdownBlockRenderer(spec: PluginMarkdownBlockRendererContribution): Disposable
    registerService<T>(serviceId: string, impl: T): Disposable
    readonly api: AstrorderPluginApi
  }

  export interface AstrorderPluginModule {
    activate(ctx: AstrorderPluginContext): void | Promise<void>
    deactivate?(): void | Promise<void>
  }

  export function defineAstrorderPlugin<T extends AstrorderPluginModule>(mod: T): T
}
`
}

func pluginViteConfigTemplate() string {
	return `import { copyFileSync, existsSync, mkdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'

const pluginRoot = path.dirname(fileURLToPath(import.meta.url))

// Bare specifiers mapped at runtime by Astrorder's scoped Import Map (/api/v1/ext/_sdk/*).
const ASTRORDER_HOST_EXTERNALS = new Set([
  'react',
  'react/jsx-runtime',
  'react-dom',
  'react-dom/client',
  '@mantine/core',
  '@mantine/notifications',
  '@astrorder/sdk',
  'react-router-dom',
  '@tanstack/react-query',
  '@astrorder/workspace-bridge',
])

export default defineConfig({
  root: pluginRoot,
  mode: 'production',
  base: './',
  define: {
    'process.env.NODE_ENV': JSON.stringify('production'),
  },
  plugins: [
    {
      name: 'astrorder-host-externals',
      enforce: 'pre',
      resolveId(source) {
        if (ASTRORDER_HOST_EXTERNALS.has(source)) {
          return { id: source, external: true }
        }
        return null
      },
    },
    {
      name: 'astrorder-copy-manifest',
      closeBundle() {
        const src = path.resolve(pluginRoot, 'plugin.json')
        const outDir = path.resolve(pluginRoot, 'dist')
        if (!existsSync(src)) return
        mkdirSync(outDir, { recursive: true })
        copyFileSync(src, path.join(outDir, 'plugin.json'))
      },
    },
  ],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
    cssCodeSplit: false,
    lib: {
      entry: path.resolve(pluginRoot, 'src/entry.tsx'),
      formats: ['es'],
      fileName: () => 'ui/entry.js',
    },
    rollupOptions: {
      external: (id) => ASTRORDER_HOST_EXTERNALS.has(id),
      output: {
        entryFileNames: 'ui/entry.js',
        chunkFileNames: 'ui/chunks/[name]-[hash].js',
        assetFileNames: (assetInfo) => {
          const names = assetInfo.names || (assetInfo.name ? [assetInfo.name] : [])
          if (names.some((name) => name.endsWith('.css'))) {
            return 'ui/entry.css'
          }
          return 'ui/assets/[name]-[hash][extname]'
        },
      },
    },
  },
})
`
}

func esmViewerEntryTemplate(id, name, ext string) string {
	idJSON, _ := json.Marshal(id)
	nameJSON, _ := json.Marshal(name)
	extJSON, _ := json.Marshal(ext)
	return fmt.Sprintf(`import { useEffect, useState } from 'react'
import {
  defineAstrorderPlugin,
  type AstrorderPluginContext,
  type ViewerContext,
} from '@astrorder/sdk'

function PluginFileViewer({ artifact, toolbarAction, onSave, onDirtyChange }: ViewerContext) {
  const [content, setContent] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    fetch(artifact.readUrl, { credentials: 'include' })
      .then((res) => {
        if (!res.ok) throw new Error('HTTP ' + res.status)
        return res.text()
      })
      .then((text) => {
        if (!cancelled) {
          setContent(text)
          onDirtyChange?.(false)
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err))
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [artifact.readUrl, onDirtyChange])

  async function handleSave() {
    if (!onSave || saving) return
    setSaving(true)
    try {
      const ok = await onSave(content)
      if (ok) onDirtyChange?.(false)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%%', gap: 8, padding: 12 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
        <strong>{artifact.name}</strong>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          {artifact.writable && onSave ? (
            <button type="button" onClick={handleSave} disabled={saving}>
              {saving ? 'Saving...' : 'Save'}
            </button>
          ) : null}
          {toolbarAction}
        </div>
      </div>
      {error ? <div style={{ color: 'var(--mantine-color-red-6)' }}>{error}</div> : null}
      {loading ? (
        <div>Loading...</div>
      ) : (
        <textarea
          value={content}
          readOnly={!artifact.writable || !onSave}
          onChange={(e) => {
            setContent(e.target.value)
            onDirtyChange?.(true)
          }}
          style={{ flex: 1, width: '100%%', fontFamily: 'monospace', padding: 8 }}
        />
      )}
    </div>
  )
}

export function activate(ctx: AstrorderPluginContext): void {
  ctx.registerFileHandler({
    id: %s,
    title: %s,
    extensions: [%s],
    priority: 90,
    defaultForExtensions: true,
    canEdit: true,
    component: PluginFileViewer,
  })
}

export function deactivate(): void {}

export default defineAstrorderPlugin({
  activate,
  deactivate,
})
`, string(idJSON), string(nameJSON), string(extJSON))
}

func iframeViewerHTMLTemplate(name, ext string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>%s</title>
    <script src="/api/v1/ext/_sdk/bridge.js"></script>
    <style>
      body {
        margin: 0;
        padding: 12px;
        font-family: system-ui, -apple-system, sans-serif;
        display: flex;
        flex-direction: column;
        height: 100vh;
        box-sizing: border-box;
        gap: 8px;
      }
      header {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 8px;
      }
      textarea {
        flex: 1;
        width: 100%%;
        font-family: ui-monospace, SFMono-Regular, monospace;
        padding: 8px;
        box-sizing: border-box;
      }
    </style>
  </head>
  <body>
    <header>
      <strong id="file-title">%s (%s)</strong>
      <div>
        <button id="reload-btn" type="button">Reload</button>
        <button id="save-btn" type="button">Save</button>
      </div>
    </header>
    <textarea id="editor" placeholder="Loading file..."></textarea>
    <script>
      (function () {
        var titleEl = document.getElementById('file-title');
        var editorEl = document.getElementById('editor');
        var reloadBtn = document.getElementById('reload-btn');
        var saveBtn = document.getElementById('save-btn');

        async function loadFile() {
          try {
            var res = await window.astrorderPlugin.readFile({ encoding: 'utf8' });
            if (res && typeof res.path === 'string' && res.path) {
              titleEl.textContent = res.path;
            }
            editorEl.value = res && typeof res.content === 'string' ? res.content : '';
          } catch (err) {
            window.astrorderPlugin.toast('Read failed: ' + err.message, 'error');
          }
        }

        async function persistFile() {
          try {
            await window.astrorderPlugin.saveFile(editorEl.value);
            window.astrorderPlugin.toast('Saved successfully', 'success');
          } catch (err) {
            window.astrorderPlugin.toast('Save failed: ' + err.message, 'error');
          }
        }

        reloadBtn.addEventListener('click', loadFile);
        saveBtn.addEventListener('click', persistFile);
        window.astrorderPlugin.onContextChange(function () {
          loadFile();
        });
        loadFile();
      })();
    </script>
  </body>
</html>
`, htmlEscapeText(name), htmlEscapeText(name), htmlEscapeText(ext))
}

func htmlEscapeText(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}

func chatHookEntryTemplate(id, name string) string {
	idJSON, _ := json.Marshal(id)
	nameJSON, _ := json.Marshal(name)
	langTag := strings.ReplaceAll(id, ".", "-")
	langJSON, _ := json.Marshal(langTag)
	svcIDJSON, _ := json.Marshal(id + ".service")
	return fmt.Sprintf(`import {
  defineAstrorderPlugin,
  type AstrorderPluginContext,
  type MarkdownBlockRendererProps,
} from '@astrorder/sdk'

function CustomFenceRenderer({ code, language }: MarkdownBlockRendererProps) {
  return (
    <div style={{ border: '1px solid var(--mantine-color-default-border)', borderRadius: 6, padding: 10 }}>
      <div style={{ fontSize: 12, opacity: 0.7, marginBottom: 6 }}>{language}</div>
      <pre style={{ margin: 0, overflowX: 'auto' }}>
        <code>{code}</code>
      </pre>
    </div>
  )
}

export function activate(ctx: AstrorderPluginContext): void {
  const state = { eventCount: 0 }

  ctx.registerService(%s, {
    getEventCount: () => state.eventCount,
  })

  ctx.registerMessageInterceptor({
    id: %s + ':before-send',
    order: 50,
    beforeSend(sendCtx) {
      return { text: sendCtx.text }
    },
  })

  ctx.registerMarkdownBlockRenderer({
    id: %s + ':fence',
    title: %s,
    languages: [%s],
    component: CustomFenceRenderer,
  })

  ctx.api.onHostEvent('session.*', () => {
    state.eventCount += 1
  })
}

export function deactivate(): void {}

export default defineAstrorderPlugin({
  activate,
  deactivate,
})
`, string(svcIDJSON), string(idJSON), string(idJSON), string(nameJSON), string(langJSON))
}

func runPluginKeygen(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("plugin keygen", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	outDir := flags.String("out-dir", "", "directory to write Ed25519 key files")
	keyID := flags.String("key-id", "default", "key identifier prefix")

	pos, err := parseFlagsFlexible(flags, args)
	if err != nil {
		return cliFailure(output, err)
	}
	if len(pos) != 0 {
		return cliFailure(output, errors.New("unexpected positional arguments for plugin keygen"))
	}
	if strings.TrimSpace(*outDir) == "" {
		return cliFailure(output, errors.New("plugin keygen requires --out-dir"))
	}
	cleanKeyID := strings.TrimSpace(*keyID)
	if err := validateKeyID(cleanKeyID); err != nil {
		return cliFailure(output, err)
	}

	absOutDir, err := filepath.Abs(*outDir)
	if err != nil {
		return cliFailure(output, fmt.Errorf("resolve out-dir: %w", err))
	}
	if err := os.MkdirAll(absOutDir, 0o755); err != nil {
		return cliFailure(output, fmt.Errorf("create out-dir: %w", err))
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return cliFailure(output, fmt.Errorf("generate ed25519 keypair: %w", err))
	}

	privB64 := base64.StdEncoding.EncodeToString(priv)
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	privPath := filepath.Join(absOutDir, cleanKeyID+".private.key")
	pubPath := filepath.Join(absOutDir, cleanKeyID+".public.key")

	if err := writeFileMode(privPath, []byte(privB64+"\n"), 0o600); err != nil {
		return cliFailure(output, fmt.Errorf("write private key: %w", err))
	}
	if err := writeFileMode(pubPath, []byte(pubB64+"\n"), 0o644); err != nil {
		return cliFailure(output, fmt.Errorf("write public key: %w", err))
	}

	return cliSuccess(output, map[string]any{
		"ok":                true,
		"key_id":            cleanKeyID,
		"private_key_path":  privPath,
		"public_key_path":   pubPath,
		"public_key_base64": pubB64,
	})
}

func validateKeyID(keyID string) error {
	if keyID == "" {
		return errors.New("key-id must not be empty")
	}
	if strings.ContainsAny(keyID, "/\\ \t\r\n\x00") || strings.Contains(keyID, "..") || strings.HasPrefix(keyID, ".") {
		return fmt.Errorf("invalid key-id %q", keyID)
	}
	return nil
}

func writeFileMode(target string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(target, mode)
}

func runPluginSign(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("plugin sign", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	keyPath := flags.String("key", "", "path to base64 Ed25519 private key file")
	keyID := flags.String("key-id", "", "signing key identifier")

	pos, err := parseFlagsFlexible(flags, args)
	if err != nil {
		return cliFailure(output, err)
	}
	if len(pos) != 1 {
		return cliFailure(output, errors.New("usage: astrorderctl plugin sign <dir> --key <private-key-file> --key-id <id>"))
	}
	if strings.TrimSpace(*keyPath) == "" || strings.TrimSpace(*keyID) == "" {
		return cliFailure(output, errors.New("plugin sign requires --key and --key-id"))
	}

	absDir, err := filepath.Abs(pos[0])
	if err != nil {
		return cliFailure(output, fmt.Errorf("resolve plugin directory: %w", err))
	}

	privKey, err := loadEd25519PrivateKey(*keyPath)
	if err != nil {
		return cliFailure(output, err)
	}

	manifestPath := filepath.Join(absDir, "plugin.json")
	manifest, err := pluginhost.LoadManifestFile(manifestPath)
	if err != nil {
		return cliFailure(output, err)
	}

	sig, err := pluginhost.SignPluginDir(absDir, strings.TrimSpace(*keyID), privKey)
	if err != nil {
		return cliFailure(output, fmt.Errorf("sign plugin directory: %w", err))
	}

	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return cliFailure(output, fmt.Errorf("read plugin.json: %w", err))
	}
	var manifestMap map[string]any
	if err := json.Unmarshal(rawManifest, &manifestMap); err != nil {
		return cliFailure(output, fmt.Errorf("parse plugin.json: %w", err))
	}
	manifestMap["signature"] = sig

	updatedBytes, err := marshalPrettyJSON(manifestMap)
	if err != nil {
		return cliFailure(output, fmt.Errorf("encode signed plugin.json: %w", err))
	}
	if _, err := pluginhost.ParseManifest(updatedBytes); err != nil {
		return cliFailure(output, fmt.Errorf("validate signed plugin.json: %w", err))
	}
	if err := writeFileAtomic(manifestPath, updatedBytes, 0o644); err != nil {
		return cliFailure(output, fmt.Errorf("write signed plugin.json: %w", err))
	}

	digest, err := pluginhost.ComputePluginContentDigest(absDir)
	if err != nil {
		return cliFailure(output, fmt.Errorf("compute plugin content digest: %w", err))
	}

	return cliSuccess(output, map[string]any{
		"ok":        true,
		"id":        manifest.ID,
		"key_id":    sig.KeyID,
		"algorithm": sig.Algorithm,
		"digest":    digest,
	})
}

func runPluginVerify(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("plugin verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	pubKeySpec := flags.String("pub-key", "", "Ed25519 public key file path or raw base64")

	pos, err := parseFlagsFlexible(flags, args)
	if err != nil {
		return cliFailure(output, err)
	}
	if len(pos) != 1 {
		return cliFailure(output, errors.New("usage: astrorderctl plugin verify <dir> --pub-key <public-key-file-or-base64>"))
	}
	if strings.TrimSpace(*pubKeySpec) == "" {
		return cliFailure(output, errors.New("plugin verify requires --pub-key"))
	}

	absDir, err := filepath.Abs(pos[0])
	if err != nil {
		return cliFailure(output, fmt.Errorf("resolve plugin directory: %w", err))
	}

	pubKey, err := loadEd25519PublicKey(*pubKeySpec)
	if err != nil {
		return cliFailure(output, err)
	}

	manifest, err := pluginhost.LoadManifestFile(filepath.Join(absDir, "plugin.json"))
	if err != nil {
		return cliFailure(output, err)
	}
	if manifest.Signature == nil || strings.TrimSpace(manifest.Signature.Value) == "" {
		return cliFailure(output, fmt.Errorf("plugin %q is not signed (missing signature block in plugin.json)", manifest.ID))
	}

	keyID := strings.TrimSpace(manifest.Signature.KeyID)
	trusted := map[string]ed25519.PublicKey{keyID: pubKey}
	if err := verifyPluginDirSignature(absDir, manifest, trusted); err != nil {
		return cliFailure(output, err)
	}

	return cliSuccess(output, map[string]any{
		"ok":       true,
		"id":       manifest.ID,
		"key_id":   keyID,
		"verified": true,
	})
}

func loadEd25519PrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	decoded, err := decodeBase64Key(string(raw))
	if err != nil {
		return nil, fmt.Errorf("decode private key base64: %w", err)
	}
	if len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid ed25519 private key length %d (expected %d bytes)", len(decoded), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(decoded), nil
}

func loadEd25519PublicKey(spec string) (ed25519.PublicKey, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return nil, errors.New("public key is empty")
	}
	rawText := trimmed
	if info, err := os.Stat(trimmed); err == nil && !info.IsDir() {
		raw, readErr := os.ReadFile(trimmed)
		if readErr != nil {
			return nil, fmt.Errorf("read public key file: %w", readErr)
		}
		rawText = string(raw)
	}
	decoded, err := decodeBase64Key(rawText)
	if err != nil {
		return nil, fmt.Errorf("decode public key base64: %w", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid ed25519 public key length %d (expected %d bytes)", len(decoded), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(decoded), nil
}

func decodeBase64Key(raw string) ([]byte, error) {
	clean := strings.TrimSpace(raw)
	if b, err := base64.StdEncoding.DecodeString(clean); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(clean)
}

// verifyPluginDirSignature verifies manifest.Signature against pluginDir using the
// canonical payload format signed by pluginhost.SignPluginDir.
func verifyPluginDirSignature(pluginDir string, manifest pluginhost.Manifest, trustedKeys map[string]ed25519.PublicKey) error {
	if manifest.Signature == nil {
		return errors.New("missing plugin signature")
	}
	if !strings.EqualFold(strings.TrimSpace(manifest.Signature.Algorithm), "ed25519") {
		return fmt.Errorf("unsupported signature algorithm %q", manifest.Signature.Algorithm)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(manifest.Signature.Value))
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return errors.New("malformed ed25519 signature value")
	}

	payload, err := buildCanonicalSignaturePayload(manifest, pluginDir)
	if err != nil {
		return fmt.Errorf("build signature payload: %w", err)
	}

	keyID := strings.TrimSpace(manifest.Signature.KeyID)
	if keyID != "" {
		pub, ok := trustedKeys[keyID]
		if !ok || len(pub) != ed25519.PublicKeySize {
			return fmt.Errorf("no trusted ed25519 public key for key_id %q", keyID)
		}
		if !ed25519.Verify(pub, payload, sigBytes) {
			return errors.New("signature verification failed: content tampered or key mismatch")
		}
		return nil
	}
	for _, pub := range trustedKeys {
		if len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, payload, sigBytes) {
			return nil
		}
	}
	return errors.New("signature verification failed: content tampered or key mismatch")
}

func buildCanonicalSignaturePayload(manifest pluginhost.Manifest, pluginDir string) ([]byte, error) {
	canonical := manifest
	canonical.Signature = nil
	canonical.Normalize()
	manifestJSON, err := json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	manifestSum := sha256.Sum256(manifestJSON)

	type fileDigest struct {
		rel string
		sum string
	}
	absRoot, err := filepath.Abs(pluginDir)
	if err != nil {
		return nil, err
	}
	var digests []fileDigest
	err = filepath.WalkDir(absRoot, func(curr string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("symlinks are not allowed in signed plugin directory")
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(absRoot, curr)
		if err != nil {
			return err
		}
		slashRel := filepath.ToSlash(rel)
		if slashRel == "plugin.json" {
			return nil
		}
		sum, err := sha256FileHex(curr)
		if err != nil {
			return err
		}
		digests = append(digests, fileDigest{rel: slashRel, sum: sum})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(digests, func(i, j int) bool {
		return digests[i].rel < digests[j].rel
	})

	var buf bytes.Buffer
	buf.WriteString(pluginSignatureDomainPrefixV1)
	buf.WriteString(hex.EncodeToString(manifestSum[:]))
	buf.WriteByte('\n')
	for _, fd := range digests {
		buf.WriteString(fd.rel)
		buf.WriteByte(0)
		buf.WriteString(fd.sum)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func runPluginPack(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("plugin pack", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	outPath := flags.String("out", "", "output zip archive path")

	pos, err := parseFlagsFlexible(flags, args)
	if err != nil {
		return cliFailure(output, err)
	}
	if len(pos) != 1 {
		return cliFailure(output, errors.New("usage: astrorderctl plugin pack <dir> [--out <path.zip>]"))
	}

	absDir, err := filepath.Abs(pos[0])
	if err != nil {
		return cliFailure(output, fmt.Errorf("resolve plugin directory: %w", err))
	}
	realRoot, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		return cliFailure(output, fmt.Errorf("inspect plugin directory: %w", err))
	}

	manifest, err := pluginhost.LoadManifestFile(filepath.Join(absDir, "plugin.json"))
	if err != nil {
		return cliFailure(output, err)
	}

	if err := verifyDeclaredPluginAssets(realRoot, manifest); err != nil {
		return cliFailure(output, err)
	}

	targetZip := strings.TrimSpace(*outPath)
	if targetZip == "" {
		targetZip = filepath.Join(absDir, fmt.Sprintf("%s-%s.zip", manifest.ID, manifest.Version))
	}
	absOut, err := filepath.Abs(targetZip)
	if err != nil {
		return cliFailure(output, fmt.Errorf("resolve output path: %w", err))
	}

	entries, err := collectPackEntries(absDir, realRoot, absOut)
	if err != nil {
		return cliFailure(output, err)
	}
	if len(entries) == 0 {
		return cliFailure(output, errors.New("no packable files found in plugin directory"))
	}

	if err := os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil {
		return cliFailure(output, fmt.Errorf("create archive parent directory: %w", err))
	}
	size, shaHex, err := writeDeterministicZip(absOut, entries)
	if err != nil {
		return cliFailure(output, fmt.Errorf("write plugin archive: %w", err))
	}

	return cliSuccess(output, map[string]any{
		"ok":      true,
		"id":      manifest.ID,
		"version": manifest.Version,
		"archive": absOut,
		"size":    size,
		"sha256":  shaHex,
	})
}

func verifyDeclaredPluginAssets(realRoot string, manifest pluginhost.Manifest) error {
	checkRel := func(rel, label string) error {
		cleanRel, err := pluginhost.ValidateRelativePluginPath(rel)
		if err != nil {
			return fmt.Errorf("invalid %s path %q: %w", label, rel, err)
		}
		candidate := filepath.Join(realRoot, filepath.FromSlash(cleanRel))
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return fmt.Errorf("declared %s %q does not exist: %w", label, cleanRel, err)
		}
		if !isStrictlyWithinDir(realRoot, resolved) {
			return fmt.Errorf("declared %s %q escapes plugin directory", label, cleanRel)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("declared %s %q is not a regular file", label, cleanRel)
		}
		return nil
	}

	if manifest.Frontend != nil {
		if manifest.Frontend.Entry != "" {
			if err := checkRel(manifest.Frontend.Entry, "frontend.entry"); err != nil {
				return err
			}
		}
		for _, st := range manifest.Frontend.Styles {
			if err := checkRel(st, "frontend.styles"); err != nil {
				return err
			}
		}
	}
	if manifest.Settings.UIEntry != "" {
		if err := checkRel(manifest.Settings.UIEntry, "settings.ui_entry"); err != nil {
			return err
		}
	}
	for _, v := range manifest.Contributes.Viewers {
		if v.UIEntry != "" {
			if err := checkRel(v.UIEntry, fmt.Sprintf("viewer %q ui_entry", v.ID)); err != nil {
				return err
			}
		}
	}
	for _, p := range manifest.Contributes.SidecarPanels {
		if p.UIEntry != "" {
			if err := checkRel(p.UIEntry, fmt.Sprintf("sidecar panel %q ui_entry", p.ID)); err != nil {
				return err
			}
		}
	}
	for _, skill := range manifest.Contributes.AgentSkills {
		if err := checkRel(skill, "agent_skills"); err != nil {
			return err
		}
	}
	return nil
}

type packEntry struct {
	relSlash string
	srcPath  string
	mode     os.FileMode
}

func collectPackEntries(absDir, realRoot, absOut string) ([]packEntry, error) {
	cleanOut := filepath.Clean(absOut)
	var entries []packEntry

	err := filepath.WalkDir(absDir, func(curr string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if curr == absDir {
			return nil
		}
		name := d.Name()
		if name == "node_modules" || name == ".git" || strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Clean(curr) == cleanOut {
			return nil
		}

		// Handle symlinks: skip any symlink escaping plugin root or pointing to directories.
		srcPath := curr
		if d.Type()&os.ModeSymlink != 0 {
			resolved, evalErr := filepath.EvalSymlinks(curr)
			if evalErr != nil || !isStrictlyWithinDir(realRoot, resolved) {
				return nil
			}
			targetInfo, statErr := os.Stat(resolved)
			if statErr != nil || targetInfo.IsDir() || !targetInfo.Mode().IsRegular() {
				return nil
			}
			srcPath = resolved
		} else if d.IsDir() {
			return nil
		}

		info, err := os.Stat(srcPath)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(absDir, curr)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		entries = append(entries, packEntry{
			relSlash: filepath.ToSlash(rel),
			srcPath:  srcPath,
			mode:     mode,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].relSlash < entries[j].relSlash
	})
	return entries, nil
}

func writeDeterministicZip(absOut string, entries []packEntry) (int64, string, error) {
	tmpFile, err := os.CreateTemp(filepath.Dir(absOut), ".astrorder-pack-*.zip")
	if err != nil {
		return 0, "", err
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	zw := zip.NewWriter(tmpFile)
	fixedTime := time.Unix(0, 0).UTC()
	for _, entry := range entries {
		hdr := &zip.FileHeader{
			Name:     entry.relSlash,
			Method:   zip.Deflate,
			Modified: fixedTime,
		}
		hdr.SetMode(entry.mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			_ = zw.Close()
			_ = tmpFile.Close()
			return 0, "", err
		}
		f, err := os.Open(entry.srcPath)
		if err != nil {
			_ = zw.Close()
			_ = tmpFile.Close()
			return 0, "", err
		}
		_, copyErr := io.Copy(w, f)
		closeErr := f.Close()
		if copyErr != nil {
			_ = zw.Close()
			_ = tmpFile.Close()
			return 0, "", copyErr
		}
		if closeErr != nil {
			_ = zw.Close()
			_ = tmpFile.Close()
			return 0, "", closeErr
		}
	}
	if err := zw.Close(); err != nil {
		_ = tmpFile.Close()
		return 0, "", err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return 0, "", err
	}
	if err := tmpFile.Close(); err != nil {
		return 0, "", err
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		return 0, "", err
	}
	shaHex, err := sha256FileHex(tmpPath)
	if err != nil {
		return 0, "", err
	}
	if err := os.Rename(tmpPath, absOut); err != nil {
		return 0, "", err
	}
	cleanup = false
	return info.Size(), shaHex, nil
}

type pluginFileStamp struct {
	Size            int64
	ModTimeUnixNano int64
}

type pluginDirSnapshot map[string]pluginFileStamp

func snapshotPluginDir(dir string) (pluginDirSnapshot, error) {
	absRoot, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	snap := make(pluginDirSnapshot)
	err = filepath.WalkDir(absRoot, func(curr string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if curr == absRoot {
			return nil
		}
		name := d.Name()
		if name == "node_modules" || name == ".git" || strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(absRoot, curr)
		if err != nil {
			return err
		}
		snap[filepath.ToSlash(rel)] = pluginFileStamp{
			Size:            info.Size(),
			ModTimeUnixNano: info.ModTime().UnixNano(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

func diffPluginSnapshots(prev, curr pluginDirSnapshot) []string {
	var changed []string
	for rel, stamp := range curr {
		if oldStamp, ok := prev[rel]; !ok || oldStamp != stamp {
			changed = append(changed, rel)
		}
	}
	for rel := range prev {
		if _, ok := curr[rel]; !ok {
			changed = append(changed, rel)
		}
	}
	sort.Strings(changed)
	return changed
}

func triggerInstall(ctx context.Context, client *http.Client, serverURL, token, absDir string) error {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	body, err := json.Marshal(map[string]string{"source_path": absDir})
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(strings.TrimSpace(serverURL), "/") + pluginhost.BaseAdminPath + "/install"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if t := strings.TrimSpace(token); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("POST %s failed with status %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func triggerReload(ctx context.Context, client *http.Client, serverURL, token string) error {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	endpoint := strings.TrimRight(strings.TrimSpace(serverURL), "/") + pluginhost.BaseAdminPath + "/reload"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	if t := strings.TrimSpace(token); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("POST %s failed with status %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

type pluginDevOptions struct {
	Dir        string
	Server     string
	Token      string
	IntervalMS int
	NoInstall  bool
	Client     *http.Client
}

func runPluginDev(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("plugin dev", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	server := flags.String("server", "http://127.0.0.1:30001", "Astrorder App server base URL")
	token := flags.String("token", "", "optional bearer authentication token")
	intervalMS := flags.Int("interval-ms", 800, "directory polling interval in milliseconds")
	noInstall := flags.Bool("no-install", false, "skip initial POST /api/v1/extensions/install")

	pos, err := parseFlagsFlexible(flags, args)
	if err != nil {
		return cliFailure(output, err)
	}
	if len(pos) != 1 {
		return cliFailure(output, errors.New("usage: astrorderctl plugin dev <dir> [--server http://127.0.0.1:30001] [--token <bearer-token>] [--interval-ms 800] [--no-install]"))
	}
	if *intervalMS <= 0 {
		return cliFailure(output, errors.New("interval-ms must be greater than 0"))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = runPluginDevLoop(ctx, pluginDevOptions{
		Dir:        pos[0],
		Server:     *server,
		Token:      *token,
		IntervalMS: *intervalMS,
		NoInstall:  *noInstall,
	}, output)
	if err != nil {
		return cliFailure(output, err)
	}
	return 0
}

func runPluginDevLoop(ctx context.Context, opts pluginDevOptions, output io.Writer) error {
	absDir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return fmt.Errorf("resolve plugin directory: %w", err)
	}
	manifest, err := pluginhost.LoadManifestFile(filepath.Join(absDir, "plugin.json"))
	if err != nil {
		return err
	}
	server := strings.TrimRight(strings.TrimSpace(opts.Server), "/")
	if server == "" {
		server = "http://127.0.0.1:30001"
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	if !opts.NoInstall {
		if err := triggerInstall(ctx, client, server, opts.Token, absDir); err != nil {
			return err
		}
		_ = json.NewEncoder(output).Encode(map[string]any{
			"ok":        true,
			"event":     "installed",
			"id":        manifest.ID,
			"dir":       absDir,
			"server":    server,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	}

	prevSnap, err := snapshotPluginDir(absDir)
	if err != nil {
		return fmt.Errorf("snapshot plugin directory: %w", err)
	}
	_ = json.NewEncoder(output).Encode(map[string]any{
		"ok":          true,
		"event":       "watching",
		"id":          manifest.ID,
		"dir":         absDir,
		"server":      server,
		"interval_ms": opts.IntervalMS,
		"files":       len(prevSnap),
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	})

	ticker := time.NewTicker(time.Duration(opts.IntervalMS) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = json.NewEncoder(output).Encode(map[string]any{
				"ok":        true,
				"event":     "stopped",
				"id":        manifest.ID,
				"timestamp": time.Now().UTC().Format(time.RFC3339),
			})
			return nil
		case <-ticker.C:
			currSnap, snapErr := snapshotPluginDir(absDir)
			if snapErr != nil {
				_ = json.NewEncoder(output).Encode(map[string]any{
					"ok":        false,
					"event":     "scan_error",
					"id":        manifest.ID,
					"error":     snapErr.Error(),
					"timestamp": time.Now().UTC().Format(time.RFC3339),
				})
				continue
			}
			changed := diffPluginSnapshots(prevSnap, currSnap)
			if len(changed) == 0 {
				continue
			}
			prevSnap = currSnap
			if reloadErr := triggerReload(ctx, client, server, opts.Token); reloadErr != nil {
				_ = json.NewEncoder(output).Encode(map[string]any{
					"ok":        false,
					"event":     "reload_error",
					"id":        manifest.ID,
					"changed":   changed,
					"error":     reloadErr.Error(),
					"timestamp": time.Now().UTC().Format(time.RFC3339),
				})
				continue
			}
			_ = json.NewEncoder(output).Encode(map[string]any{
				"ok":        true,
				"event":     "reloaded",
				"id":        manifest.ID,
				"changed":   changed,
				"timestamp": time.Now().UTC().Format(time.RFC3339),
			})
		}
	}
}

func marshalPrettyJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeFileAtomic(target string, data []byte, mode os.FileMode) error {
	tmpFile, err := os.CreateTemp(filepath.Dir(target), ".plugin-atomic-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func sha256FileHex(filename string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isStrictlyWithinDir(baseDir, candidate string) bool {
	absBase, err1 := filepath.Abs(baseDir)
	absCand, err2 := filepath.Abs(candidate)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(absBase, absCand)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
