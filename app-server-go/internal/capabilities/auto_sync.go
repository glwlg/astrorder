package capabilities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/agentskill"
	"astrorder.dev/app-server/internal/capabilities/officialskills"
	"astrorder.dev/app-server/internal/envtarget"
)

const (
	autoSyncStartupDelay   = 2 * time.Second
	autoSyncMachineTimeout = 5 * time.Minute
	autoSyncMaxConcurrency = 4
)

// AutoSyncReport summarizes one background pass of Astrorder-managed skill & CLI synchronization.
type AutoSyncReport struct {
	MachinesChecked int      `json:"machines_checked"`
	MachinesSkipped int      `json:"machines_skipped"`
	SkillsUpdated   []string `json:"skills_updated"`
	SkillsInstalled []string `json:"skills_installed"`
	SkillsRemoved   []string `json:"skills_removed"`
	SkillsUnchanged int      `json:"skills_unchanged"`
	Errors          []string `json:"errors,omitempty"`
}

// StartAutoSync schedules a non-blocking startup check that keeps Astrorder's
// managed SKILL.md files, enabled plugin skills, and astrorder-agent binaries
// up to date across all reachable local and SSH machines.
func (s *Service) StartAutoSync(ctx context.Context) {
	if s == nil || s.Targets == nil {
		return
	}
	go func() {
		timer := time.NewTimer(autoSyncStartupDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.TriggerAutoSync(ctx)
	}()
}

// TriggerAutoSync coalesces concurrent refresh requests into a single non-blocking
// background worker loop so rapid plugin toggles never overlap or block callers.
func (s *Service) TriggerAutoSync(ctx context.Context) {
	if s == nil || s.Targets == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.autoSyncMu.Lock()
	if s.autoSyncRunning {
		s.autoSyncPending = true
		s.autoSyncMu.Unlock()
		return
	}
	s.autoSyncRunning = true
	s.autoSyncPending = false
	s.autoSyncMu.Unlock()

	go func() {
		for {
			if ctx.Err() != nil {
				s.autoSyncMu.Lock()
				s.autoSyncRunning = false
				s.autoSyncPending = false
				s.autoSyncMu.Unlock()
				return
			}
			report := s.AutoSyncManagedSkills(ctx)
			s.logAutoSyncReport(report)

			s.autoSyncMu.Lock()
			if !s.autoSyncPending {
				s.autoSyncRunning = false
				s.autoSyncMu.Unlock()
				return
			}
			s.autoSyncPending = false
			s.autoSyncMu.Unlock()
		}
	}()
}

func (s *Service) logAutoSyncReport(report AutoSyncReport) {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if len(report.SkillsUpdated) > 0 || len(report.SkillsInstalled) > 0 || len(report.SkillsRemoved) > 0 || len(report.Errors) > 0 {
		logger.Info("astrorder skill/cli auto-sync completed",
			"machines_checked", report.MachinesChecked,
			"machines_skipped", report.MachinesSkipped,
			"updated", len(report.SkillsUpdated),
			"installed", len(report.SkillsInstalled),
			"removed", len(report.SkillsRemoved),
			"unchanged", report.SkillsUnchanged,
			"errors", len(report.Errors),
			"error_details", report.Errors,
		)
	}
}

// AutoSyncManagedSkills inspects every reachable machine and configured Agent home:
//  1. Upgrades already-installed unmodified official skills when SKILL.md or bundled astrorder-agent changed.
//  2. Installs or upgrades SKILL.md (+ CLI) for currently enabled plugins.
//  3. Safely removes unmodified skills belonging to plugins that were disabled or uninstalled.
//
// User-edited or unmanaged files are never overwritten or deleted.
func (s *Service) AutoSyncManagedSkills(ctx context.Context) AutoSyncReport {
	report := AutoSyncReport{
		SkillsUpdated:   []string{},
		SkillsInstalled: []string{},
		SkillsRemoved:   []string{},
	}
	if s == nil || s.Targets == nil {
		return report
	}
	targets, err := s.Targets.List(ctx)
	if err != nil || len(targets) == 0 {
		return report
	}

	pluginSkills := s.pluginSkills(ctx)
	var mu sync.Mutex
	sem := make(chan struct{}, autoSyncMaxConcurrency)
	var wg sync.WaitGroup

	for _, target := range targets {
		if ctx.Err() != nil {
			break
		}
		target := target
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			mCtx, cancel := context.WithTimeout(ctx, autoSyncMachineTimeout)
			defer cancel()
			mReport := s.syncMachineManagedSkills(mCtx, target, pluginSkills)
			mu.Lock()
			report.MachinesChecked += mReport.MachinesChecked
			report.MachinesSkipped += mReport.MachinesSkipped
			report.SkillsUnchanged += mReport.SkillsUnchanged
			report.SkillsUpdated = append(report.SkillsUpdated, mReport.SkillsUpdated...)
			report.SkillsInstalled = append(report.SkillsInstalled, mReport.SkillsInstalled...)
			report.SkillsRemoved = append(report.SkillsRemoved, mReport.SkillsRemoved...)
			report.Errors = append(report.Errors, mReport.Errors...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Strings(report.SkillsUpdated)
	sort.Strings(report.SkillsInstalled)
	sort.Strings(report.SkillsRemoved)
	return report
}

func (s *Service) syncMachineManagedSkills(ctx context.Context, target envtarget.Target, enabledPluginSkills []PluginSkill) AutoSyncReport {
	report := AutoSyncReport{}
	env, err := s.open(ctx, target.ID)
	if err != nil {
		report.MachinesSkipped = 1
		return report
	}
	paths, err := env.Paths(ctx)
	if err != nil {
		report.MachinesSkipped = 1
		return report
	}
	report.MachinesChecked = 1

	cliProvider := s.CLIBinary
	if cliProvider == nil {
		cliProvider = bundledSessionCLI
	}
	binName, bundledBin, cliErr := cliProvider(ctx, env)
	bundledBinHash := ""
	if cliErr == nil && (binName == "astrorder-agent" || binName == "astrorder-agent.exe") && len(bundledBin) > 0 {
		bundledBinHash = payloadHash(bundledBin)
	} else {
		bundledBin = nil
	}

	conn := target.ConnectionID
	if conn == "" {
		conn = "local"
	}

	activePlugins := make(map[string]PluginSkill, len(enabledPluginSkills))
	validPluginSkills := make([]PluginSkill, 0, len(enabledPluginSkills))
	for _, ps := range enabledPluginSkills {
		ps.Name = strings.TrimSpace(ps.Name)
		ps.PluginID = strings.TrimSpace(ps.PluginID)
		if ps.Name == "" || ps.PluginID == "" || strings.TrimSpace(ps.Content) == "" {
			continue
		}
		if !skillNameRE.MatchString(ps.Name) {
			continue
		}
		if _, isOfficial := officialskills.GetOfficialSkill(ps.Name); isOfficial {
			continue
		}
		key := ps.PluginID + "\x00" + ps.Name
		if _, dup := activePlugins[key]; dup {
			continue
		}
		activePlugins[key] = ps
		validPluginSkills = append(validPluginSkills, ps)
	}

	for _, sa := range registeredSkillAgents() {
		agent := sa.ID
		home := skillHome(paths, agent)
		if home == "" {
			continue
		}
		callerRaw, ok := readValidCallerBinding(ctx, env, home, agent, conn)
		if !ok {
			// Agent has not been provisioned with Astrorder CLI on this machine — leave untouched.
			continue
		}
		callerHash := payloadHash(callerRaw)
		s.syncAgentHomeManagedSkills(ctx, env, target.ID, home, agent, callerHash, binName, bundledBin, bundledBinHash, validPluginSkills, activePlugins, &report)
	}
	return report
}

func readValidCallerBinding(ctx context.Context, env envtarget.Env, home, agent, conn string) ([]byte, bool) {
	callerPath := path.Join(home, agentskill.CallerFile)
	if err := checkOfficialTarget(ctx, env, home, callerPath); err != nil {
		return nil, false
	}
	raw, err := env.Read(ctx, callerPath)
	if err != nil {
		return nil, false
	}
	var caller map[string]any
	if json.Unmarshal(raw, &caller) != nil || len(caller) != 4 {
		return nil, false
	}
	for _, key := range []string{"agent_id", "connection_id", "profile_name", "runtime"} {
		v, ok := caller[key].(string)
		if !ok || v == "" || strings.ContainsAny(v, " \t\r\n") {
			return nil, false
		}
	}
	if caller["runtime"] != agent || caller["connection_id"] != conn {
		return nil, false
	}
	return raw, true
}

func (s *Service) syncAgentHomeManagedSkills(
	ctx context.Context,
	env envtarget.Env,
	targetID, home, agent, callerHash string,
	binName string,
	bundledBin []byte,
	bundledBinHash string,
	validPluginSkills []PluginSkill,
	activePlugins map[string]PluginSkill,
	report *AutoSyncReport,
) {
	unlock := s.lock(targetID + "|skills-dispatch|" + home + "/skills")
	defer unlock()

	// 1. Auto-upgrade already-installed Astrorder official skills & bundled CLI.
	for _, skill := range officialskills.ListOfficialSkills() {
		if ctx.Err() != nil {
			return
		}
		folder := path.Join(home, "skills", skill.Name)
		manifest, exists := readManagedSkillManifest(ctx, env, home, folder)
		if !exists {
			continue
		}
		if manifest.PluginID != "" || manifest.Skill != skill.Name || manifest.Agent != agent || manifest.CallerHash != callerHash {
			continue
		}
		renderedMD := officialskills.RenderOfficialSkill(skill, home)
		wantMDHash := payloadHash(renderedMD)
		needMD := manifest.Files["SKILL.md"] != wantMDHash
		needCLI := false
		relBin := ""
		if skill.RequiresCLI && bundledBinHash != "" {
			relBin = "scripts/" + binName
			needCLI = manifest.Files[relBin] != bundledBinHash
		}
		if !needMD && !needCLI {
			report.SkillsUnchanged++
			continue
		}
		files := map[string][]byte{"SKILL.md": renderedMD}
		var verifyFns []func() error
		if skill.RequiresCLI {
			if bundledBinHash != "" {
				files[relBin] = bundledBin
				if needCLI {
					cliPath := path.Join(folder, relBin)
					verifyFns = append(verifyFns, func() error {
						return verifyCLIExecutable(ctx, env, home, cliPath, bundledBinHash)
					})
				}
			} else {
				// Preserve existing CLI binary if bundled binary for this arch isn't present.
				for _, cand := range []string{"scripts/astrorder-agent.exe", "scripts/astrorder-agent"} {
					if expected := manifest.Files[cand]; expected != "" {
						if existing, rErr := env.Read(ctx, path.Join(folder, cand)); rErr == nil && payloadHash(existing) == expected {
							files[cand] = existing
						}
					}
				}
			}
		}
		if err := writeManagedOfficialFiles(ctx, env, folder, agent, skill.Name, "", files, verifyFns...); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s/%s/%s: %v", targetID, agent, skill.Name, err))
			continue
		}
		report.SkillsUpdated = append(report.SkillsUpdated, fmt.Sprintf("%s/%s/%s", targetID, agent, skill.Name))
	}

	// 2. Auto-install or auto-upgrade enabled plugin skills.
	for _, ps := range validPluginSkills {
		if ctx.Err() != nil {
			return
		}
		folder := path.Join(home, "skills", ps.Name)
		if err := checkOfficialTarget(ctx, env, home, folder); err != nil {
			continue
		}
		renderedMD := officialskills.RenderSkillContent(ps.Content, home)
		wantMDHash := payloadHash(renderedMD)
		manifest, exists := readManagedSkillManifest(ctx, env, home, folder)
		if exists {
			if manifest.PluginID != ps.PluginID || manifest.Skill != ps.Name || manifest.Agent != agent || manifest.CallerHash != callerHash {
				continue
			}
			needMD := manifest.Files["SKILL.md"] != wantMDHash
			needCLI := bundledBinHash != "" && manifest.Files["scripts/"+binName] != bundledBinHash
			if !needMD && !needCLI {
				report.SkillsUnchanged++
				continue
			}
		} else if env.IsDir(ctx, folder) {
			if entries, lErr := env.ListDir(ctx, folder); lErr != nil || len(entries) > 0 {
				// Non-empty unmanaged directory belongs to the user; never touch it.
				continue
			}
		}

		files := map[string][]byte{"SKILL.md": renderedMD}
		var verifyFns []func() error
		if bundledBinHash != "" {
			relBin := "scripts/" + binName
			files[relBin] = bundledBin
			cliPath := path.Join(folder, relBin)
			verifyFns = append(verifyFns, func() error {
				return verifyCLIExecutable(ctx, env, home, cliPath, bundledBinHash)
			})
		}
		if err := writeManagedOfficialFiles(ctx, env, folder, agent, ps.Name, ps.PluginID, files, verifyFns...); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s/%s/%s: %v", targetID, agent, ps.Name, err))
			continue
		}
		if exists {
			report.SkillsUpdated = append(report.SkillsUpdated, fmt.Sprintf("%s/%s/%s", targetID, agent, ps.Name))
		} else {
			report.SkillsInstalled = append(report.SkillsInstalled, fmt.Sprintf("%s/%s/%s", targetID, agent, ps.Name))
		}
	}

	// 3. Auto-remove unmodified skills whose owning plugin was disabled or uninstalled.
	skillsRoot := path.Join(home, "skills")
	entries, err := env.ListDir(ctx, skillsRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		if !entry.Dir || entry.Name == "" || strings.HasPrefix(entry.Name, ".") {
			continue
		}
		folder := path.Join(skillsRoot, entry.Name)
		manifest, exists := readManagedSkillManifest(ctx, env, home, folder)
		if !exists || manifest.PluginID == "" || manifest.Agent != agent || manifest.CallerHash != callerHash {
			continue
		}
		if _, active := activePlugins[manifest.PluginID+"\x00"+manifest.Skill]; active {
			continue
		}
		if !isManagedSkillUnmodified(ctx, env, home, folder, manifest) {
			continue
		}
		if err := env.RemoveAll(ctx, folder); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s/%s/%s(remove): %v", targetID, agent, manifest.Skill, err))
			continue
		}
		report.SkillsRemoved = append(report.SkillsRemoved, fmt.Sprintf("%s/%s/%s", targetID, agent, manifest.Skill))
	}
}

func readManagedSkillManifest(ctx context.Context, env envtarget.Env, home, folder string) (officialManifest, bool) {
	manifestPath := path.Join(folder, officialManifestFile)
	if err := checkOfficialTarget(ctx, env, home, manifestPath); err != nil {
		return officialManifest{}, false
	}
	raw, err := env.Read(ctx, manifestPath)
	if err != nil {
		return officialManifest{}, false
	}
	var m officialManifest
	if json.Unmarshal(raw, &m) != nil || m.Owner != officialManifestOwner || m.Version != officialManifestVersion || m.Skill == "" || m.Files["SKILL.md"] == "" {
		return officialManifest{}, false
	}
	return m, true
}

func isManagedSkillUnmodified(ctx context.Context, env envtarget.Env, home, folder string, manifest officialManifest) bool {
	for rel, expected := range manifest.Files {
		if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") || expected == "" {
			return false
		}
		full := path.Join(folder, rel)
		if err := checkOfficialTarget(ctx, env, home, full); err != nil {
			return false
		}
		data, err := env.Read(ctx, full)
		if err != nil || payloadHash(data) != expected {
			return false
		}
	}
	// Ensure no extra user-created top-level entries exist in the folder.
	topEntries, err := env.ListDir(ctx, folder)
	if err != nil {
		return false
	}
	allowedTop := map[string]bool{officialManifestFile: true}
	for rel := range manifest.Files {
		head := rel
		if idx := strings.IndexByte(rel, '/'); idx > 0 {
			head = rel[:idx]
		}
		allowedTop[head] = true
	}
	for _, entry := range topEntries {
		if !allowedTop[entry.Name] {
			return false
		}
	}
	return true
}

func verifyCLIExecutable(ctx context.Context, env envtarget.Env, home, binaryPath, expectedHash string) error {
	if err := checkOfficialTarget(ctx, env, home, binaryPath); err != nil {
		return err
	}
	data, err := env.Read(ctx, binaryPath)
	if err != nil || payloadHash(data) != expectedHash {
		return envtarget.Errorf(422, "CLI 哈希校验失败")
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := env.Run(bounded, envtarget.Command{Name: binaryPath, Args: []string{"--help"}})
	if err != nil || res.Code != 0 {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return envtarget.Errorf(422, "更新后的 CLI 无法正常执行")
	}
	return nil
}
