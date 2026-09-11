package factory_service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"senspace/pkg/app/security"
)

// GenerateReleaseAssetData 执行插件资产生成器。
func GenerateReleaseAssetData(user security.JwtUser, pluginIdRaw string, req GenerateReleaseAssetDataRequest) (*GenerateReleaseAssetDataResponse, error) {
	if user.Id == 0 {
		return nil, newParameterError("用户ID不能为空")
	}
	pluginId := strings.TrimSpace(pluginIdRaw)
	if pluginId == "" {
		return nil, newParameterError("插件ID不能为空")
	}
	generator, err := pluginAssetGenerator(pluginId)
	if err != nil {
		return nil, err
	}

	mode := req.Mode
	if mode == "" {
		mode = GenerateReleaseAssetDataModeTest
	}
	if mode != GenerateReleaseAssetDataModeTest && mode != GenerateReleaseAssetDataModeFormal {
		return nil, newParameterError("未知生成模式")
	}
	if mode == GenerateReleaseAssetDataModeFormal {
		if err := requireReleaseFreezeOperator(user, pluginId); err != nil {
			return nil, err
		}
	}

	generatorDir, err := pluginAssetGeneratorDir(generator)
	if err != nil {
		return nil, err
	}
	if mode == GenerateReleaseAssetDataModeFormal {
		return generateFormalBatch(generatorDir, req.Seed)
	}
	outputDir := filepath.Join(generatorDir, "generated")
	dataDirName := generator.TestDirName

	args := []string{
		filepath.Join("dist", "cli", "generate.js"),
		"--output-dir", outputDir,
		"--fish-dir-name", dataDirName,
		"--report-dir", filepath.Join(outputDir, "test-reports"),
	}
	if mode == GenerateReleaseAssetDataModeTest {
		count := normalizeGenerateCount(req.Count)
		args = append(args, "--limit-per-tier", strconv.Itoa(count))
		if tier := normalizeGenerateTier(req.Tier); tier != "" {
			args = append(args, "--tiers", tier)
		}
	}

	stdout, stderr, err := runAssetGeneratorCommand(generatorDir, args)
	if err != nil {
		message := strings.TrimSpace(stderr)
		if message == "" {
			message = err.Error()
		}
		return nil, newConflictError("生成数据失败：" + message)
	}

	total := parseGeneratorTotal(stdout)
	message := strings.TrimSpace(stdout)
	if message == "" {
		message = "生成数据完成"
	}

	return &GenerateReleaseAssetDataResponse{
		Mode:        mode,
		DataDirName: dataDirName,
		OutputDir:   filepath.ToSlash(filepath.Join("generated", dataDirName)),
		Total:       total,
		Message:     message,
	}, nil
}

func pluginAssetGenerator(pluginId string) (*pluginAssetGeneratorTooling, error) {
	tooling, ok := pluginTooling(pluginId)
	if !ok || tooling.Generator == nil {
		return nil, newParameterError("当前插件未配置资产生成器")
	}
	return tooling.Generator, nil
}

func pluginAssetGeneratorDir(generator *pluginAssetGeneratorTooling) (string, error) {
	for _, candidate := range generator.DirCandidates {
		absCandidate, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(absCandidate); err == nil && info.IsDir() {
			return absCandidate, nil
		}
	}
	return "", newConflictError("资产生成器目录不存在")
}

func runAssetGeneratorCommand(dir string, args []string, extraEnv ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "node", args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return stdout.String(), stderr.String(), fmt.Errorf("生成数据超时")
	}
	return stdout.String(), stderr.String(), err
}

func normalizeGenerateCount(count int) int {
	if count <= 0 {
		return 12
	}
	if count > 60000 {
		return 60000
	}
	return count
}

func normalizeGenerateTier(tier string) string {
	switch strings.TrimSpace(tier) {
	case "common", "rare", "epic", "legendary":
		return strings.TrimSpace(tier)
	default:
		return ""
	}
}

func parseGeneratorTotal(output string) int {
	start := strings.Index(output, "完成：")
	if start < 0 {
		return 0
	}
	rest := strings.TrimSpace(output[start+len("完成："):])
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0
	}
	total, _ := strconv.Atoi(fields[0])
	return total
}
