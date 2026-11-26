package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	unbuiltSourceRefPattern  = regexp.MustCompile(`(?i)\b(?:src|href)\s*=\s*["'](?:\.?/)?src/[^"']+["']`)
	builtAssetRefPattern     = regexp.MustCompile(`(?i)\b(?:src|href)\s*=\s*["']((?:\.?/)?assets/[^"'?#]+)(?:[?#][^"']*)?["']`)
	moduleEntryBeforeSrc     = regexp.MustCompile(`(?i)<script\b[^>]*\btype\s*=\s*["']module["'][^>]*\bsrc\s*=\s*["'](?:\.?/)?assets/[^"'?#]+`)
	moduleEntryBeforeType    = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc\s*=\s*["'](?:\.?/)?assets/[^"'?#]+["'][^>]*\btype\s*=\s*["']module["']`)
	browserServiceWorkerFile = "sw-astrorder.js"
)

// ValidateStaticDir verifies that a configured static frontend directory exists,
// contains a non-empty confined index.html, does not point at unbuilt /src/
// files, and has every /assets/... file referenced by index.html present.
func ValidateStaticDir(dir string) error {
	_, _, err := inspectStaticDir(dir)
	return err
}

// ValidateBrowserDist verifies the full Web/PWA static bundle contract before
// syncing or publishing to an installed server root: non-empty compiled
// index.html with a module entry under /assets/, non-empty assets/ directory
// containing all referenced files, and the browser PWA service worker.
func ValidateBrowserDist(dir string) error {
	resolvedRoot, indexHTML, err := inspectStaticDir(dir)
	if err != nil {
		return err
	}
	if !moduleEntryBeforeSrc.MatchString(indexHTML) && !moduleEntryBeforeType.MatchString(indexHTML) {
		return errors.New("frontend/dist/index.html is missing a compiled module script entry under /assets/")
	}
	assetsDir := filepath.Join(resolvedRoot, "assets")
	resolvedAssets, err := filepath.EvalSymlinks(assetsDir)
	if err != nil || !pathWithin(resolvedRoot, resolvedAssets) {
		return errors.New("frontend/dist must contain a confined assets/ directory")
	}
	assetsInfo, err := os.Stat(resolvedAssets)
	if err != nil || !assetsInfo.IsDir() {
		return errors.New("frontend/dist/assets must be a directory")
	}
	entries, err := os.ReadDir(resolvedAssets)
	if err != nil {
		return fmt.Errorf("read frontend/dist/assets: %w", err)
	}
	hasRegularAsset := false
	for _, entry := range entries {
		info, statErr := entry.Info()
		if statErr == nil && info.Mode().IsRegular() && info.Size() > 0 {
			hasRegularAsset = true
			break
		}
	}
	if !hasRegularAsset {
		return errors.New("frontend/dist/assets must contain compiled bundle files")
	}
	if err := validateConfinedNonEmptyFile(resolvedRoot, browserServiceWorkerFile); err != nil {
		return fmt.Errorf("frontend/dist is missing browser PWA worker %s (rebuild without ASTRORDER_WAILS=1): %w", browserServiceWorkerFile, err)
	}
	return nil
}

func inspectStaticDir(dir string) (string, string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", "", errors.New("static directory path is empty")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", "", fmt.Errorf("resolve static directory: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		return "", "", fmt.Errorf("static directory does not exist: %w", err)
	}
	rootInfo, err := os.Stat(resolvedRoot)
	if err != nil || !rootInfo.IsDir() {
		return "", "", errors.New("static directory must be an existing directory")
	}
	if err := validateConfinedNonEmptyFile(resolvedRoot, "index.html"); err != nil {
		return "", "", fmt.Errorf("static directory must contain a non-empty index.html: %w", err)
	}
	rawIndex, err := os.ReadFile(filepath.Join(resolvedRoot, "index.html"))
	if err != nil {
		return "", "", fmt.Errorf("read static index.html: %w", err)
	}
	indexHTML := string(rawIndex)
	if unbuiltSourceRefPattern.MatchString(indexHTML) {
		return "", "", errors.New("static index.html references unbuilt /src/ assets; run frontend production build")
	}
	matches := builtAssetRefPattern.FindAllStringSubmatch(indexHTML, -1)
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		rawRef := strings.ReplaceAll(match[1], `\`, "/")
		trimmed := strings.TrimPrefix(strings.TrimPrefix(rawRef, "./"), "/")
		cleaned := path.Clean("/" + trimmed)
		if !strings.HasPrefix(cleaned, "/assets/") {
			return "", "", fmt.Errorf("static index.html references invalid asset path %q", match[1])
		}
		rel := strings.TrimPrefix(cleaned, "/")
		if _, dup := seen[rel]; dup {
			continue
		}
		seen[rel] = struct{}{}
		if err := validateConfinedNonEmptyFile(resolvedRoot, filepath.FromSlash(rel)); err != nil {
			return "", "", fmt.Errorf("static index.html references missing asset %q: %w", "/"+rel, err)
		}
	}
	return resolvedRoot, indexHTML, nil
}

func validateConfinedNonEmptyFile(resolvedRoot, relativePath string) error {
	candidate := filepath.Join(resolvedRoot, relativePath)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return err
	}
	if !pathWithin(resolvedRoot, resolved) {
		return fmt.Errorf("%s escapes static directory", relativePath)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", relativePath)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%s is empty", relativePath)
	}
	return nil
}
