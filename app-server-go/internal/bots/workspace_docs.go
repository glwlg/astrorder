package bots

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"astrorder.dev/app-server/internal/targetfs"
)

const (
	maxDocReadBytes    = 2 << 20   // 2 MiB for AGENTS.md / SKILL.md
	maxAttachFileBytes = 256 << 20 // 256 MiB max per staged attachment
)

// isPOSIXTarget reports whether paths in this workspace should use POSIX forward slashes.
func isPOSIXTarget(connectionID, workspace string) bool {
	cid := strings.TrimSpace(connectionID)
	if cid != "" && !strings.EqualFold(cid, targetfs.LocalConnectionID) {
		return true
	}
	ws := strings.TrimSpace(workspace)
	if strings.HasPrefix(ws, "/") {
		return true
	}
	return false
}

func joinTargetPath(connectionID, workspace string, segments ...string) string {
	ws := strings.TrimSpace(workspace)
	if isPOSIXTarget(connectionID, ws) {
		clean := strings.ReplaceAll(ws, "\\", "/")
		parts := append([]string{clean}, segments...)
		return path.Join(parts...)
	}
	parts := append([]string{ws}, segments...)
	return filepath.Join(parts...)
}

func resolveBotFS(ctx context.Context, resolver *targetfs.Resolver, connectionID, workspace string) (targetfs.Resolved, error) {
	if resolver == nil {
		return targetfs.Resolved{}, errors.New("filesystem resolver is not configured")
	}
	return resolver.Resolve(ctx, targetfs.ResolveRequest{
		Workspace:    workspace,
		ConnectionID: connectionID,
	})
}

func readSmallTextFile(ctx context.Context, fs targetfs.FileSystem, filePath string) (string, bool, error) {
	res, err := fs.Read(ctx, filePath)
	if err != nil {
		var tErr *targetfs.Error
		if errors.As(err, &tErr) && (tErr.Status == http.StatusNotFound || tErr.Code == "not_found") {
			return "", false, nil
		}
		if strings.Contains(strings.ToLower(err.Error()), "no such file") || strings.Contains(strings.ToLower(err.Error()), "not found") || strings.Contains(strings.ToLower(err.Error()), "cannot find") {
			return "", false, nil
		}
		return "", false, err
	}
	defer res.Reader.Close()
	data, err := io.ReadAll(io.LimitReader(res.Reader, maxDocReadBytes+1))
	if err != nil {
		return "", false, err
	}
	if len(data) > maxDocReadBytes {
		data = data[:maxDocReadBytes]
	}
	return string(data), true, nil
}

func ensureTargetDir(ctx context.Context, fs targetfs.FileSystem, connectionID, workspace string, relSegments ...string) (string, error) {
	current := strings.TrimSpace(workspace)
	if current == "" {
		return "", errors.New("workspace is required")
	}
	for _, seg := range relSegments {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, `/\`) {
			return "", fmt.Errorf("invalid directory segment: %q", seg)
		}
		nextPath := joinTargetPath(connectionID, current, seg)
		if info, err := fs.Stat(ctx, nextPath); err == nil {
			if !info.IsDir {
				return "", fmt.Errorf("%s exists and is not a directory", nextPath)
			}
			current = nextPath
			continue
		}
		if _, err := fs.Mkdir(ctx, targetfs.MkdirRequest{Parent: current, Name: seg}); err != nil {
			// Recheck Stat in case of concurrent creation or already-exists response.
			if info, statErr := fs.Stat(ctx, nextPath); statErr == nil && info.IsDir {
				current = nextPath
				continue
			}
			return "", fmt.Errorf("create directory %s: %w", nextPath, err)
		}
		current = nextPath
	}
	return current, nil
}

// ReadAgentsMD loads <workspace>/AGENTS.md (or returns exists=false if missing).
func (s *Service) ReadAgentsMD(ctx context.Context, bot BotProfile) (filePath, content string, exists bool, err error) {
	resolved, err := resolveBotFS(ctx, s.options.Resolver, bot.ConnectionID, bot.Workspace)
	if err != nil {
		return "", "", false, err
	}
	filePath = joinTargetPath(bot.ConnectionID, resolved.Workspace, "AGENTS.md")
	content, exists, err = readSmallTextFile(ctx, resolved.FileSystem, filePath)
	return filePath, content, exists, err
}

// WriteAgentsMD writes <workspace>/AGENTS.md atomically across Local, WSL, or SSH.
func (s *Service) WriteAgentsMD(ctx context.Context, bot BotProfile, content string) (string, error) {
	resolved, err := resolveBotFS(ctx, s.options.Resolver, bot.ConnectionID, bot.Workspace)
	if err != nil {
		return "", err
	}
	filePath := joinTargetPath(bot.ConnectionID, resolved.Workspace, "AGENTS.md")
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if normalized != "" && !strings.HasSuffix(normalized, "\n") {
		normalized += "\n"
	}
	if _, err := resolved.FileSystem.WriteAtomic(ctx, filePath, strings.NewReader(normalized), 0o644); err != nil {
		return "", fmt.Errorf("write AGENTS.md: %w", err)
	}
	return filePath, nil
}

// ListSkills reads skills from <workspace>/.agents/skills/*/SKILL.md.
func (s *Service) ListSkills(ctx context.Context, bot BotProfile, includeContent bool) ([]BotSkillDoc, error) {
	resolved, err := resolveBotFS(ctx, s.options.Resolver, bot.ConnectionID, bot.Workspace)
	if err != nil {
		return nil, err
	}
	skillsDir := joinTargetPath(bot.ConnectionID, resolved.Workspace, ".agents", "skills")
	listing, err := resolved.FileSystem.List(ctx, targetfs.ListRequest{Path: skillsDir, Depth: 1})
	if err != nil {
		// Directory doesn't exist yet -> zero skills.
		return []BotSkillDoc{}, nil
	}
	docs := make([]BotSkillDoc, 0, len(listing.Items))
	for _, entry := range listing.Items {
		if !entry.IsDir {
			continue
		}
		slug := strings.TrimSpace(entry.Name)
		if slug == "" || strings.HasPrefix(slug, ".") {
			continue
		}
		skillPath := joinTargetPath(bot.ConnectionID, skillsDir, slug, "SKILL.md")
		content, exists, readErr := readSmallTextFile(ctx, resolved.FileSystem, skillPath)
		if readErr != nil || !exists {
			continue
		}
		name, desc := parseSkillFrontmatter(content, slug)
		doc := BotSkillDoc{
			BotSkillSummary: BotSkillSummary{
				Slug:        slug,
				Name:        name,
				Description: desc,
				Path:        skillPath,
			},
		}
		if includeContent {
			doc.Content = content
			skillFolder := joinTargetPath(bot.ConnectionID, skillsDir, slug)
			if subListing, subErr := resolved.FileSystem.List(ctx, targetfs.ListRequest{Path: skillFolder, Depth: 2}); subErr == nil {
				prefix := strings.TrimRight(strings.ReplaceAll(skillFolder, "\\", "/"), "/") + "/"
				aux := make([]string, 0, 8)
				var collectAux func(entries []targetfs.Entry)
				collectAux = func(entries []targetfs.Entry) {
					for _, item := range entries {
						if len(aux) >= 24 {
							return
						}
						if item.IsDir {
							if item.Children != nil && !strings.HasPrefix(item.Name, ".") {
								collectAux(*item.Children)
							}
							continue
						}
						rel := strings.TrimPrefix(strings.ReplaceAll(item.Path, "\\", "/"), prefix)
						if rel == "" || strings.EqualFold(rel, "SKILL.md") || strings.HasPrefix(path.Base(rel), ".") {
							continue
						}
						aux = append(aux, rel)
					}
				}
				collectAux(subListing.Items)
				sort.Strings(aux)
				doc.Files = aux
			}
		}
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool {
		return strings.ToLower(docs[i].Slug) < strings.ToLower(docs[j].Slug)
	})
	return docs, nil
}

// WriteSkill writes <workspace>/.agents/skills/<skillSlug>/SKILL.md atomically.
func (s *Service) WriteSkill(ctx context.Context, bot BotProfile, rawSlug, content string) (*BotSkillDoc, error) {
	slug := NormalizeSlug(rawSlug)
	if slug == "" {
		return nil, fmt.Errorf("%w: Skill 标识仅支持英文字母、数字与连字符", ErrInvalid)
	}
	resolved, err := resolveBotFS(ctx, s.options.Resolver, bot.ConnectionID, bot.Workspace)
	if err != nil {
		return nil, err
	}
	skillDir, err := ensureTargetDir(ctx, resolved.FileSystem, bot.ConnectionID, resolved.Workspace, ".agents", "skills", slug)
	if err != nil {
		return nil, err
	}
	skillPath := joinTargetPath(bot.ConnectionID, skillDir, "SKILL.md")
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if normalized != "" && !strings.HasSuffix(normalized, "\n") {
		normalized += "\n"
	}
	if _, err := resolved.FileSystem.WriteAtomic(ctx, skillPath, strings.NewReader(normalized), 0o644); err != nil {
		return nil, fmt.Errorf("write skill %s: %w", slug, err)
	}
	name, desc := parseSkillFrontmatter(normalized, slug)
	return &BotSkillDoc{
		BotSkillSummary: BotSkillSummary{
			Slug:        slug,
			Name:        name,
			Description: desc,
			Path:        skillPath,
		},
		Content: normalized,
	}, nil
}

// UploadInput holds one uploaded file payload from browser multipart upload.
type UploadInput struct {
	Name string
	Data []byte
}

// StageUploadedFilesToInbox writes browser-uploaded files into <bot.Workspace>/inbox/upload-<stamp>/
// so they can be attached to a Bot dispatch across Local, WSL, or SSH workspaces.
func (s *Service) StageUploadedFilesToInbox(ctx context.Context, bot BotProfile, files []UploadInput) ([]UploadedInboxFile, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: 请至少选择一个要上传的文件", ErrInvalid)
	}
	botResolved, err := resolveBotFS(ctx, s.options.Resolver, bot.ConnectionID, bot.Workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve bot workspace: %w", err)
	}
	batchID := "upload-" + randomHex(4)
	inboxDir, err := ensureTargetDir(ctx, botResolved.FileSystem, bot.ConnectionID, botResolved.Workspace, "inbox", batchID)
	if err != nil {
		return nil, err
	}
	usedNames := make(map[string]int, len(files))
	uploaded := make([]UploadedInboxFile, 0, len(files))
	for _, f := range files {
		if len(f.Data) == 0 {
			continue
		}
		baseName := dedupeFileName(sanitizeFileName(f.Name), usedNames)
		dstPath := joinTargetPath(bot.ConnectionID, inboxDir, baseName)
		if _, err := botResolved.FileSystem.WriteAtomic(ctx, dstPath, bytes.NewReader(f.Data), 0o644); err != nil {
			return uploaded, fmt.Errorf("upload %q to bot inbox: %w", baseName, err)
		}
		uploaded = append(uploaded, UploadedInboxFile{
			Name: baseName,
			Path: dstPath,
			Size: int64(len(f.Data)),
		})
	}
	if len(uploaded) == 0 {
		return nil, fmt.Errorf("%w: 上传的文件内容为空", ErrInvalid)
	}
	return uploaded, nil
}

// StageAttachmentsToInbox copies source files from caller environment into <bot.Workspace>/inbox/<jobID>/.
func (s *Service) StageAttachmentsToInbox(ctx context.Context, callerConnID, callerWorkspace string, bot BotProfile, jobID string, attachPaths []string) (string, []StagedFile, error) {
	if len(attachPaths) == 0 {
		return "", nil, nil
	}
	botResolved, err := resolveBotFS(ctx, s.options.Resolver, bot.ConnectionID, bot.Workspace)
	if err != nil {
		return "", nil, fmt.Errorf("resolve bot workspace: %w", err)
	}
	inboxDir, err := ensureTargetDir(ctx, botResolved.FileSystem, bot.ConnectionID, botResolved.Workspace, "inbox", jobID)
	if err != nil {
		return "", nil, err
	}

	botInboxPrefix := strings.TrimRight(strings.ReplaceAll(joinTargetPath(bot.ConnectionID, botResolved.Workspace, "inbox"), "\\", "/"), "/") + "/"
	usedNames := make(map[string]int, len(attachPaths))
	staged := make([]StagedFile, 0, len(attachPaths))

	for _, rawPath := range attachPaths {
		srcPath := strings.TrimSpace(rawPath)
		if srcPath == "" {
			continue
		}
		normSrc := strings.ReplaceAll(srcPath, "\\", "/")
		// Files uploaded directly to this Bot's inbox via /upload live on the Bot's own filesystem.
		alreadyInBotInbox := strings.HasPrefix(strings.ToLower(normSrc), strings.ToLower(botInboxPrefix))
		effectiveConn := callerConnID
		effectiveRoot := callerWorkspace
		if alreadyInBotInbox {
			effectiveConn = bot.ConnectionID
			effectiveRoot = botResolved.Workspace
		}

		// Resolve source path relative to effectiveRoot if relative.
		resolvedSrc := srcPath
		if !isAbsolutePath(srcPath) && strings.TrimSpace(effectiveRoot) != "" {
			resolvedSrc = joinTargetPath(effectiveConn, effectiveRoot, srcPath)
		}
		srcRoot := effectiveRoot
		if strings.TrimSpace(srcRoot) == "" {
			srcRoot = parentDirOf(effectiveConn, resolvedSrc)
		}
		callerResolved, err := resolveBotFS(ctx, s.options.Resolver, effectiveConn, srcRoot)
		if err != nil {
			return inboxDir, staged, fmt.Errorf("resolve caller file %q: %w", srcPath, err)
		}
		readRes, err := callerResolved.FileSystem.Read(ctx, resolvedSrc)
		if err != nil {
			// Fallback: retry with parent directory of resolvedSrc as scope root if file lives outside effectiveRoot.
			altRoot := parentDirOf(effectiveConn, resolvedSrc)
			if altRoot != "" && altRoot != srcRoot {
				if altResolved, altErr := resolveBotFS(ctx, s.options.Resolver, effectiveConn, altRoot); altErr == nil {
					readRes, err = altResolved.FileSystem.Read(ctx, resolvedSrc)
				}
			}
			if err != nil {
				return inboxDir, staged, fmt.Errorf("read attachment %q: %w", srcPath, err)
			}
		}

		data, readErr := readBoundedAttachment(readRes.Reader, maxAttachFileBytes)
		_ = readRes.Reader.Close()
		if readErr != nil {
			return inboxDir, staged, fmt.Errorf("read attachment %q: %w", srcPath, readErr)
		}

		baseName := sanitizeFileName(path.Base(strings.ReplaceAll(resolvedSrc, "\\", "/")))
		baseName = dedupeFileName(baseName, usedNames)
		dstPath := joinTargetPath(bot.ConnectionID, inboxDir, baseName)
		if _, err := botResolved.FileSystem.WriteAtomic(ctx, dstPath, bytes.NewReader(data), 0o644); err != nil {
			return inboxDir, staged, fmt.Errorf("stage attachment %q into bot inbox: %w", srcPath, err)
		}
		staged = append(staged, StagedFile{
			SourcePath: resolvedSrc,
			TargetPath: dstPath,
			Name:       baseName,
			Size:       int64(len(data)),
		})
	}
	return inboxDir, staged, nil
}

func readBoundedAttachment(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("attachment exceeds maximum size of %d bytes", limit)
	}
	return data, nil
}

func isAbsolutePath(p string) bool {
	trimmed := strings.TrimSpace(p)
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\\`) {
		return true
	}
	if len(trimmed) >= 3 && trimmed[1] == ':' && (trimmed[2] == '\\' || trimmed[2] == '/') {
		return true
	}
	return false
}

func parentDirOf(connectionID, fullPath string) string {
	trimmed := strings.TrimSpace(fullPath)
	if isPOSIXTarget(connectionID, trimmed) {
		return path.Dir(strings.ReplaceAll(trimmed, "\\", "/"))
	}
	return filepath.Dir(trimmed)
}

func sanitizeFileName(name string) string {
	clean := strings.TrimSpace(name)
	clean = strings.ReplaceAll(clean, "/", "_")
	clean = strings.ReplaceAll(clean, "\\", "_")
	if clean == "" || clean == "." || clean == ".." {
		return "attachment"
	}
	return clean
}

func dedupeFileName(name string, used map[string]int) string {
	key := strings.ToLower(name)
	count := used[key]
	used[key] = count + 1
	if count == 0 {
		return name
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	return fmt.Sprintf("%s-%d%s", stem, count+1, ext)
}

func parseSkillFrontmatter(content, fallbackSlug string) (name, description string) {
	name = fallbackSlug
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		// Fallback: first markdown heading + first non-empty body line.
		for _, line := range lines {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "# ") && name == fallbackSlug {
				name = strings.TrimSpace(strings.TrimPrefix(t, "# "))
				continue
			}
			if t != "" && !strings.HasPrefix(t, "#") && description == "" {
				description = truncateRunes(t, 120)
				break
			}
		}
		return name, description
	}
	for i := 1; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "---" {
			break
		}
		if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
			continue
		}
		if strings.HasPrefix(trimmed, "name:") {
			v, nextIdx := readYAMLFrontmatterValue(lines, i, strings.TrimPrefix(trimmed, "name:"))
			i = nextIdx
			if v != "" {
				name = v
			}
		} else if strings.HasPrefix(trimmed, "description:") {
			v, nextIdx := readYAMLFrontmatterValue(lines, i, strings.TrimPrefix(trimmed, "description:"))
			i = nextIdx
			if v != "" {
				description = truncateRunes(v, 240)
			}
		}
	}
	return name, description
}

func readYAMLFrontmatterValue(lines []string, idx int, inline string) (string, int) {
	v := strings.TrimSpace(inline)
	isBlockScalar := v == "" || v == ">" || v == ">-" || v == ">+" || v == "|" || v == "|-" || v == "|+"
	if !isBlockScalar {
		return unquoteYAML(v), idx
	}
	parts := make([]string, 0, 4)
	lastIdx := idx
	for j := idx + 1; j < len(lines); j++ {
		raw := lines[j]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "---" {
			break
		}
		if trimmed == "" {
			if len(parts) > 0 {
				lastIdx = j
			}
			continue
		}
		if !strings.HasPrefix(raw, " ") && !strings.HasPrefix(raw, "\t") {
			break
		}
		parts = append(parts, trimmed)
		lastIdx = j
	}
	return strings.Join(parts, " "), lastIdx
}

func unquoteYAML(v string) string {
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1]
	}
	return v
}

func excerptAgentsMD(content string) string {
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || t == "---" {
			continue
		}
		t = strings.TrimLeft(t, "-*> ")
		if t != "" {
			return truncateRunes(t, 140)
		}
	}
	return ""
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}

var (
	backtickSkillSlugPattern = regexp.MustCompile("`([a-z][a-z0-9]*(?:-[a-z0-9]+)+)`")
	nonSkillSlugBlocklist    = map[string]struct{}{
		"astrorder-agent": {},
		"job-id":          {},
		"full-access":     {},
		"agents-md":       {},
		"skill-md":        {},
		"utf-8":           {},
		"kebab-case":      {},
	}
)

// extractReferencedExternalSkills scans AGENTS.md for referenced skill names/paths
// that are not already present in the workspace's own .agents/skills/ folder (e.g., global/system skills).
func extractReferencedExternalSkills(agentsMD string, workspaceSkills []BotSkillDoc) []string {
	if strings.TrimSpace(agentsMD) == "" {
		return nil
	}
	localSet := make(map[string]struct{}, len(workspaceSkills))
	for _, sk := range workspaceSkills {
		localSet[strings.ToLower(strings.TrimSpace(sk.Slug))] = struct{}{}
		localSet[strings.ToLower(strings.TrimSpace(sk.Name))] = struct{}{}
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, 6)
	add := func(raw string) {
		slug := strings.ToLower(strings.TrimSpace(raw))
		if slug == "" || len(slug) > 64 {
			return
		}
		if _, blocked := nonSkillSlugBlocklist[slug]; blocked {
			return
		}
		if _, isLocal := localSet[slug]; isLocal {
			return
		}
		if _, dup := seen[slug]; dup {
			return
		}
		seen[slug] = struct{}{}
		out = append(out, slug)
	}

	for _, match := range skillPathPattern.FindAllStringSubmatch(agentsMD, -1) {
		if len(match) >= 2 {
			add(match[1])
		}
	}

	inSkillSection := false
	for _, line := range strings.Split(strings.ReplaceAll(agentsMD, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		low := strings.ToLower(trimmed)
		if strings.HasPrefix(trimmed, "#") {
			inSkillSection = strings.Contains(low, "skill") || strings.Contains(trimmed, "技能")
			continue
		}
		if !inSkillSection && !strings.Contains(low, "skill") && !strings.Contains(trimmed, "技能") {
			continue
		}
		for _, match := range backtickSkillSlugPattern.FindAllStringSubmatch(trimmed, -1) {
			if len(match) >= 2 {
				add(match[1])
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}
