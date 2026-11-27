package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	trayAppServiceLabel    = "主服务"
	trayDaemonServiceLabel = "小内核"
	trayDaemonUpdateLabel  = "更新小内核"
)

type kernelMenuState struct {
	Label      string
	Detail     string
	CanStart   bool
	CanStop    bool
	CanRestart bool
}

func normalizedServiceState(state map[string]any) string {
	if state == nil || state["ok"] == false {
		return "unknown"
	}
	value, _ := state["state"].(string)
	switch value {
	case "running", "stopped", "starting", "stopping", "conflict", "error", "unknown":
		return value
	default:
		return "unknown"
	}
}

func describeKernel(name string, state map[string]any) kernelMenuState {
	result := kernelMenuState{Label: name + " · 状态未知", Detail: "正在等待服务响应，可稍后刷新"}
	if state == nil {
		return result
	}
	value := normalizedServiceState(state)
	label := map[string]string{
		"running":  "运行中",
		"stopped":  "已停止",
		"starting": "启动中",
		"stopping": "停止中",
		"conflict": "状态冲突",
		"error":    "异常",
		"unknown":  "状态未知",
	}[value]
	if label == "" {
		label = "状态未知"
	}
	result.Label = name + " · " + label
	switch value {
	case "running":
		result.CanStop = true
		result.CanRestart = true
		result.Detail = "当前正常工作中"
		if port, ok := state["port"]; ok {
			result.Detail = fmt.Sprintf("当前正常工作中（本地端口 %v）", port)
		}
	case "stopped":
		result.CanStart = true
		result.Detail = "当前未运行，可随时重新启动"
	case "starting":
		result.Detail = "正在启动，请稍候…"
	case "stopping":
		result.Detail = "正在安全停止…"
	case "conflict":
		result.Detail = "检测到端口或旧实例占用，建议打开设置检查"
	case "error":
		result.Detail = "服务暂时未就绪，可尝试重启或打开设置排查"
	default:
		result.Detail = "暂未获取到服务状态，可点击刷新重试"
	}
	return result
}

func describeTrayTooltip(appState, daemonState map[string]any) string {
	appVal := normalizedServiceState(appState)
	daemonVal := normalizedServiceState(daemonState)
	switch {
	case appVal == "running" && daemonVal == "running":
		return "星序 · 运行正常"
	case appVal == "starting" || daemonVal == "starting":
		return "星序 · 正在启动服务…"
	case appVal == "stopping" || daemonVal == "stopping":
		return "星序 · 正在停止服务…"
	case appVal == "running" && daemonVal != "running":
		return "星序 · 界面已就绪（后台引擎连接中）"
	case appVal == "stopped" && daemonVal == "running":
		return "星序 · 后台任务持续运行中"
	case appVal == "stopped" && daemonVal == "stopped":
		return "星序 · 服务已暂停"
	case appVal == "conflict" || daemonVal == "conflict" || appVal == "error" || daemonVal == "error":
		return "星序 · 服务需要检查，点击打开查看"
	default:
		return "星序 · 正在同步服务状态"
	}
}

// A single polling worker reads CLI authority off the UI thread. All menu
// mutations are dispatched to the main thread; shutdown cancels pending reads.
func (s *ShellService) installTray(tray *application.SystemTray, show func()) {
	menu := s.app.NewMenu()
	menu.Add("打开星序").OnClick(func(*application.Context) { show() })
	menu.Add("设置").OnClick(func(*application.Context) { _ = s.OpenSettings("") })
	menu.Add("复制网页登录口令").OnClick(func(*application.Context) {
		go func() {
			if _, err := s.CopyAccessToken(); err != nil {
				s.showError("复制登录口令未成功", err)
			}
		}()
	})
	menu.AddSeparator()
	refresh := make(chan struct{}, 1)
	requestRefresh := func() {
		select {
		case refresh <- struct{}{}:
		default:
		}
	}
	var working atomic.Bool
	type controls struct{ parent, detail, start, restart, stop *application.MenuItem }
	items := map[string]controls{}
	names := map[string]string{"app": trayAppServiceLabel, "daemon": trayDaemonServiceLabel}
	run := func(target, action string) {
		if !working.CompareAndSwap(false, true) {
			return
		}
		go func() {
			defer working.Store(false)
			result, err := s.Run(target, action)
			if err != nil {
				s.showError(names[target]+"操作未完成", err)
			} else if result["ok"] != true && result["state"] != "cancelled" {
				s.showError(names[target]+"操作未完成", fmt.Errorf("操作尚未生效，请稍后刷新状态或重试"))
			} else if action == "update" && result["ok"] == true {
				message, _ := result["message"].(string)
				s.app.Dialog.Info().SetTitle("小内核更新").SetMessage(message).AttachToWindow(s.window).Show()
			}
			requestRefresh()
		}()
	}
	for _, target := range []string{"app", "daemon"} {
		name := names[target]
		submenu := menu.AddSubmenu(name + " · 检查中")
		parent := menu.FindByLabel(name + " · 检查中")
		detail := submenu.Add("正在检查状态…").SetEnabled(false)
		submenu.AddSeparator()
		start := submenu.Add("启动" + name).SetEnabled(false).OnClick(func(*application.Context) { run(target, "start") })
		restart := submenu.Add("重启" + name).SetEnabled(false).OnClick(func(*application.Context) { run(target, "restart") })
		stop := submenu.Add("停止" + name).SetEnabled(false).OnClick(func(*application.Context) { run(target, "stop") })
		if target == "daemon" {
			submenu.AddSeparator()
			submenu.Add(trayDaemonUpdateLabel).OnClick(func(*application.Context) { run(target, "update") })
		}
		items[target] = controls{parent, detail, start, restart, stop}
	}
	menu.Add("刷新服务状态").OnClick(func(*application.Context) { requestRefresh() })
	menu.AddSeparator()
	menu.Add("退出桌面窗口（后台任务继续运行）").OnClick(func(*application.Context) { go s.exitClient() })
	tray.SetMenu(menu)
	tray.OnClick(show)
	tray.OnDoubleClick(show)
	tray.OnMouseEnter(requestRefresh)
	ctx, cancel := context.WithCancel(context.Background())
	s.app.OnShutdown(cancel)
	s.refreshTray = requestRefresh
	s.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				var status map[string]any
				if s.ctl != nil {
					readCtx, stop := context.WithTimeout(ctx, 8*time.Second)
					status, _ = s.ctl.Status(readCtx)
					stop()
				}
				if ctx.Err() != nil {
					return
				}
				states := map[string]kernelMenuState{}
				var appRaw, daemonRaw map[string]any
				for _, target := range []string{"app", "daemon"} {
					value, _ := status[target].(map[string]any)
					if target == "app" {
						appRaw = value
					} else {
						daemonRaw = value
					}
					states[target] = describeKernel(names[target], value)
				}
				tooltip := describeTrayTooltip(appRaw, daemonRaw)
				application.InvokeAsync(func() {
					for _, target := range []string{"app", "daemon"} {
						c, state := items[target], states[target]
						c.parent.SetLabel(state.Label)
						c.detail.SetLabel(state.Detail)
						c.start.SetEnabled(state.CanStart)
						c.stop.SetEnabled(state.CanStop)
						c.restart.SetEnabled(state.CanRestart)
					}
					tray.SetTooltip(tooltip)
				})
				select {
				case <-ctx.Done():
					return
				case <-refresh:
				case <-ticker.C:
				}
			}
		}()
	})
}
