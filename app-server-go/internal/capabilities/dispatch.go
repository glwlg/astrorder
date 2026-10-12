package capabilities

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"astrorder.dev/app-server/internal/envtarget"
)

type DispatchTarget struct {
	TargetID string `json:"target_id"`
	Agent    string `json:"agent"`
}

type DispatchReport struct {
	TargetID      string `json:"target_id"`
	TargetName    string `json:"target_name,omitempty"`
	Agent         string `json:"agent"`
	OK            bool   `json:"ok"`
	InstalledPath string `json:"installed_path,omitempty"`
	Message       string `json:"message"`
}

type DispatchResult struct {
	OK           bool             `json:"ok"`
	Type         string           `json:"capability_type"`
	ItemID       string           `json:"item_id"`
	TotalTargets int              `json:"total_targets"`
	SuccessCount int              `json:"success_count"`
	Reports      []DispatchReport `json:"reports"`
}

// Dispatch copies one skill, MCP server or plugin from a source machine to
// every (machine, agent) pair in the matrix. Each destination is verified by
// reading it back.
func (s *Service) Dispatch(ctx context.Context, kind, sourceTarget, sourceAgent, item string, matrix []DispatchTarget) (*DispatchResult, error) {
	item = strings.TrimSpace(item)
	if item == "" || len(matrix) == 0 {
		return nil, envtarget.Errorf(422, "请选择要分发的项目和至少一个目标")
	}
	if len(matrix) > 64 {
		return nil, envtarget.Errorf(422, "一次最多分发到 64 个目标")
	}
	source, err := s.open(ctx, sourceTarget)
	if err != nil {
		return nil, err
	}
	var deliver func(ctx context.Context, dest envtarget.Env, agent string) (string, string, error)
	switch kind {
	case "skill":
		deliver, err = s.skillDelivery(ctx, source, sourceAgent, item)
	case "mcp":
		deliver, err = s.mcpDelivery(ctx, source, sourceAgent, item)
	case "plugin":
		deliver, err = s.pluginDelivery(ctx, source, sourceAgent, item)
	default:
		err = envtarget.Errorf(422, "不支持的能力类型：%s", kind)
	}
	if err != nil {
		return nil, err
	}
	result := &DispatchResult{Type: kind, ItemID: item, TotalTargets: len(matrix), Reports: []DispatchReport{}}
	for _, entry := range matrix {
		report := DispatchReport{TargetID: entry.TargetID, Agent: entry.Agent}
		dest, openErr := s.open(ctx, entry.TargetID)
		switch {
		case !validAgent(entry.Agent):
			report.Message = "不支持的 Agent：" + entry.Agent
		case openErr != nil:
			_, report.Message = statusOf(openErr)
		default:
			report.TargetName = dest.Target().Name
			where, message, deliverErr := deliver(ctx, dest, entry.Agent)
			report.InstalledPath, report.Message = where, message
			if deliverErr != nil {
				_, report.Message = statusOf(deliverErr)
			} else {
				report.OK = true
			}
		}
		if report.OK {
			result.SuccessCount++
		}
		result.Reports = append(result.Reports, report)
	}
	result.OK = result.SuccessCount > 0
	return result, nil
}

// ---- skills -----------------------------------------------------------

func (s *Service) skillDelivery(ctx context.Context, source envtarget.Env, sourceAgent, name string) (func(context.Context, envtarget.Env, string) (string, string, error), error) {
	if !skillNameRE.MatchString(name) {
		return nil, envtarget.Errorf(422, "技能名称无效")
	}
	sn, err := s.snapshot(ctx, source)
	if err != nil {
		return nil, err
	}
	locations, err := sn.discoverSkillsDetailed(ctx)
	if err != nil {
		return nil, err
	}
	var chosen *skillLocation
	for i, location := range locations[name] {
		if chosen == nil || sourceAgent != "" && sourceAgent != "all" && strings.HasPrefix(location.root, sn.paths.Home) && strings.Contains(location.root, "/."+sourceAgent+"/") {
			chosen = &locations[name][i]
		}
	}
	if chosen == nil {
		return nil, envtarget.Errorf(404, "源端没有找到技能 %s", name)
	}
	dir := path.Dir(chosen.path)
	archive, err := source.PackDir(ctx, dir)
	if err != nil {
		return nil, envtarget.Errorf(502, "打包技能 %s 失败：%v", name, err)
	}
	folder := path.Base(dir)
	return func(ctx context.Context, dest envtarget.Env, agent string) (string, string, error) {
		paths, err := dest.Paths(ctx)
		if err != nil {
			return "", "", err
		}
		root := map[string]string{"codex": paths.Codex, "hermes": paths.Hermes, "grok": paths.Grok}[agent] + "/skills"
		defer s.lock(dest.Target().ID + "|skills-dispatch|" + root)()
		if err := dest.UnpackDir(ctx, root, folder, archive); err != nil {
			return "", "", err
		}
		if _, err := dest.Read(ctx, root+"/"+folder+"/SKILL.md"); err != nil {
			return "", "", envtarget.Errorf(502, "复制后未能在目标上读到 SKILL.md")
		}
		return root + "/" + folder, "技能已复制并核验", nil
	}, nil
}

// ---- MCP --------------------------------------------------------------

func (s *Service) mcpDelivery(ctx context.Context, source envtarget.Env, sourceAgent, name string) (func(context.Context, envtarget.Env, string) (string, string, error), error) {
	if !validAgent(sourceAgent) {
		return nil, envtarget.Errorf(422, "源 Agent 无效：%s", sourceAgent)
	}
	stored, err := s.storedMCP(ctx, source, sourceAgent, name)
	if err != nil {
		return nil, err
	}
	in := mcpInput{Name: stored.Name, Transport: stored.Transport, URL: stored.URL, Command: stored.Command, Args: stored.Args, Enabled: stored.Enabled}
	if len(stored.Env) > 0 {
		in.Env = &stored.Env
	}
	if len(stored.Headers) > 0 {
		in.Headers = &stored.Headers
	}
	return func(ctx context.Context, dest envtarget.Env, agent string) (string, string, error) {
		if _, err := s.saveMCP(ctx, dest, agent, in); err != nil {
			return "", "", err
		}
		paths, _ := dest.Paths(ctx)
		return configPath(paths, agent), "MCP 服务已写入 " + agent + " 配置并核验", nil
	}, nil
}

// ---- plugins ----------------------------------------------------------

var grokPluginListRE = regexp.MustCompile(`:\s+(\S+)\s+\[git:\s*([^\]\s]+)\]`)

func (s *Service) pluginDelivery(ctx context.Context, source envtarget.Env, sourceAgent, name string) (func(context.Context, envtarget.Env, string) (string, string, error), error) {
	if !pluginNameRE.MatchString(name) {
		return nil, envtarget.Errorf(422, "插件名称无效")
	}
	sn, err := s.snapshot(ctx, source)
	if err != nil {
		return nil, err
	}
	plugin, ok := sn.findPlugin(ctx, sourceAgent, name)
	if !ok {
		return nil, envtarget.Errorf(404, "源端 %s 里没有插件 %s", sourceAgent, name)
	}
	var archive []byte
	var gitURL string
	if sourceAgent == "hermes" {
		if archive, err = source.PackDir(ctx, sn.paths.Hermes+"/plugins/"+plugin.Name); err != nil {
			archive = nil
		}
	}
	if sourceAgent == "grok" {
		if listing, listErr := s.agentCLI(ctx, source, "grok", "plugin", "list"); listErr == nil {
			for _, match := range grokPluginListRE.FindAllStringSubmatch(listing, -1) {
				if match[1] == plugin.Name {
					gitURL = match[2]
				}
			}
		}
	}
	return func(ctx context.Context, dest envtarget.Env, agent string) (string, string, error) {
		if agent != sourceAgent {
			return "", "", envtarget.Errorf(422, "插件只能分发给同类 Agent（%s → %s）", sourceAgent, agent)
		}
		defer s.lock(dest.Target().ID + "|plugin-dispatch")()
		destSnap, err := s.snapshot(ctx, dest)
		if err != nil {
			return "", "", err
		}
		if existing, present := destSnap.findPlugin(ctx, agent, plugin.FullName); present && existing.Enabled {
			return "", "目标上该插件已安装并启用", nil
		}
		message := "插件已启用"
		_, installed := destSnap.findPlugin(ctx, agent, plugin.FullName)
		if !installed {
			switch agent {
			case "codex":
				if _, err := s.agentCLI(ctx, dest, "codex", "plugin", "add", plugin.FullName); err != nil {
					return "", "", envtarget.Errorf(502, "目标未能安装 %s（需要目标先添加市场源 %s）：%v", plugin.FullName, firstNonEmpty(plugin.Marketplace, "?"), err)
				}
				message = "插件已通过 codex plugin add 安装"
			case "hermes":
				if archive == nil {
					return "", "", envtarget.Errorf(422, "源端没有可复制的插件目录（内置插件无需分发）")
				}
				paths, _ := dest.Paths(ctx)
				if err := dest.UnpackDir(ctx, paths.Hermes+"/plugins", plugin.Name, archive); err != nil {
					return "", "", err
				}
				message = "插件目录已复制并启用"
			case "grok":
				if gitURL == "" {
					return "", "", envtarget.Errorf(422, "源端查不到该插件的 Git 来源，无法在目标上安装")
				}
				if _, err := s.agentCLI(ctx, dest, "grok", "plugin", "install", "--trust", gitURL); err != nil {
					return "", "", err
				}
				message = "插件已从 " + gitURL + " 安装"
			}
		}
		if err := s.applyPlugin(ctx, dest, agent, "toggle", plugin.FullName, true); err != nil {
			return "", "", err
		}
		return "", fmt.Sprint(message), nil
	}, nil
}
