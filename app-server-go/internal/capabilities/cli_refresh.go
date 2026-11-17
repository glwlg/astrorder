package capabilities

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"

	"astrorder.dev/app-server/internal/capabilities/officialskills"
	"astrorder.dev/app-server/internal/envtarget"
)

type officialCLIRefresh struct {
	folder string
	name   string
	files  map[string][]byte
}

// planOfficialCLIRefresh updates only installed, unmodified official modules
// bound to this same Agent. Unmanaged and user-edited files are never copied.
func planOfficialCLIRefresh(ctx context.Context, dest envtarget.Env, home, agent, bin string, binary, caller []byte) ([]officialCLIRefresh, error) {
	var plans []officialCLIRefresh
	for _, skill := range officialskills.ListOfficialSkills() {
		if !skill.RequiresCLI || skill.Name == "astrorder-sessions" {
			continue
		}
		folder := path.Join(home, "skills", skill.Name)
		manifestPath := path.Join(folder, officialManifestFile)
		if err := checkOfficialTarget(ctx, dest, home, manifestPath); err != nil {
			return nil, err
		}
		raw, err := dest.Read(ctx, manifestPath)
		if errors.Is(err, envtarget.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var manifest officialManifest
		if json.Unmarshal(raw, &manifest) != nil || manifest.Owner != officialManifestOwner ||
			manifest.Version != officialManifestVersion || manifest.Skill != skill.Name ||
			manifest.Agent != agent || manifest.CallerHash != payloadHash(caller) {
			return nil, envtarget.Errorf(409, "目标技能 %s 的身份或清单不匹配，已保留原文件", skill.Name)
		}
		files := make(map[string][]byte, len(manifest.Files))
		for rel, expected := range manifest.Files {
			if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
				return nil, envtarget.Errorf(409, "目标技能 %s 的文件路径无效", skill.Name)
			}
			full := path.Join(folder, rel)
			if err := checkOfficialTarget(ctx, dest, home, full); err != nil {
				return nil, err
			}
			data, err := dest.Read(ctx, full)
			if err != nil || expected == "" || payloadHash(data) != expected {
				return nil, envtarget.Errorf(409, "目标技能 %s 已修改或不完整，已保留原文件", skill.Name)
			}
			files[rel] = data
		}
		relBin := "scripts/" + bin
		if _, ok := files[relBin]; !ok {
			return nil, envtarget.Errorf(409, "目标技能 %s 的客户端与当前平台不匹配", skill.Name)
		}
		if _, ok := files["SKILL.md"]; !ok {
			return nil, envtarget.Errorf(409, "目标技能 %s 缺少技能文件", skill.Name)
		}
		files[relBin] = binary
		plans = append(plans, officialCLIRefresh{folder: folder, name: skill.Name, files: files})
	}
	return plans, nil
}

func applyOfficialCLIRefresh(ctx context.Context, dest envtarget.Env, home, agent, bin string, binary []byte, plans []officialCLIRefresh) error {
	for _, plan := range plans {
		if err := writeOfficialFiles(ctx, dest, plan.folder, agent, plan.name, plan.files, func() error {
			_, _, err := readVerifiedCLI(ctx, dest, home, path.Join(plan.folder, "scripts", bin), bin, payloadHash(binary))
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}
