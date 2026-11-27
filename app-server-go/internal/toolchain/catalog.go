package toolchain

import (
	"fmt"
	"strings"
)

type ItemKind string

const (
	ItemKindTool  ItemKind = "tool"
	ItemKindAgent ItemKind = "agent"
)

type ToolGroup string

const (
	ToolGroupCore  ToolGroup = "core"
	ToolGroupExtra ToolGroup = "extra"
)

type InstallMode string

const (
	InstallModeBackground  InstallMode = "background"
	InstallModeInteractive InstallMode = "interactive"
)

type InstallRecipe struct {
	ID           string      `json:"id"`
	Label        string      `json:"label"`
	Mode         InstallMode `json:"mode"`
	Command      string      `json:"command"`          // 展示/交互终端运行的命令
	Shell        string      `json:"shell,omitempty"`  // "powershell" | "sh"
	RequiresSudo bool        `json:"requires_sudo,omitempty"`
	Note         string      `json:"note,omitempty"`
}

type ToolSpec struct {
	ID           string    `json:"id"`
	Kind         ItemKind  `json:"kind"`
	Group        ToolGroup `json:"group,omitempty"`
	PluginID     string    `json:"plugin_id,omitempty"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Binaries     []string  `json:"binaries"`
	VersionArgs  []string  `json:"version_args"`
	Dependencies []string  `json:"dependencies,omitempty"`
	AuthCommand  string    `json:"auth_command,omitempty"`
	AuthHint     string    `json:"auth_hint,omitempty"`
	DocsURL      string    `json:"docs_url,omitempty"`
}

var toolCatalogSpecs = []ToolSpec{
	// Core tools
	{
		ID:          "uv",
		Kind:        ItemKindTool,
		Group:       ToolGroupCore,
		PluginID:    "toolchain-uv",
		Name:        "uv",
		Description: "极速 Python 包与虚拟环境管理器（自带 uvx，众多 MCP 与 Hermes 依赖）",
		Binaries:    []string{"uv", "uvx"},
		VersionArgs: []string{"--version"},
		DocsURL:     "https://docs.astral.sh/uv/",
	},
	{
		ID:          "node",
		Kind:        ItemKindTool,
		Group:       ToolGroupCore,
		PluginID:    "toolchain-node",
		Name:        "Node.js / npm",
		Description: "JavaScript 运行时与包管理器（含 npm / npx，Codex 与大量 MCP 依赖）",
		Binaries:    []string{"node", "npm"},
		VersionArgs: []string{"--version"},
		DocsURL:     "https://nodejs.org/",
	},
	{
		ID:          "git",
		Kind:        ItemKindTool,
		Group:       ToolGroupCore,
		PluginID:    "toolchain-git",
		Name:        "Git",
		Description: "分布式版本控制工具（代码检查点、Worktree 分叉与 Diff 面板依赖）",
		Binaries:    []string{"git"},
		VersionArgs: []string{"--version"},
		DocsURL:     "https://git-scm.com/",
	},
	{
		ID:          "rg",
		Kind:        ItemKindTool,
		Group:       ToolGroupCore,
		PluginID:    "toolchain-rg",
		Name:        "ripgrep (rg)",
		Description: "高性能全文代码检索工具（Agent 代码搜索常用加速组件）",
		Binaries:    []string{"rg"},
		VersionArgs: []string{"--version"},
		DocsURL:     "https://github.com/BurntSushi/ripgrep",
	},
	// Extra tools
	{
		ID:          "gh",
		Kind:        ItemKindTool,
		Group:       ToolGroupExtra,
		PluginID:    "toolchain-gh",
		Name:        "GitHub CLI (gh)",
		Description: "官方 GitHub 命令行工具，便于 Agent 创建 PR、查看 Issue 与 CI 日志",
		Binaries:    []string{"gh"},
		VersionArgs: []string{"--version"},
		AuthCommand: "gh auth login",
		AuthHint:    "安装后可在终端执行 gh auth login 授权 GitHub 账号",
		DocsURL:     "https://cli.github.com/",
	},
	{
		ID:          "jq",
		Kind:        ItemKindTool,
		Group:       ToolGroupExtra,
		PluginID:    "toolchain-jq",
		Name:        "jq",
		Description: "轻量级命令行 JSON 处理器，大量 Skill 与自动化脚本常用依赖",
		Binaries:    []string{"jq"},
		VersionArgs: []string{"--version"},
		DocsURL:     "https://jqlang.github.io/jq/",
	},
	{
		ID:          "fd",
		Kind:        ItemKindTool,
		Group:       ToolGroupExtra,
		PluginID:    "toolchain-fd",
		Name:        "fd",
		Description: "高速文件查找工具，自动遵守 .gitignore 规则",
		Binaries:    []string{"fd", "fdfind"},
		VersionArgs: []string{"--version"},
		DocsURL:     "https://github.com/sharkdp/fd",
	},
	{
		ID:           "pnpm",
		Kind:         ItemKindTool,
		Group:        ToolGroupExtra,
		PluginID:     "toolchain-pnpm",
		Name:         "pnpm",
		Description:  "高性能磁盘共享 Node.js 包管理器（前端工程常用）",
		Binaries:     []string{"pnpm"},
		VersionArgs:  []string{"--version"},
		Dependencies: []string{"node"},
		DocsURL:      "https://pnpm.io/",
	},
	{
		ID:           "python",
		Kind:         ItemKindTool,
		Group:        ToolGroupExtra,
		PluginID:     "toolchain-python",
		Name:         "Python (uv 托管)",
		Description:  "通过 uv 一键安装并托管 Python 3.12 运行时，免去系统 Python 版本冲突",
		Binaries:     []string{"python3", "python"},
		VersionArgs:  []string{"--version"},
		Dependencies: []string{"uv"},
		DocsURL:      "https://docs.astral.sh/uv/guides/install-python/",
	},
}

func Catalog() []ToolSpec {
	engines := RegisteredAgentEngines()
	out := make([]ToolSpec, 0, len(toolCatalogSpecs)+len(engines))
	out = append(out, toolCatalogSpecs...)
	for _, engine := range engines {
		out = append(out, ToolSpec{
			ID:           engine.Kind,
			Kind:         ItemKindAgent,
			PluginID:     engine.PluginID,
			Name:         engine.Name,
			Description:  engine.Description,
			Binaries:     append([]string(nil), engine.Binaries...),
			VersionArgs:  append([]string(nil), engine.VersionArgs...),
			Dependencies: append([]string(nil), engine.Dependencies...),
			AuthCommand:  engine.AuthCommand,
			AuthHint:     engine.AuthHint,
			DocsURL:      engine.DocsURL,
		})
	}
	return out
}

func LookupSpec(id string) (ToolSpec, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, spec := range Catalog() {
		if spec.ID == id {
			return spec, true
		}
	}
	return ToolSpec{}, false
}

// ResolveInstallOrder expands requested IDs with missing dependencies in
// topological order without duplicates.
func ResolveInstallOrder(ids []string, includeDeps bool, installed map[string]bool) ([]ToolSpec, error) {
	seen := map[string]bool{}
	visiting := map[string]bool{}
	ordered := make([]ToolSpec, 0, len(ids))

	var visit func(id string, required bool) error
	visit = func(id string, required bool) error {
		id = strings.ToLower(strings.TrimSpace(id))
		spec, ok := LookupSpec(id)
		if !ok {
			return fmt.Errorf("未知工具或 Agent：%s", id)
		}
		if seen[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("工具依赖存在循环：%s", id)
		}
		visiting[id] = true
		if includeDeps {
			for _, dep := range spec.Dependencies {
				if installed != nil && installed[dep] {
					continue
				}
				if err := visit(dep, false); err != nil {
					return err
				}
			}
		}
		visiting[id] = false
		if !required && installed != nil && installed[id] {
			return nil
		}
		seen[id] = true
		ordered = append(ordered, spec)
		return nil
	}

	for _, id := range ids {
		if err := visit(id, true); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

// RecipesFor builds recommended installation recipes for one tool or Agent on
// the given target OS ("windows" | "linux" | "darwin").
func RecipesFor(toolID, targetOS string) []InstallRecipe {
	id := strings.ToLower(strings.TrimSpace(toolID))
	if engine, ok := LookupAgentEngine(id); ok && engine.RecipesBuilder != nil {
		return engine.RecipesBuilder(targetOS)
	}

	osLower := strings.ToLower(strings.TrimSpace(targetOS))
	isWin := osLower == "windows"
	isMac := osLower == "darwin" || osLower == "macos"

	switch id {
	case "uv":
		if isWin {
			return []InstallRecipe{
				{
					ID:      RecipeIDUVPortable,
					Label:   "免 winget 便携版 uv（推荐 · 自动下载至星序托管目录）",
					Mode:    InstallModeBackground,
					Shell:   "builtin",
					Command: "builtin:portable-uv",
					Note:    "无需管理员权限与 winget，优先使用国内镜像下载并自动回退官方源，内置 uv 与 uvx，解压后立即生效",
				},
				{
					ID:      "uv-ps-official",
					Label:   "官方 PowerShell 脚本安装",
					Mode:    InstallModeBackground,
					Shell:   "powershell",
					Command: `powershell -ExecutionPolicy ByPass -c "irm https://astral.sh/uv/install.ps1 | iex"`,
					Note:    "自动安装 uv 与 uvx 至用户目录，无需管理员权限",
				},
				{
					ID:      "uv-winget",
					Label:   "winget 安装",
					Mode:    InstallModeInteractive,
					Shell:   "powershell",
					Command: `winget install --id astral-sh.uv -e --accept-source-agreements --accept-package-agreements`,
				},
			}
		}
		return []InstallRecipe{
			{
				ID:      "uv-sh-official",
				Label:   "官方脚本安装（免 sudo）",
				Mode:    InstallModeBackground,
				Shell:   "sh",
				Command: `curl -LsSf https://astral.sh/uv/install.sh | sh`,
				Note:    "安装至 ~/.local/bin，自动包含 uv 与 uvx",
			},
			{
				ID:      RecipeIDUVPortable,
				Label:   "便携版 uv（自动下载至星序托管目录，国内镜像加速）",
				Mode:    InstallModeBackground,
				Shell:   "builtin",
				Command: "builtin:portable-uv",
				Note:    "仅限本机环境使用，优先国内镜像并自动回退官方源，解压后立即生效",
			},
		}

	case "node":
		if isWin {
			return []InstallRecipe{
				{
					ID:      RecipeIDNodePortable,
					Label:   "免 winget 便携版 Node.js LTS（推荐 · 自动下载至星序托管目录）",
					Mode:    InstallModeBackground,
					Shell:   "builtin",
					Command: "builtin:portable-node",
					Note:    "无需管理员权限与 winget，优先使用国内镜像（npmmirror/TUNA）下载并自动回退官方源，解压后立即可用无需重启",
				},
				{
					ID:      "node-winget",
					Label:   "winget 安装 Node.js LTS",
					Mode:    InstallModeInteractive,
					Shell:   "powershell",
					Command: `winget install -e --id OpenJS.NodeJS.LTS --accept-source-agreements --accept-package-agreements`,
					Note:    "Windows 安装器可能弹出权限确认，建议在交互终端中查看进度",
				},
			}
		}
		if isMac {
			return []InstallRecipe{
				{
					ID:      "node-brew",
					Label:   "Homebrew 安装 Node.js",
					Mode:    InstallModeBackground,
					Shell:   "sh",
					Command: `brew install node`,
				},
				{
					ID:      RecipeIDNodePortable,
					Label:   "便携版 Node.js LTS（自动下载至星序托管目录，国内镜像加速）",
					Mode:    InstallModeBackground,
					Shell:   "builtin",
					Command: "builtin:portable-node",
					Note:    "仅限本机环境使用，免 Homebrew，优先国内镜像并自动回退官方源",
				},
			}
		}
		return []InstallRecipe{
			{
				ID:      "node-fnm-lts",
				Label:   "用户态安装 Node.js LTS（通过 fnm，免 sudo）",
				Mode:    InstallModeBackground,
				Shell:   "sh",
				Command: `curl -fsSL https://fnm.vercel.app/install | bash -s -- --skip-shell && export PATH="$HOME/.local/share/fnm:$PATH" && eval "$(fnm env)" && fnm install --lts && fnm default lts-latest`,
				Note:    "安装到用户目录无需 root 权限，同时自动就绪 node / npm / npx",
			},
			{
				ID:      RecipeIDNodePortable,
				Label:   "便携版 Node.js LTS（自动下载至星序托管目录，国内镜像加速）",
				Mode:    InstallModeBackground,
				Shell:   "builtin",
				Command: "builtin:portable-node",
				Note:    "仅限本机环境使用，优先国内镜像并自动回退官方源",
			},
			{
				ID:           "node-system-pkg",
				Label:        "系统包管理器安装（需 sudo）",
				Mode:         InstallModeInteractive,
				Shell:        "sh",
				RequiresSudo: true,
				Command:      `if command -v apt-get >/dev/null 2>&1; then sudo apt-get update && sudo apt-get install -y nodejs npm; elif command -v dnf >/dev/null 2>&1; then sudo dnf install -y nodejs npm; else echo "请使用当前发行版包管理器安装 nodejs 与 npm"; fi`,
			},
		}

	case "git":
		if isWin {
			return []InstallRecipe{
				{
					ID:      "git-winget",
					Label:   "winget 安装 Git",
					Mode:    InstallModeBackground,
					Shell:   "powershell",
					Command: `winget install -e --id Git.Git --accept-source-agreements --accept-package-agreements`,
					Note:    "如遇权限弹窗可切换至交互终端执行",
				},
			}
		}
		if isMac {
			return []InstallRecipe{
				{
					ID:      "git-brew",
					Label:   "Homebrew 安装 Git",
					Mode:    InstallModeBackground,
					Shell:   "sh",
					Command: `brew install git`,
				},
			}
		}
		return []InstallRecipe{
			{
				ID:           "git-system-pkg",
				Label:        "系统包管理器安装 Git（需 sudo）",
				Mode:         InstallModeInteractive,
				Shell:        "sh",
				RequiresSudo: true,
				Command:      `if command -v apt-get >/dev/null 2>&1; then sudo apt-get update && sudo apt-get install -y git; elif command -v dnf >/dev/null 2>&1; then sudo dnf install -y git; elif command -v pacman >/dev/null 2>&1; then sudo pacman -Sy --noconfirm git; else echo "请手动安装 git"; fi`,
				Note:         "需要输入 sudo 密码，默认使用交互终端运行",
			},
		}

	case "rg":
		if isWin {
			return []InstallRecipe{
				{
					ID:      "rg-winget",
					Label:   "winget 安装 ripgrep",
					Mode:    InstallModeBackground,
					Shell:   "powershell",
					Command: `winget install -e --id BurntSushi.ripgrep.MSVC --accept-source-agreements --accept-package-agreements`,
				},
			}
		}
		if isMac {
			return []InstallRecipe{
				{
					ID:      "rg-brew",
					Label:   "Homebrew 安装 ripgrep",
					Mode:    InstallModeBackground,
					Shell:   "sh",
					Command: `brew install ripgrep`,
				},
			}
		}
		return []InstallRecipe{
			{
				ID:      "rg-github-user",
				Label:   "用户态安装 ripgrep（~/.local/bin，免 sudo）",
				Mode:    InstallModeBackground,
				Shell:   "sh",
				Command: `set -e; arch="$(uname -m)"; case "$arch" in x86_64) target="x86_64-unknown-linux-musl" ;; aarch64|arm64) target="aarch64-unknown-linux-gnu" ;; *) echo "Unsupported arch: $arch" >&2; exit 1 ;; esac; ver="$(curl -fsSL https://api.github.com/repos/BurntSushi/ripgrep/releases/latest | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"; tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT; curl -fsSL "https://github.com/BurntSushi/ripgrep/releases/download/${ver}/ripgrep-${ver}-${target}.tar.gz" | tar -xz -C "$tmp"; mkdir -p "$HOME/.local/bin"; install -m 0755 "$tmp/ripgrep-${ver}-${target}/rg" "$HOME/.local/bin/rg"; "$HOME/.local/bin/rg" --version`,
				Note:    "直接从官方 GitHub Release 解压至 ~/.local/bin/rg，无需 root",
			},
			{
				ID:           "rg-system-pkg",
				Label:        "系统包管理器安装 ripgrep（需 sudo）",
				Mode:         InstallModeInteractive,
				Shell:        "sh",
				RequiresSudo: true,
				Command:      `if command -v apt-get >/dev/null 2>&1; then sudo apt-get update && sudo apt-get install -y ripgrep; elif command -v dnf >/dev/null 2>&1; then sudo dnf install -y ripgrep; elif command -v pacman >/dev/null 2>&1; then sudo pacman -Sy --noconfirm ripgrep; else echo "请手动安装 ripgrep"; fi`,
			},
		}

	case "gh":
		if isWin {
			return []InstallRecipe{
				{
					ID:      "gh-winget",
					Label:   "winget 安装 GitHub CLI",
					Mode:    InstallModeBackground,
					Shell:   "powershell",
					Command: `winget install -e --id GitHub.cli --accept-source-agreements --accept-package-agreements`,
				},
			}
		}
		if isMac {
			return []InstallRecipe{
				{
					ID:      "gh-brew",
					Label:   "Homebrew 安装 GitHub CLI",
					Mode:    InstallModeBackground,
					Shell:   "sh",
					Command: `brew install gh`,
				},
			}
		}
		return []InstallRecipe{
			{
				ID:      "gh-github-user",
				Label:   "用户态安装 GitHub CLI（~/.local/bin，免 sudo）",
				Mode:    InstallModeBackground,
				Shell:   "sh",
				Command: `set -e; arch="$(uname -m)"; case "$arch" in x86_64) a="amd64" ;; aarch64|arm64) a="arm64" ;; *) echo "Unsupported arch: $arch" >&2; exit 1 ;; esac; tag="$(curl -fsSL https://api.github.com/repos/cli/cli/releases/latest | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"; ver="${tag#v}"; tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT; curl -fsSL "https://github.com/cli/cli/releases/download/${tag}/gh_${ver}_linux_${a}.tar.gz" | tar -xz -C "$tmp"; mkdir -p "$HOME/.local/bin"; install -m 0755 "$tmp/gh_${ver}_linux_${a}/bin/gh" "$HOME/.local/bin/gh"; "$HOME/.local/bin/gh" --version`,
				Note:    "直接从官方 Release 安装到 ~/.local/bin/gh，无需 sudo",
			},
		}

	case "jq":
		if isWin {
			return []InstallRecipe{
				{
					ID:      "jq-winget",
					Label:   "winget 安装 jq",
					Mode:    InstallModeBackground,
					Shell:   "powershell",
					Command: `winget install -e --id jqlang.jq --accept-source-agreements --accept-package-agreements`,
				},
			}
		}
		if isMac {
			return []InstallRecipe{
				{
					ID:      "jq-brew",
					Label:   "Homebrew 安装 jq",
					Mode:    InstallModeBackground,
					Shell:   "sh",
					Command: `brew install jq`,
				},
			}
		}
		return []InstallRecipe{
			{
				ID:      "jq-github-user",
				Label:   "用户态安装 jq（~/.local/bin，免 sudo）",
				Mode:    InstallModeBackground,
				Shell:   "sh",
				Command: `set -e; arch="$(uname -m)"; case "$arch" in x86_64) bin="jq-linux-amd64" ;; aarch64|arm64) bin="jq-linux-arm64" ;; *) echo "Unsupported arch: $arch" >&2; exit 1 ;; esac; mkdir -p "$HOME/.local/bin"; curl -fsSL "https://github.com/jqlang/jq/releases/latest/download/${bin}" -o "$HOME/.local/bin/jq"; chmod +x "$HOME/.local/bin/jq"; "$HOME/.local/bin/jq" --version`,
				Note:    "下载官方单文件二进制至 ~/.local/bin/jq，免 root",
			},
		}

	case "fd":
		if isWin {
			return []InstallRecipe{
				{
					ID:      "fd-winget",
					Label:   "winget 安装 fd",
					Mode:    InstallModeBackground,
					Shell:   "powershell",
					Command: `winget install -e --id sharkdp.fd --accept-source-agreements --accept-package-agreements`,
				},
			}
		}
		if isMac {
			return []InstallRecipe{
				{
					ID:      "fd-brew",
					Label:   "Homebrew 安装 fd",
					Mode:    InstallModeBackground,
					Shell:   "sh",
					Command: `brew install fd`,
				},
			}
		}
		return []InstallRecipe{
			{
				ID:      "fd-github-user",
				Label:   "用户态安装 fd（~/.local/bin，免 sudo）",
				Mode:    InstallModeBackground,
				Shell:   "sh",
				Command: `set -e; arch="$(uname -m)"; case "$arch" in x86_64) target="x86_64-unknown-linux-musl" ;; aarch64|arm64) target="aarch64-unknown-linux-gnu" ;; *) echo "Unsupported arch: $arch" >&2; exit 1 ;; esac; ver="$(curl -fsSL https://api.github.com/repos/sharkdp/fd/releases/latest | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"; tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT; curl -fsSL "https://github.com/sharkdp/fd/releases/download/${ver}/fd-${ver}-${target}.tar.gz" | tar -xz -C "$tmp"; mkdir -p "$HOME/.local/bin"; install -m 0755 "$tmp/fd-${ver}-${target}/fd" "$HOME/.local/bin/fd"; "$HOME/.local/bin/fd" --version`,
				Note:    "直接从官方 Release 安装到 ~/.local/bin/fd，免 sudo",
			},
		}

	case "pnpm":
		if isWin {
			return []InstallRecipe{
				{
					ID:      "pnpm-npm-win",
					Label:   "通过 npm 全局安装 pnpm",
					Mode:    InstallModeBackground,
					Shell:   "powershell",
					Command: `npm install -g pnpm@latest`,
				},
			}
		}
		return []InstallRecipe{
			{
				ID:      "pnpm-npm-unix",
				Label:   "通过 npm 全局安装 pnpm",
				Mode:    InstallModeBackground,
				Shell:   "sh",
				Command: `export PATH="$HOME/.vite-plus/bin:$HOME/.local/share/fnm:$HOME/.local/bin:$PATH"; if command -v fnm >/dev/null 2>&1; then eval "$(fnm env)"; fi; npm install -g pnpm@latest`,
			},
		}

	case "python":
		if isWin {
			return []InstallRecipe{
				{
					ID:      "python-uv-win",
					Label:   "通过 uv 安装托管 Python 3.12",
					Mode:    InstallModeBackground,
					Shell:   "powershell",
					Command: `uv python install 3.12 --default`,
					Note:    "由 uv 独立托管，不污染系统 Python 环境",
				},
			}
		}
		return []InstallRecipe{
			{
				ID:      "python-uv-unix",
				Label:   "通过 uv 安装托管 Python 3.12",
				Mode:    InstallModeBackground,
				Shell:   "sh",
				Command: `export PATH="$HOME/.local/bin:$PATH"; uv python install 3.12 --default`,
				Note:    "由 uv 独立托管并软链至 ~/.local/bin，免 sudo",
			},
		}
	}
	return nil
}
