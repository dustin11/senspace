package factory_service

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"senspace/domain/factory"
	"senspace/pkg/setting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxBuildErrorLength = 2000

// PluginBuildRequest 表示插件构建请求。
type PluginBuildRequest struct {
	PluginId  string
	Version   string
	ReleaseId int64
	SourceDir string
	OutputDir string
	Manifest  PluginManifest
}

// PluginBuildResult 表示插件构建结果。
type PluginBuildResult struct {
	BundleHash           string
	Integrity            string
	BuiltAt              time.Time
	ExternalDependencies []string
	BundledDependencies  []string
}

// PluginBuildExecutor 表示插件构建执行器。
type PluginBuildExecutor interface {
	Build(ctx context.Context, req PluginBuildRequest) (*PluginBuildResult, error)
}

type pluginBuildSettings struct {
	BuilderImage string
	Timeout      time.Duration
	CPU          string
	Memory       string
	PidsLimit    int
	Tmpfs        string
}

type dockerPluginBuildExecutor struct{}

// runtimeManifestFile 表示插件运行产物清单。
type runtimeManifestFile struct {
	PluginId             string                      `json:"pluginId"`
	Version              string                      `json:"version"`
	ReleaseId            string                      `json:"releaseId"`
	BundleHash           string                      `json:"bundleHash"`
	Integrity            string                      `json:"integrity"`
	SandboxEntry         string                      `json:"sandboxEntry"`
	StaticAssets         []string                    `json:"staticAssets,omitempty"`
	ExternalDependencies []runtimeManifestDependency `json:"externalDependencies"`
	BundledDependencies  []runtimeManifestDependency `json:"bundledDependencies"`
}

type runtimeManifestDependency struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Mode    string `json:"mode"`
}

var pluginBuildExecutor PluginBuildExecutor = dockerPluginBuildExecutor{}

// SetPluginBuildExecutorForTest 设置测试用构建执行器。
func SetPluginBuildExecutorForTest(executor PluginBuildExecutor) func() {
	previous := pluginBuildExecutor
	pluginBuildExecutor = executor
	return func() {
		pluginBuildExecutor = previous
	}
}

// 执行插件构建，并在成功后提升为当前主推版本。
func executeReleaseBuild(release factory.Release) (factory.Release, error) {
	tx, err := db()
	if err != nil {
		return release, err
	}

	if err := tx.Model(&factory.Release{}).
		Where("id = ?", release.Id).
		Updates(map[string]interface{}{
			"build_status": factory.BuildStatusBuilding,
			"build_error":  "",
		}).Error; err != nil {
		return release, err
	}
	release.BuildStatus = factory.BuildStatusBuilding
	release.BuildError = ""

	req := PluginBuildRequest{
		PluginId:  release.PluginId,
		Version:   release.Version,
		ReleaseId: release.Id,
		SourceDir: getPluginSourceSnapshotRoot(release.PluginId, release.Id),
		OutputDir: getPluginRuntimeReleaseRoot(release.PluginId, release.Version, release.Id),
		Manifest: PluginManifest{
			Name:        release.ManifestSnapshot.Name,
			Version:     release.ManifestSnapshot.Version,
			Entry:       release.ManifestSnapshot.Entry,
			Description: release.ManifestSnapshot.Description,
		},
	}

	buildCtx, cancel := context.WithTimeout(context.Background(), pluginBuildSettingsFromConfig().Timeout)
	defer cancel()

	result, buildErr := pluginBuildExecutor.Build(buildCtx, req)
	if buildErr != nil {
		message := truncateBuildError(buildErr.Error())
		updateErr := tx.Model(&factory.Release{}).
			Where("id = ?", release.Id).
			Updates(map[string]interface{}{
				"build_status": factory.BuildStatusFailed,
				"build_error":  message,
			}).Error
		if updateErr != nil {
			return release, updateErr
		}
		release.BuildStatus = factory.BuildStatusFailed
		release.BuildError = message
		return release, buildErr
	}

	if result == nil {
		return release, fmt.Errorf("plugin build result is nil")
	}

	err = tx.Transaction(func(tx *gorm.DB) error {
		var fresh factory.Release
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&fresh, "id = ?", release.Id).Error; err != nil {
			return err
		}

		if err := tx.Model(&factory.Release{}).
			Where("plugin_id = ? AND current_release = ?", fresh.PluginId, true).
			Update("current_release", false).Error; err != nil {
			return err
		}

		builtAt := result.BuiltAt
		if builtAt.IsZero() {
			builtAt = time.Now()
		}

		fresh.Status = factory.ReleaseStatusPublished
		fresh.CurrentRelease = true
		fresh.BundleHash = strings.TrimSpace(result.BundleHash)
		fresh.Integrity = strings.TrimSpace(result.Integrity)
		fresh.BuildStatus = factory.BuildStatusReady
		fresh.BuildError = ""
		fresh.BuiltAt = nowPtr(builtAt)
		if fresh.PublishedAt == nil {
			fresh.PublishedAt = nowPtr(builtAt)
		}

		if err := tx.Save(&fresh).Error; err != nil {
			return err
		}
		release = fresh
		return nil
	})
	if err != nil {
		return release, err
	}

	if err := rebuildReleaseStaticSnapshotNow(release); err != nil {
		message := truncateBuildError(err.Error())
		updateErr := tx.Model(&factory.Release{}).
			Where("id = ?", release.Id).
			Updates(map[string]interface{}{
				"build_status": factory.BuildStatusFailed,
				"build_error":  message,
			}).Error
		if updateErr != nil {
			return release, updateErr
		}
		release.BuildStatus = factory.BuildStatusFailed
		release.BuildError = message
		return release, err
	}
	if err := enqueueFactoryReleaseSnapshot(release); err != nil {
		message := truncateBuildError(err.Error())
		updateErr := tx.Model(&factory.Release{}).
			Where("id = ?", release.Id).
			Updates(map[string]interface{}{
				"build_status": factory.BuildStatusFailed,
				"build_error":  message,
			}).Error
		if updateErr != nil {
			return release, updateErr
		}
		release.BuildStatus = factory.BuildStatusFailed
		release.BuildError = message
		return release, err
	}

	tooling, hasTooling := pluginTooling(release.PluginId)
	// 生成器资产必须先预览批次，再通过显式冻结入口绑定；构建不自动选择最新批次。
	if releaseHasFactoryAssetTemplate(release) && (!hasTooling || tooling.Generator == nil) {
		var stagingDir string
		var backupDir string
		activatedSnapshot := false
		if err := tx.Transaction(func(tx *gorm.DB) error {
			_, releaseStagingDir, err := freezeReleaseAssets(tx, release)
			stagingDir = releaseStagingDir
			if err != nil {
				return err
			}
			if stagingDir != "" {
				releaseBackupDir, snapshotActivated, err := activateStagedReleaseSnapshot(release, stagingDir)
				if err != nil {
					return err
				}
				backupDir = releaseBackupDir
				activatedSnapshot = snapshotActivated
				stagingDir = ""
			}
			return nil
		}); err != nil {
			rollbackFreezeStaticSnapshot(release, stagingDir, backupDir, activatedSnapshot)
			message := truncateBuildError(err.Error())
			updateErr := tx.Model(&factory.Release{}).
				Where("id = ?", release.Id).
				Updates(map[string]interface{}{
					"build_status": factory.BuildStatusFailed,
					"build_error":  message,
				}).Error
			if updateErr != nil {
				return release, updateErr
			}
			release.BuildStatus = factory.BuildStatusFailed
			release.BuildError = message
			return release, err
		}
		commitFreezeStaticSnapshot(backupDir, activatedSnapshot)
	}

	return release, nil
}

func releaseHasFactoryAssetTemplate(release factory.Release) bool {
	if hasFactoryAssetTemplateInDir(getPluginSourceSnapshotRoot(release.PluginId, release.Id)) {
		return true
	}
	if hasFactoryAssetTemplateInDir(filepath.Join("asset", "factory-templates", release.PluginId)) {
		return true
	}
	return hasFactoryAssetTemplateInDir(filepath.Join("..", "senspace-web", "src", "components", "StarSky", "Desktop", "Plugins", release.PluginId)) ||
		hasFactoryAssetTemplateInDir(filepath.Join("senspace-web", "src", "components", "StarSky", "Desktop", "Plugins", release.PluginId))
}

func hasFactoryAssetTemplateInDir(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	assetMetaPath := filepath.Join(dir, "asset.meta.json")
	info, err := os.Stat(assetMetaPath)
	if err != nil || info.IsDir() {
		return false
	}
	hasCollections, err := factory.AssetMetaHasCollections(assetMetaPath)
	if err != nil {
		// 模板存在但内容异常时，仍按“存在模板”处理，让后续冻结阶段报出真实错误。
		return true
	}
	return hasCollections
}

func hasFactoryAssetTemplate(pluginId string) bool {
	assetMetaPath := filepath.Join("asset", "factory-templates", pluginId, "asset.meta.json")
	info, err := os.Stat(assetMetaPath)
	if err != nil || info.IsDir() {
		return false
	}
	hasCollections, err := factory.AssetMetaHasCollections(assetMetaPath)
	if err != nil {
		return true
	}
	return hasCollections
}

func (dockerPluginBuildExecutor) Build(ctx context.Context, req PluginBuildRequest) (*PluginBuildResult, error) {
	settings := pluginBuildSettingsFromConfig()
	if settings.BuilderImage == "" {
		return nil, fmt.Errorf("pluginBuilderImage 未配置")
	}
	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return nil, err
	}

	args := []string{
		"run", "--rm",
		"--network=none",
		"--read-only",
		"--tmpfs", settings.Tmpfs,
		"--cpus", settings.CPU,
		"--memory", settings.Memory,
		"--pids-limit", strconv.Itoa(settings.PidsLimit),
		"--security-opt=no-new-privileges:true",
		"--cap-drop=ALL",
		"-u", "1000:1000",
		"-v", req.SourceDir + ":/work/src:ro",
		"-v", req.OutputDir + ":/work/out:rw",
		settings.BuilderImage,
		"--src", "/work/src",
		"--out", "/work/out",
		"--plugin-id", req.PluginId,
		"--version", req.Version,
		"--release-id", strconv.FormatInt(req.ReleaseId, 10),
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("插件构建失败: %s", message)
	}

	result, err := readRuntimeBuildResult(req.OutputDir, req)
	if err != nil {
		return nil, err
	}
	if result.BuiltAt.IsZero() {
		result.BuiltAt = time.Now()
	}
	return result, nil
}

func pluginBuildSettingsFromConfig() pluginBuildSettings {
	timeout := setting.Config.App.PluginBuildTimeoutSec
	if timeout <= 0 {
		timeout = 120
	}
	pidsLimit := setting.Config.App.PluginBuildPidsLimit
	if pidsLimit <= 0 {
		pidsLimit = 128
	}

	settings := pluginBuildSettings{
		BuilderImage: strings.TrimSpace(setting.Config.App.PluginBuilderImage),
		Timeout:      time.Duration(timeout) * time.Second,
		CPU:          strings.TrimSpace(setting.Config.App.PluginBuildCPU),
		Memory:       strings.TrimSpace(setting.Config.App.PluginBuildMemory),
		PidsLimit:    pidsLimit,
		Tmpfs:        strings.TrimSpace(setting.Config.App.PluginBuildTmpfs),
	}
	if settings.CPU == "" {
		settings.CPU = "1.5"
	}
	if settings.Memory == "" {
		settings.Memory = "512m"
	}
	if settings.Tmpfs == "" {
		settings.Tmpfs = "/tmp:rw,noexec,nosuid,size=256m"
	}
	return settings
}

// 冻结源码目录并生成 manifest 快照。
func snapshotPluginSource(versionRoot string, pluginId string, releaseId int64, manifest PluginManifest) (string, error) {
	sourceHash, _, err := buildHashes(versionRoot, manifest)
	if err != nil {
		return "", err
	}

	snapshotRoot := getPluginSourceSnapshotRoot(pluginId, releaseId)
	if err := os.RemoveAll(snapshotRoot); err != nil {
		return "", err
	}
	if err := os.MkdirAll(snapshotRoot, 0o755); err != nil {
		return "", err
	}

	if err := copyDirectory(versionRoot, snapshotRoot); err != nil {
		return "", err
	}

	manifestBytes, err := json.MarshalIndent(toManifestSnapshot(manifest), "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(snapshotRoot, "manifest.snapshot.json"), manifestBytes, 0o644); err != nil {
		return "", err
	}
	return sourceHash, nil
}

// 读取运行产物信息。
func readRuntimeBuildResult(outputDir string, req PluginBuildRequest) (*PluginBuildResult, error) {
	result := &PluginBuildResult{
		BundleHash:           "",
		Integrity:            "",
		BuiltAt:              time.Now(),
		ExternalDependencies: nil,
		BundledDependencies:  nil,
	}
	manifestPath := filepath.Join(outputDir, "runtime-manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("读取运行清单失败: %w", err)
	}

	var manifest runtimeManifestFile
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("解析运行清单失败: %w", err)
	}

	entryPath, err := resolveRuntimeSandboxEntryPath(outputDir, manifest.SandboxEntry)
	if err != nil {
		return nil, err
	}
	entryBytes, err := os.ReadFile(entryPath)
	if err != nil {
		return nil, fmt.Errorf("读取沙箱运行入口失败: %w", err)
	}

	sha256Sum := sha256.Sum256(entryBytes)
	sha384Sum := sha512.Sum384(entryBytes)
	actualBundleHash := "sha256:" + hex.EncodeToString(sha256Sum[:])
	actualIntegrity := "sha384-" + base64.StdEncoding.EncodeToString(sha384Sum[:])

	if err := validateRuntimeManifest(manifest, req, actualBundleHash, actualIntegrity); err != nil {
		return nil, err
	}

	result.BundleHash = actualBundleHash
	result.Integrity = actualIntegrity
	result.ExternalDependencies = dependencyNames(manifest.ExternalDependencies)
	result.BundledDependencies = dependencyNames(manifest.BundledDependencies)

	return result, nil
}

// 解析沙箱运行入口路径，并限制它只能落在当前 release 运行目录内。
func resolveRuntimeSandboxEntryPath(outputDir string, sandboxEntry string) (string, error) {
	normalizedEntry := filepath.Clean(filepath.FromSlash(strings.TrimSpace(sandboxEntry)))
	if normalizedEntry == "." || normalizedEntry == "" {
		return "", fmt.Errorf("运行清单 sandboxEntry 不能为空")
	}
	if filepath.IsAbs(normalizedEntry) || strings.HasPrefix(normalizedEntry, ".."+string(filepath.Separator)) || normalizedEntry == ".." {
		return "", fmt.Errorf("运行清单 sandboxEntry 非法")
	}
	return filepath.Join(outputDir, normalizedEntry), nil
}

func validateRuntimeManifest(
	manifest runtimeManifestFile,
	req PluginBuildRequest,
	actualBundleHash string,
	actualIntegrity string,
) error {
	if strings.TrimSpace(manifest.PluginId) != strings.TrimSpace(req.PluginId) {
		return fmt.Errorf("运行清单 pluginId 不匹配")
	}
	if strings.TrimSpace(manifest.Version) != strings.TrimSpace(req.Version) {
		return fmt.Errorf("运行清单 version 不匹配")
	}
	if strings.TrimSpace(manifest.ReleaseId) != strconv.FormatInt(req.ReleaseId, 10) {
		return fmt.Errorf("运行清单 releaseId 不匹配")
	}
	if strings.TrimSpace(manifest.SandboxEntry) == "" {
		return fmt.Errorf("运行清单 sandboxEntry 不能为空")
	}
	if strings.TrimSpace(manifest.BundleHash) != actualBundleHash {
		return fmt.Errorf("运行清单 bundleHash 校验失败")
	}
	if strings.TrimSpace(manifest.Integrity) != actualIntegrity {
		return fmt.Errorf("运行清单 integrity 校验失败")
	}

	if err := validateRuntimeDependencyList(manifest.ExternalDependencies, "external"); err != nil {
		return err
	}
	if err := validateRuntimeDependencyList(manifest.BundledDependencies, "bundled"); err != nil {
		return err
	}
	if err := validateRuntimeStaticAssetList(manifest.StaticAssets); err != nil {
		return err
	}

	seen := make(map[string]string, len(manifest.ExternalDependencies)+len(manifest.BundledDependencies))
	for _, dependency := range manifest.ExternalDependencies {
		seen[strings.TrimSpace(dependency.Name)] = "external"
	}
	for _, dependency := range manifest.BundledDependencies {
		name := strings.TrimSpace(dependency.Name)
		if previousMode, ok := seen[name]; ok {
			return fmt.Errorf("运行清单依赖重复: %s (%s/%s)", name, previousMode, "bundled")
		}
	}
	return nil
}

// validateRuntimeStaticAssetList 校验静态资源列表只能暴露 `assets/` 目录内的相对路径。
func validateRuntimeStaticAssetList(staticAssets []string) error {
	for _, assetPath := range staticAssets {
		normalizedPath := strings.TrimSpace(filepath.ToSlash(assetPath))
		if normalizedPath == "" {
			return fmt.Errorf("运行清单 staticAssets 不能包含空路径")
		}
		if strings.HasPrefix(normalizedPath, "/") || strings.Contains(normalizedPath, "..") {
			return fmt.Errorf("运行清单 staticAssets 非法: %s", normalizedPath)
		}
		if !strings.HasPrefix(normalizedPath, "assets/") {
			return fmt.Errorf("运行清单 staticAssets 必须使用 assets/ 相对路径: %s", normalizedPath)
		}
	}
	return nil
}

func validateRuntimeDependencyList(
	dependencies []runtimeManifestDependency,
	expectedMode string,
) error {
	for _, dependency := range dependencies {
		if strings.TrimSpace(dependency.Name) == "" {
			return fmt.Errorf("运行清单依赖名称不能为空")
		}
		if strings.TrimSpace(dependency.Mode) != expectedMode {
			return fmt.Errorf("运行清单依赖 %s 模式非法", strings.TrimSpace(dependency.Name))
		}
	}
	return nil
}

func dependencyNames(dependencies []runtimeManifestDependency) []string {
	if len(dependencies) == 0 {
		return nil
	}
	names := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		name := strings.TrimSpace(dependency.Name)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// 获取插件源码快照目录。
func getPluginSourceSnapshotRoot(pluginId string, releaseId int64) string {
	baseRoot := strings.TrimSpace(setting.Config.App.PluginSourceRoot)
	if baseRoot == "" {
		baseRoot = filepath.Join(setting.Config.App.RuntimeRootPath, "plugin-source")
	}
	return filepath.Join(baseRoot, pluginId, strconv.FormatInt(releaseId, 10))
}

// 获取插件运行产物目录。
func getPluginRuntimeReleaseRoot(pluginId string, version string, releaseId int64) string {
	baseRoot := strings.TrimSpace(setting.Config.App.PluginRuntimeRoot)
	if baseRoot == "" {
		baseRoot = filepath.Join(setting.Config.App.RuntimeRootPath, "plugin-runtime")
	}
	return filepath.Join(baseRoot, pluginId, "v"+version+"-"+strconv.FormatInt(releaseId, 10))
}

func copyDirectory(src string, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src string, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

func truncateBuildError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= maxBuildErrorLength {
		return message
	}
	return message[:maxBuildErrorLength]
}
