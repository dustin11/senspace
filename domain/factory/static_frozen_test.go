package factory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"senspace/pkg/setting"
)

// 后台重建和同目录重建均保留已冻结数据、生成程序及证明根。
func TestFrozenSnapshotSurvivesRebuild(t *testing.T) {
	original := *setting.Config
	t.Cleanup(func() { *setting.Config = original })
	setting.Config.App.FilePath.Factory = t.TempDir()
	setting.Config.App.PluginSourceRoot = t.TempDir()
	release := Release{Id: 990002, PluginId: "FrozenFixture", Version: "1"}
	source := filepath.Join(setting.Config.App.PluginSourceRoot, "FrozenFixture", "990002")
	require.NoError(t, os.MkdirAll(source, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "asset.meta.json"), []byte(`{"collections":[{"key":"fish","metadataRef":"fish.json"}]}`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(source, "fish.json"), []byte(`[{"id":"original"}]`), 0644))
	require.NoError(t, EnsureReleaseStaticSnapshot(release))
	final := ReleaseStaticDir(release)
	require.NoError(t, WriteJSONAtomic(filepath.Join(final, "inventory.json"), map[string]string{"root": "frozen-root"}))
	require.NoError(t, os.MkdirAll(filepath.Join(final, "generator-batch"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(final, "generator-batch", "program.js"), []byte("original-program"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(source, "fish.json"), []byte(`[{"id":"changed"}]`), 0644))
	require.NoError(t, EnsureReleaseStaticSnapshot(release))
	stage, err := StageReleaseStaticSnapshot(release)
	require.NoError(t, err)
	for _, root := range []string{final, stage} {
		data, err := os.ReadFile(filepath.Join(root, "fish.json"))
		require.NoError(t, err)
		require.JSONEq(t, `[{"id":"original"}]`, string(data))
		program, err := os.ReadFile(filepath.Join(root, "generator-batch", "program.js"))
		require.NoError(t, err)
		require.Equal(t, "original-program", string(program))
	}
	// 显式冻结可选择新来源，后台任务不能。
	stage, err = StageReleaseStaticSnapshot(release, "")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(stage, "fish.json"))
	require.NoError(t, err)
	require.JSONEq(t, `[{"id":"changed"}]`, string(data))
}
