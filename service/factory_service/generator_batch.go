package factory_service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"senspace/domain/factory"
)

// 正式批次包含实际输出及重现该输出所需的配置、生成器和渲染源码指纹。
type generatorBatchManifest struct {
	// 批次协议版本。
	Schema string `json:"schema"`
	// 唯一批次编号。
	BatchID string `json:"batchId"`
	// 实际生成种子。
	Seed string `json:"seed"`
	// 完成时间。
	CreatedAt string `json:"createdAt"`
	// 实际条目数。
	Total int `json:"total"`
	// 执行生成器的 Node 版本。
	NodeVersion string `json:"nodeVersion"`
	// 相对路径与完整文件字节哈希。
	Files map[string]string `json:"files"`
}

// 当前已验收生成批次的原子指针。
type generatorBatchPointer struct {
	// 当前正式批次。
	BatchID string `json:"batchId"`
	// 批次清单字节哈希。
	ManifestHash string `json:"manifestHash"`
}

// 批次编号不允许路径分隔符。
var batchIDPattern = regexp.MustCompile(`^batch-[a-zA-Z0-9-]+$`)

// 文件锁同时约束不同服务进程的正式生成与冻结；异常退出遗留锁需要人工确认后清理。
func lockGeneratorBatch(dir string) (func() error, error) {
	root := filepath.Join(dir, "generated")
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	lock := filepath.Join(root, ".formal-lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		if os.IsExist(err) {
			return nil, newConflictError("正式生成或冻结正在进行；若服务曾异常退出，请确认任务已停止后清理 .formal-lock")
		}
		return nil, err
	}
	return func() error { return os.Remove(lock) }, nil
}

// 在独立目录生成成功后才切换批次指针，不覆盖既有正式文件。
func generateFormalBatch(dir string, seed string) (response *GenerateReleaseAssetDataResponse, resultErr error) {
	unlock, err := lockGeneratorBatch(dir)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, unlock()) }()
	// 进度文件不进入批次封存；正式锁确保同一时刻只有一个写入任务。
	progressPath := filepath.Join(dir, "generated", "formal-progress.json")
	runID := time.Now().UTC().Format(time.RFC3339Nano)
	reportProgress := func(message string) error {
		return factory.WriteJSONAtomic(progressPath, map[string]string{"runId": runID, "message": message})
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, reportProgress("生成失败："+resultErr.Error()))
		}
	}()
	if err := reportProgress("正在编译当前生成器"); err != nil {
		return nil, err
	}

	// 编译当前源码，避免服务继续执行旧 dist。
	webRoot := dir
	for {
		if _, err := os.Stat(filepath.Join(webRoot, "node_modules", "typescript", "bin", "tsc")); err == nil {
			break
		}
		parent := filepath.Dir(webRoot)
		if parent == webRoot {
			return nil, newConflictError("缺少 TypeScript 编译器，请安装生成器构建依赖")
		}
		webRoot = parent
	}
	if stdout, stderr, err := runAssetGeneratorCommand(dir, []string{filepath.Join(webRoot, "node_modules", "typescript", "bin", "tsc"), "-p", filepath.Join(dir, "tsconfig.json")}); err != nil {
		return nil, fmt.Errorf("构建生成器失败：%s %s: %w", stdout, stderr, err)
	}
	if err := reportProgress("编译完成，正在准备独立批次与源码快照"); err != nil {
		return nil, err
	}
	batches := filepath.Join(dir, "generated", "batches")
	if err := os.MkdirAll(batches, 0755); err != nil {
		return nil, err
	}
	batchDir, err := os.MkdirTemp(batches, "batch-"+time.Now().UTC().Format("20060102T150405")+"-")
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			resultErr = errors.Join(resultErr, os.RemoveAll(batchDir))
		}
	}()
	for _, name := range []string{"config", "dist", "src"} {
		if err := copyDirectory(filepath.Join(dir, name), filepath.Join(batchDir, name)); err != nil {
			return nil, err
		}
	}
	if err := copyDirectory(filepath.Join(dir, "..", "fish"), filepath.Join(batchDir, "renderer")); err != nil {
		return nil, err
	}
	args := []string{filepath.Join(batchDir, "dist", "cli", "generate.js"), "--output-dir", filepath.Join(batchDir, "generated"), "--report-dir", filepath.Join(batchDir, "reports")}
	if strings.TrimSpace(seed) != "" {
		args = append(args, "--seed", strings.TrimSpace(seed))
	}
	stdout, stderr, err := runAssetGeneratorCommand(batchDir, args, "FISH_GENERATOR_PROGRESS_PATH="+progressPath, "FISH_GENERATOR_RUN_ID="+runID)
	if err != nil {
		return nil, fmt.Errorf("生成正式批次失败：%s: %w", stderr, err)
	}
	if err := reportProgress("数据已写入，正在独立复核四等级文件与全量文件"); err != nil {
		return nil, err
	}
	if err := auditBatchFiles(batchDir, filepath.Join(batchDir, "generated"), filepath.Join(batchDir, "reports")); err != nil {
		return nil, err
	}
	var summary struct {
		Seed string `json:"seed"`
	}
	if err := readJSONFile(filepath.Join(batchDir, "generated", "summary.json"), &summary); err != nil {
		return nil, err
	}
	manifest := generatorBatchManifest{Schema: "senspace.generator-batch.v1", BatchID: filepath.Base(batchDir), Seed: summary.Seed, CreatedAt: time.Now().UTC().Format(time.RFC3339), Total: parseGeneratorTotal(stdout)}
	version, _, err := runAssetGeneratorCommand(dir, []string{"--version"})
	if err != nil {
		return nil, err
	}
	manifest.NodeVersion = strings.TrimSpace(version)
	if err := reportProgress("独立复核通过，正在计算文件哈希并归档批次"); err != nil {
		return nil, err
	}
	manifest.Files, err = hashBatchFiles(batchDir)
	if err != nil {
		return nil, err
	}
	if err := factory.WriteJSONAtomic(filepath.Join(batchDir, "batch.json"), manifest); err != nil {
		return nil, err
	}
	hash, err := hashBatchFile(filepath.Join(batchDir, "batch.json"))
	if err != nil {
		return nil, err
	}
	if err := factory.WriteJSONAtomic(filepath.Join(dir, "generated", "current-batch.json"), generatorBatchPointer{BatchID: manifest.BatchID, ManifestHash: hash}); err != nil {
		return nil, err
	}
	committed = true
	if err := reportProgress("正式批次完成，正在载入预览"); err != nil {
		return nil, err
	}
	return &GenerateReleaseAssetDataResponse{Mode: GenerateReleaseAssetDataModeFormal, BatchID: manifest.BatchID, Seed: summary.Seed, DataDirName: "batches/" + manifest.BatchID + "/generated/fish", OutputDir: "generated/batches/" + manifest.BatchID + "/generated/fish", Total: manifest.Total, Message: strings.TrimSpace(stdout) + "；批次 " + manifest.BatchID}, nil
}

// 校验将要封存的文件，而不是只相信历史审核报告。
func auditBatchFiles(batchDir string, generatedDir string, reportDir string) error {
	var err error
	batchDir, err = filepath.Abs(batchDir)
	if err != nil {
		return err
	}
	generatedDir, err = filepath.Abs(generatedDir)
	if err != nil {
		return err
	}
	reportDir, err = filepath.Abs(reportDir)
	if err != nil {
		return err
	}
	_, stderr, err := runAssetGeneratorCommand(batchDir, []string{filepath.Join(batchDir, "dist", "cli", "validateGenerated.js"), "--config-dir", filepath.Join(batchDir, "config"), "--output-dir", generatedDir, "--report-dir", reportDir})
	if err != nil {
		return fmt.Errorf("正式批次审计失败：%s: %w", stderr, err)
	}
	return nil
}

// 读取唯一批次指针，并校验全部封存输入未被替换。
func readCurrentGeneratorBatch(dir string) (string, generatorBatchPointer, error) {
	var pointer generatorBatchPointer
	if err := readJSONFile(filepath.Join(dir, "generated", "current-batch.json"), &pointer); err != nil {
		return "", pointer, fmt.Errorf("请先生成正式批次：%w", err)
	}
	if !batchIDPattern.MatchString(pointer.BatchID) {
		return "", pointer, newConflictError("正式批次编号非法")
	}
	root := filepath.Join(dir, "generated", "batches", pointer.BatchID)
	hash, err := hashBatchFile(filepath.Join(root, "batch.json"))
	if err != nil {
		return "", pointer, err
	}
	if hash != pointer.ManifestHash {
		return "", pointer, newConflictError("正式批次清单哈希不匹配")
	}
	var manifest generatorBatchManifest
	if err := readJSONFile(filepath.Join(root, "batch.json"), &manifest); err != nil {
		return "", pointer, err
	}
	if manifest.Schema != "senspace.generator-batch.v1" || manifest.BatchID != pointer.BatchID || len(manifest.Files) == 0 {
		return "", pointer, newConflictError("正式批次清单非法")
	}
	files, err := hashBatchFiles(root)
	if err != nil {
		return "", pointer, err
	}
	if len(files) != len(manifest.Files) {
		return "", pointer, newConflictError("正式批次文件数量发生变化")
	}
	for file, expected := range manifest.Files {
		if files[file] != expected {
			return "", pointer, newConflictError("正式批次文件已变化：" + file)
		}
	}
	return root, pointer, nil
}

// 计算完整批次文件的 SHA-256；拒绝符号链接，清单哈希单独写入指针。
func hashBatchFiles(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(file string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("批次不允许特殊文件：%s", file)
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		if rel == "batch.json" {
			return nil
		}
		hash, err := hashBatchFile(file)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = hash
		return nil
	})
	return files, err
}

// 文件字节哈希用于封存完整参数和程序，不依赖业务 traitHash。
func hashBatchFile(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	if err := errors.Join(copyErr, f.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// 将已校验批次绑定到发布快照，价格模板仍使用该发布的标准来源。
func stageReleaseGeneratorBatch(release factory.Release, expectedBatch ...string) (stage string, resultErr error) {
	tooling, ok := pluginTooling(release.PluginId)
	if !ok || tooling.Generator == nil {
		return factory.StageReleaseStaticSnapshot(release, "")
	}
	dir, err := pluginAssetGeneratorDir(tooling.Generator)
	if err != nil {
		return "", err
	}
	unlock, err := lockGeneratorBatch(dir)
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, unlock()) }()
	batch, pointer, err := readCurrentGeneratorBatch(dir)
	if err != nil {
		return "", err
	}
	if len(expectedBatch) > 0 && expectedBatch[0] != pointer.BatchID {
		return "", newConflictError("正式批次已变化，请重新预览后冻结")
	}
	stage, err = factory.StageReleaseStaticSnapshot(release, filepath.Join(batch, "generated"))
	if err != nil {
		return stage, err
	}
	// all.json 也参与复核，避免全量文件和等级文件出现两套结果。
	for _, rel := range []string{"generated/fish/all.json", "generated/summary.json"} {
		if err := copyFile(filepath.Join(batch, rel), filepath.Join(stage, rel), 0644); err != nil {
			return stage, err
		}
	}
	for _, name := range []string{"config", "dist", "src", "renderer", "reports"} {
		if err := copyDirectory(filepath.Join(batch, name), filepath.Join(stage, "generator-batch", name)); err != nil {
			return stage, err
		}
	}
	if err := copyFile(filepath.Join(batch, "batch.json"), filepath.Join(stage, "generator-batch", "batch.json"), 0644); err != nil {
		return stage, err
	}
	var manifest generatorBatchManifest
	if err := readJSONFile(filepath.Join(stage, "generator-batch", "batch.json"), &manifest); err != nil {
		return stage, err
	}
	for file, expected := range manifest.Files {
		root := filepath.Join(stage, "generator-batch")
		if strings.HasPrefix(file, "generated/") {
			root = stage
		}
		actual, err := hashBatchFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return stage, err
		}
		if actual != expected {
			return stage, newConflictError("封存副本与批次不一致：" + file)
		}
	}
	if err := auditBatchFiles(filepath.Join(stage, "generator-batch"), filepath.Join(stage, "generated"), filepath.Join(stage, "generator-batch", "freeze-audit")); err != nil {
		return stage, err
	}
	seal := map[string]any{"schema": "senspace.release-batch.v1", "releaseId": fmt.Sprint(release.Id), "version": release.Version, "sourceHash": release.SourceHash, "bundleHash": release.BundleHash, "pluginId": release.PluginId, "batchId": pointer.BatchID, "manifestHash": pointer.ManifestHash}
	return stage, factory.WriteJSONAtomic(filepath.Join(stage, "generator-batch", "seal.json"), seal)
}
