package toolchain

import "strings"

// AgentEngineCapabilities describes which Astrorder host capabilities an Agent
// engine supports so UI surfaces and onboarding flows stay data-driven.
type AgentEngineCapabilities struct {
	SupportsSSH          bool `json:"supports_ssh"`
	SupportsRTK          bool `json:"supports_rtk"`
	SupportsSessionCLI   bool `json:"supports_session_cli"`
	SupportsAccountsPool bool `json:"supports_accounts_pool"`
}

// AgentEngineSpec is the single declarative source of truth for one native
// Agent engine in the App Server toolchain & plugin onboarding layer.
// Adding a new Agent engine starts by registering one entry here (see docs/ADDING_AN_AGENT.md).
type AgentEngineSpec struct {
	Kind           string                  `json:"kind"`
	PluginID       string                  `json:"plugin_id"`
	Name           string                  `json:"name"`
	ShortLabel     string                  `json:"short_label"`
	Description    string                  `json:"description"`
	Aliases        []string                `json:"aliases,omitempty"`
	Binaries       []string                `json:"binaries"`
	VersionArgs    []string                `json:"version_args"`
	Dependencies   []string                `json:"dependencies,omitempty"`
	HomeField      string                  `json:"home_field"` // "antigravity" | "codex" | "hermes" | "grok"
	AuthFiles      []string                `json:"auth_files,omitempty"`
	AuthEnvVars    []string                `json:"auth_env_vars,omitempty"`
	AuthCommand    string                  `json:"auth_command,omitempty"`
	AuthHint       string                  `json:"auth_hint,omitempty"`
	DocsURL        string                  `json:"docs_url,omitempty"`
	RelatedPlugins []string                `json:"related_plugins,omitempty"`
	Capabilities   AgentEngineCapabilities `json:"capabilities"`
	RecipesBuilder func(targetOS string) []InstallRecipe `json:"-"`
}

var registeredAgentEngines = []AgentEngineSpec{
	{
		Kind:           "antigravity",
		PluginID:       "agent-antigravity",
		Name:           "Google Antigravity",
		ShortLabel:     "Antigravity",
		Description:    "Google Antigravity 原生 CLI Agent（agy），支持多账号配额池与原生 Swarm 观测",
		Aliases:        []string{"antigravity-ssh", "agy"},
		Binaries:       []string{"agy"},
		VersionArgs:    []string{"--version"},
		HomeField:      "antigravity",
		AuthFiles:      []string{"../oauth_creds.json", "../google_accounts.json", "../settings.json", "settings.json"},
		AuthEnvVars:    []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		AuthCommand:    "agy login",
		AuthHint:       "安装后可在交互终端执行 agy login 或在「账号配额池」导入账号",
		DocsURL:        "https://antigravity.google/",
		RelatedPlugins: []string{"workspace-agent-accounts"},
		Capabilities: AgentEngineCapabilities{
			SupportsSSH:          true,
			SupportsRTK:          false,
			SupportsSessionCLI:   true,
			SupportsAccountsPool: true,
		},
		RecipesBuilder: func(targetOS string) []InstallRecipe {
			if strings.EqualFold(targetOS, "windows") {
				return []InstallRecipe{
					{
						ID:      "agy-interactive-win",
						Label:   "在交互终端检查 / 登录 Antigravity CLI",
						Mode:    InstallModeInteractive,
						Shell:   "powershell",
						Command: `agy --version`,
						Note:    "Antigravity CLI 通常随桌面端或内部渠道安装；如已安装可直接点击「选择已安装路径」定位 agy.exe",
					},
				}
			}
			return []InstallRecipe{
				{
					ID:      "agy-interactive-unix",
					Label:   "在交互终端检查 / 登录 Antigravity CLI",
					Mode:    InstallModeInteractive,
					Shell:   "sh",
					Command: `command -v agy && agy --version || echo "请按官方文档安装 agy 后点击重新检测，或直接选择已安装路径"`,
					Note:    "如已安装到自定义目录，可直接点击「选择已安装路径」定位 agy 可执行文件",
				},
			}
		},
	},
	{
		Kind:         "codex",
		PluginID:     "agent-codex",
		Name:         "OpenAI Codex",
		ShortLabel:   "Codex",
		Description:  "OpenAI Codex 原生命令行编程 Agent（依赖 Node.js / npm）",
		Aliases:      []string{"codex-ssh"},
		Binaries:     []string{"codex"},
		VersionArgs:  []string{"--version"},
		Dependencies: []string{"node"},
		HomeField:    "codex",
		AuthFiles:    []string{"auth.json", "config.toml"},
		AuthEnvVars:  []string{"OPENAI_API_KEY"},
		AuthCommand:  "codex login",
		AuthHint:     "安装后可在交互终端执行 codex login，或在「模型网关」中同步写入配置",
		DocsURL:      "https://github.com/openai/codex",
		Capabilities: AgentEngineCapabilities{
			SupportsSSH:          true,
			SupportsRTK:          true,
			SupportsSessionCLI:   true,
			SupportsAccountsPool: false,
		},
		RecipesBuilder: func(targetOS string) []InstallRecipe {
			if strings.EqualFold(targetOS, "windows") {
				return []InstallRecipe{
					{
						ID:      "codex-npm-win",
						Label:   "npm / vp 全局安装 @openai/codex",
						Mode:    InstallModeBackground,
						Shell:   "powershell",
						Command: `if (Get-Command vp -ErrorAction SilentlyContinue) { vp install -g @openai/codex@latest } else { npm install -g @openai/codex@latest }`,
						Note:    "安装完成后可在交互终端执行 codex login 完成账号登录",
					},
				}
			}
			return []InstallRecipe{
				{
					ID:      "codex-npm-unix",
					Label:   "npm / vp 全局安装 @openai/codex",
					Mode:    InstallModeBackground,
					Shell:   "sh",
					Command: `export PATH="$HOME/.vite-plus/bin:$HOME/.local/share/fnm:$HOME/.local/bin:$PATH"; if command -v fnm >/dev/null 2>&1; then eval "$(fnm env)"; fi; if command -v vp >/dev/null 2>&1; then vp install -g @openai/codex@latest; else npm install -g @openai/codex@latest; fi`,
					Note:    "安装完成后可在交互终端执行 codex login 完成账号登录",
				},
			}
		},
	},
	{
		Kind:         "hermes",
		PluginID:     "agent-hermes",
		Name:         "Hermes Agent",
		ShortLabel:   "Hermes",
		Description:  "NousResearch Hermes 原生 Agent（支持本机与多台 SSH 远端主机协同）",
		Aliases:      []string{"ssh-hermes", "hermes-ssh"},
		Binaries:     []string{"hermes"},
		VersionArgs:  []string{"--version"},
		Dependencies: []string{"uv"},
		HomeField:    "hermes",
		AuthFiles:    []string{"config.yaml", ".env"},
		AuthEnvVars:  []string{"OPENROUTER_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"},
		AuthCommand:  "hermes setup",
		AuthHint:     "安装后可在交互终端执行 hermes setup，或在「模型网关」中同步写入配置",
		DocsURL:      "https://github.com/NousResearch/hermes-agent",
		Capabilities: AgentEngineCapabilities{
			SupportsSSH:          true,
			SupportsRTK:          true,
			SupportsSessionCLI:   true,
			SupportsAccountsPool: false,
		},
		RecipesBuilder: func(targetOS string) []InstallRecipe {
			if strings.EqualFold(targetOS, "windows") {
				return []InstallRecipe{
					{
						ID:      "hermes-uv-win",
						Label:   "通过 uv 安装 Hermes Agent",
						Mode:    InstallModeInteractive,
						Shell:   "powershell",
						Command: `uv tool install --upgrade hermes-agent`,
						Note:    "若你使用 WSL 运行 Hermes，请切换到对应 WSL / SSH 环境安装",
					},
				}
			}
			return []InstallRecipe{
				{
					ID:      "hermes-official-sh",
					Label:   "官方脚本安装 Hermes Agent",
					Mode:    InstallModeBackground,
					Shell:   "sh",
					Command: `export PATH="$HOME/.local/bin:$PATH"; curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash`,
					Note:    "官方安装脚本会自动准备 Hermes 虚拟环境与 CLI",
				},
			}
		},
	},
	{
		Kind:        "grok",
		PluginID:    "agent-grok",
		Name:        "xAI Grok",
		ShortLabel:  "Grok",
		Description: "xAI Grok 原生命令行 Agent",
		Aliases:     []string{"grok-ssh"},
		Binaries:    []string{"grok"},
		VersionArgs: []string{"--version"},
		HomeField:   "grok",
		AuthFiles:   []string{"config.json", "auth.json", "settings.json"},
		AuthEnvVars: []string{"XAI_API_KEY"},
		AuthCommand: "grok login",
		AuthHint:    "安装后可在交互终端执行 grok login 或配置 XAI_API_KEY",
		DocsURL:     "https://x.ai/",
		Capabilities: AgentEngineCapabilities{
			SupportsSSH:          true,
			SupportsRTK:          false,
			SupportsSessionCLI:   true,
			SupportsAccountsPool: false,
		},
		RecipesBuilder: func(targetOS string) []InstallRecipe {
			if strings.EqualFold(targetOS, "windows") {
				return []InstallRecipe{
					{
						ID:      "grok-official-win",
						Label:   "官方脚本安装 Grok CLI",
						Mode:    InstallModeInteractive,
						Shell:   "powershell",
						Command: `powershell -ExecutionPolicy ByPass -c "irm https://x.ai/cli/install.ps1 | iex"`,
						Note:    "也可选择已手动安装的 grok.exe 路径",
					},
				}
			}
			return []InstallRecipe{
				{
					ID:      "grok-official-sh",
					Label:   "官方脚本安装 Grok CLI",
					Mode:    InstallModeBackground,
					Shell:   "sh",
					Command: `curl -fsSL https://x.ai/cli/install.sh | sh`,
					Note:    "默认安装到 ~/.grok/bin 或 ~/.local/bin",
				},
			}
		},
	},
}

// RegisteredAgentEngines returns a copy of all registered Agent engine specs.
func RegisteredAgentEngines() []AgentEngineSpec {
	out := make([]AgentEngineSpec, len(registeredAgentEngines))
	copy(out, registeredAgentEngines)
	return out
}

// LookupAgentEngine finds an Agent engine by canonical kind or alias.
func LookupAgentEngine(kind string) (AgentEngineSpec, bool) {
	needle := strings.ToLower(strings.TrimSpace(kind))
	for _, spec := range registeredAgentEngines {
		if spec.Kind == needle {
			return spec, true
		}
		for _, alias := range spec.Aliases {
			if strings.EqualFold(alias, needle) {
				return spec, true
			}
		}
	}
	return AgentEngineSpec{}, false
}
