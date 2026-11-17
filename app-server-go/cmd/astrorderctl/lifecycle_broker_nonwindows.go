//go:build !windows

package main

import (
	"context"
	"errors"
)

func lifecycleControllerInJob() (bool, error) { return false, nil }

func invokeLifecycleBroker(context.Context, string, string, string, string, bool) (int, []byte, error) {
	return 1, nil, errors.New("independent lifecycle broker requires Windows")
}
