//go:build !windows

package main

import (
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func placeStatisticsWindow(_, _ *application.WebviewWindow, _ StatisticsBounds) error {
	return errors.New("当前平台尚不支持原生统计视图，请使用外部打开")
}
