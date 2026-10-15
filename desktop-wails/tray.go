package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

type kernelMenuState struct {
	Label      string
	Detail     string
	CanStart   bool
	CanStop    bool
	CanRestart bool
}

func describeKernel(name string, state map[string]any) kernelMenuState {
	result := kernelMenuState{Label: name + " · 状态未知", Detail: "无法读取服务状态"}
	if state == nil {
		return result
	}
	value, _ := state["state"].(string)
	label := map[string]string{"running": "运行中", "stopped": "已停止", "starting": "启动中", "stopping": "停止中", "conflict": "状态冲突", "error": "异常", "unknown": "状态未知"}[value]
	if label == "" {
		label = "状态未知"
	}
	if state["ok"] == false {
		label = "状态未知"
		value = "unknown"
	}
	result.Label = name + " · " + label
	result.Detail = "状态：" + label
	if value == "running" {
		result.CanStop = true
		result.CanRestart = true
		if port, ok := state["port"]; ok {
			result.Detail += fmt.Sprintf(" · 端口 %v", port)
		}
	} else if value == "stopped" {
		result.CanStart = true
	}
	return result
}

// A single polling worker reads CLI authority off the UI thread. All menu
// mutations are dispatched to the main thread; shutdown cancels pending reads.
func (s *ShellService) installTray(tray *application.SystemTray, show func()) {
	menu := s.app.NewMenu()
	menu.Add("打开星序").OnClick(func(*application.Context) { show() })
	menu.Add("复制访问令牌").OnClick(func(*application.Context) {
		go func() {
			if _, err := s.CopyAccessToken(); err != nil {
				s.showError("复制访问令牌失败", err)
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
	names := map[string]string{"app": "大内核", "daemon": "小内核"}
	run := func(target, action string) {
		if !working.CompareAndSwap(false, true) {
			return
		}
		go func() {
			defer working.Store(false)
			result, err := s.Run(target, action)
			if err != nil {
				s.showError(names[target]+"操作失败", err)
			} else if result["ok"] != true && result["state"] != "cancelled" {
				s.showError(names[target]+"操作失败", fmt.Errorf("服务操作未得到成功确认"))
			} else if action == "update" && result["ok"] == true {
				message, _ := result["message"].(string)
				s.app.Dialog.Info().SetTitle("小内核更新").SetMessage(message).AttachToWindow(s.window).Show()
			}
			requestRefresh()
		}()
	}
	for _, target := range []string{"app", "daemon"} {
		name := names[target]
		submenu := menu.AddSubmenu(name + " · 查询中")
		parent := menu.FindByLabel(name + " · 查询中")
		detail := submenu.Add("正在查询服务状态…").SetEnabled(false)
		submenu.AddSeparator()
		start := submenu.Add("启动" + name).SetEnabled(false).OnClick(func(*application.Context) { run(target, "start") })
		restart := submenu.Add("重启" + name).SetEnabled(false).OnClick(func(*application.Context) { run(target, "restart") })
		stop := submenu.Add("停止" + name).SetEnabled(false).OnClick(func(*application.Context) { run(target, "stop") })
		if target == "daemon" {
			submenu.AddSeparator()
			submenu.Add("更新小内核").OnClick(func(*application.Context) { run(target, "update") })
		}
		items[target] = controls{parent, detail, start, restart, stop}
	}
	menu.Add("刷新内核状态").OnClick(func(*application.Context) { requestRefresh() })
	menu.AddSeparator()
	menu.Add("退出客户端（小内核继续运行）").OnClick(func(*application.Context) { go s.exitClient() })
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
				for _, target := range []string{"app", "daemon"} {
					value, _ := status[target].(map[string]any)
					states[target] = describeKernel(names[target], value)
				}
				application.InvokeAsync(func() {
					for _, target := range []string{"app", "daemon"} {
						c, state := items[target], states[target]
						c.parent.SetLabel(state.Label)
						c.detail.SetLabel(state.Detail)
						c.start.SetEnabled(state.CanStart)
						c.stop.SetEnabled(state.CanStop)
						c.restart.SetEnabled(state.CanRestart)
					}
					tray.SetTooltip("星序 · " + states["app"].Label + " · " + states["daemon"].Label)
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
