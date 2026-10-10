package factory_service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// 纯文件解析测试，不连接数据库或修改业务数据。
func TestMinimalManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	input := PluginManifest{Name: "Parcel", Version: "1.0.0", Entry: "src/index.ts"}
	data, err := json.Marshal(input)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644))
	manifest, err := loadManifestFromDir(dir)
	require.NoError(t, err)
	require.Equal(t, input, manifest)
	snapshot, err := json.Marshal(toManifestSnapshot(manifest))
	require.NoError(t, err)
	require.JSONEq(t, string(data), string(snapshot))
	other := manifest
	other.Entry = "src/other.ts"
	require.Error(t, compareManifest(manifest, other))
}

func TestMintTierIsMetadataNotMutableOptions(t *testing.T) {
	for _, tier := range []string{"common", "rare", "epic", "legendary"} {
		options, err := normalizePluginOptions(map[string]any{"tier": tier, "color": "green"})
		require.NoError(t, err)
		require.Equal(t, tier, options.Tier)
		require.JSONEq(t, `{"color":"green"}`, options.Raw)
		require.NotContains(t, options.Data, "tier")
	}
	for _, value := range []any{"classic", "premium", "", nil, 1} {
		_, err := normalizePluginOptions(map[string]any{"tier": value})
		require.Error(t, err)
	}
	options, err := normalizePluginOptions(map[string]any{"tier": "rare"})
	require.NoError(t, err)
	require.Equal(t, "rare", options.Tier)
	require.Equal(t, "{}", options.Raw)
}

func TestRuntimeDisplayArtifactsValidation(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`{"version":1}`)
	digest := sha256.Sum256(content)
	hash := hex.EncodeToString(digest[:])
	artifact := runtimeDisplayArtifact{URL: "display/display-" + hash + ".json", SHA256: hash, Bytes: int64(len(content))}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "display"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(artifact.URL)), content, 0o644))
	catalog := map[string]runtimeDisplayArtifact{hash: artifact}
	require.NoError(t, validateRuntimeDisplayArtifacts(dir, catalog))
	require.NoError(t, validateRuntimeDisplayArtifacts(dir, nil))
	require.Error(t, validateRuntimeDisplayArtifacts(dir, map[string]runtimeDisplayArtifact{}))
	for _, invalid := range []runtimeDisplayArtifact{
		{URL: "../" + artifact.URL, SHA256: hash, Bytes: artifact.Bytes},
		{URL: artifact.URL, SHA256: "bad", Bytes: artifact.Bytes},
		{URL: artifact.URL, SHA256: hash, Bytes: artifact.Bytes + 1},
	} {
		require.Error(t, validateRuntimeDisplayArtifacts(dir, map[string]runtimeDisplayArtifact{hash: invalid}))
	}
	require.Error(t, validateRuntimeDisplayArtifacts(dir, map[string]runtimeDisplayArtifact{"bad": artifact}))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(artifact.URL)), []byte(`{"version":2}`), 0o644))
	require.ErrorContains(t, validateRuntimeDisplayArtifacts(dir, catalog), "摘要校验失败")
}
