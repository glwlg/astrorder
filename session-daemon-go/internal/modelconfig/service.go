package modelconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func New(cfg Config) (*Service, error) {
	if len(cfg.AllowedRoots) == 0 {
		return nil, errors.New("allowed roots cannot be empty")
	}

	roots := make([]string, 0, len(cfg.AllowedRoots))
	for _, root := range cfg.AllowedRoots {
		clean := filepath.Clean(root)
		resolved, err := filepath.EvalSymlinks(clean)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				resolved = clean
			} else {
				return nil, fmt.Errorf("eval symlink on allowed root %q: %w", root, err)
			}
		}
		roots = append(roots, resolved)
	}

	return &Service{
		config:         cfg,
		allowedRoots:   roots,
		pendingReloads: make(map[string]bool),
	}, nil
}

func (s *Service) Execute(ctx context.Context, action string, request map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil {
		request = map[string]any{}
	}

	switch action {
	case "model_config.plan":
		return s.plan(ctx, request)
	case "model_config.apply":
		return s.apply(ctx, request)
	case "model_config.reload":
		return s.reload(ctx, request)
	default:
		return nil, newProtocolErrorf("unknown or unsupported action %q", action)
	}
}

func (s *Service) resolveTargetRoot(request map[string]any) (string, error) {
	target, _ := request["target"].(map[string]any)
	var rawRoot string
	if target != nil {
		if r, ok := target["root"].(string); ok && r != "" {
			rawRoot = r
		} else if w, ok := target["workspace"].(string); ok && w != "" {
			rawRoot = w
		} else if h, ok := target["home"].(string); ok && h != "" {
			rawRoot = h
		}
	}

	if rawRoot == "" {
		if len(s.allowedRoots) == 0 {
			return "", newProtocolErrorf("no allowed root configured")
		}
		return s.allowedRoots[0], nil
	}

	clean := filepath.Clean(rawRoot)
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			resolved = clean
		} else {
			return "", newProtocolErrorf("eval symlink on target root: %v", err)
		}
	}

	allowed := false
	for _, root := range s.allowedRoots {
		if isWithinRoot(root, resolved) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", newProtocolErrorf("path escape: target root %q outside allowed roots", rawRoot)
	}

	return resolved, nil
}
