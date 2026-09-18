package factory_service

import (
	"os"
	"path/filepath"
	"testing"

	"senspace/domain/factory"
	"senspace/pkg/merkle"

	"github.com/stretchr/testify/require"
)

// 根数组、对象字段与发布目录各自隔离，同一构建不重复解析模板。
func TestSnapshotTemplateItemsReuse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fish.json")
	require.NoError(t, factory.WriteJSONAtomic(path, []map[string]any{
		{"id": "one", "paletteId": "blue"}, {"id": "two", "paletteId": "green"},
	}))
	cache := make(snapshotTemplateItems)
	one, err := cache.find(dir, "fish.json", "one")
	require.NoError(t, err)
	require.Equal(t, "blue", one["paletteId"])
	require.NoError(t, os.Remove(path))
	two, err := cache.find(dir, "./fish.json", "two")
	require.NoError(t, err)
	require.Equal(t, "green", two["paletteId"])
	require.Len(t, cache, 1)
	_, err = cache.find(dir, "fish.json", "missing")
	require.ErrorContains(t, err, "缺少原始参数")

	require.NoError(t, factory.WriteJSONAtomic(path, []map[string]any{{"id": "one", "paletteId": "red"}}))
	fresh, err := make(snapshotTemplateItems).find(dir, "fish.json", "one")
	require.NoError(t, err)
	require.Equal(t, "red", fresh["paletteId"])
	require.Equal(t, "blue", one["paletteId"])

	other := t.TempDir()
	require.NoError(t, factory.WriteJSONAtomic(filepath.Join(other, "fish.json"), []map[string]any{{"id": "one", "paletteId": "yellow"}}))
	otherItem, err := cache.find(other, "fish.json", "one")
	require.NoError(t, err)
	require.Equal(t, "yellow", otherItem["paletteId"])

	require.NoError(t, factory.WriteJSONAtomic(filepath.Join(dir, "parts.json"), map[string]any{
		"fish":  []map[string]any{{"id": "one", "paletteId": "cyan"}},
		"tanks": []map[string]any{{"id": "one", "paletteId": "white"}},
	}))
	for ref, color := range map[string]string{"parts.json#fish": "cyan", "parts.json#tanks": "white"} {
		item, err := cache.find(dir, ref, "one")
		require.NoError(t, err)
		require.Equal(t, color, item["paletteId"])
	}
	metadata := buildNFTMetadata(factory.Asset{ItemId: "one"}, mintedFactoryAsset{}, one)
	require.Contains(t, metadata.Attributes, nftMetadataAttribute{TraitType: "Palette", Value: "blue"})
}

// 缺失、损坏和非法路径必须报错，不缓存失败或静默跳过。
func TestSnapshotTemplateItemsErrors(t *testing.T) {
	dir := t.TempDir()
	cache := make(snapshotTemplateItems)
	for _, ref := range []string{"missing.json", "../escape.json", "/absolute.json", "fish.json#"} {
		_, err := cache.find(dir, ref, "one")
		require.Error(t, err, ref)
	}
	path := filepath.Join(dir, "fish.json")
	require.NoError(t, os.WriteFile(path, []byte("<!doctype html>"), 0644))
	_, err := cache.find(dir, "fish.json", "one")
	require.Error(t, err)
	require.Empty(t, cache)
	require.NoError(t, factory.WriteJSONAtomic(path, []map[string]any{{"id": "one"}, {"id": "one", "duplicate": true}}))
	item, err := cache.find(dir, "fish.json", "one")
	require.NoError(t, err)
	require.NotContains(t, item, "duplicate")
}

// 缓存只复用参数，不跳过承诺校验；下一次构建必须发现冻结文件的篡改。
func TestSnapshotTemplateItemsInventoryProof(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fish.json")
	require.NoError(t, factory.WriteJSONAtomic(path, []map[string]any{{"id": "one", "color": "blue"}}))
	cache := make(snapshotTemplateItems)
	parameters, err := cache.find(dir, "fish.json", "one")
	require.NoError(t, err)
	release := factory.Release{Id: 1, PluginId: "Fixture", Version: "1"}
	collection := assetValueCollection{Key: "fish", AssetKind: factory.AssetKindComponent}
	items, err := normalizeInventoryMetadataItems(release, collection, "fish.json", []map[string]any{parameters}, "")
	require.NoError(t, err)
	item := items[0]
	inventory := factory.NFTInventoryItem{CollectionKey: "fish", ItemId: "one", ItemIndex: item.ItemIndex,
		MetadataHash: item.MetadataHash, LeafHash: item.LeafHash, ProofJson: "[]"}
	root, _, err := merkle.Build([]string{item.LeafHash})
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		parameters, err := cache.find(dir, "fish.json", "one")
		require.NoError(t, err)
		proof, err := verifyInventoryProof(release, collection, "fish.json", parameters, inventory, root, "token", "token-hash")
		require.NoError(t, err)
		require.Equal(t, inventory.MetadataHash, sha256Hex([]byte(proof.MetadataCanonical)))
	}
	require.NoError(t, factory.WriteJSONAtomic(path, []map[string]any{{"id": "one", "color": "red"}}))
	changed, err := make(snapshotTemplateItems).find(dir, "fish.json", "one")
	require.NoError(t, err)
	_, err = verifyInventoryProof(release, collection, "fish.json", changed, inventory, root, "token", "token-hash")
	require.ErrorContains(t, err, "参数与库存承诺不一致")
}

// 显式提供冻结目录和持有人索引，只读对比真实资产的旧解码流程与单次构建缓存。
func BenchmarkSnapshotTemplateItems(b *testing.B) {
	dir := os.Getenv("FACTORY_SNAPSHOT_BENCHMARK_DIR")
	indexPath := os.Getenv("FACTORY_SNAPSHOT_BENCHMARK_INDEX")
	if dir == "" || indexPath == "" {
		b.Skip("设置 FACTORY_SNAPSHOT_BENCHMARK_DIR 和 FACTORY_SNAPSHOT_BENCHMARK_INDEX 执行只读性能对比")
	}
	var index ownerFactoryAssetIndex
	require.NoError(b, readJSONFile(indexPath, &index))
	require.NotEmpty(b, index.Assets)
	for _, cached := range []bool{false, true} {
		name := "uncached"
		if cached {
			name = "cached"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				cache := make(snapshotTemplateItems)
				for _, asset := range index.Assets {
					if asset.TemplateRef == "" {
						continue
					}
					if cached {
						item, err := cache.find(dir, asset.TemplateRef, asset.ItemId)
						require.NoError(b, err)
						require.NotNil(b, item)
					} else {
						items, err := loadMetadataRefItems(dir, asset.TemplateRef)
						require.NoError(b, err)
						require.NotNil(b, findTemplateItemById(items, asset.ItemId))
					}
				}
			}
		})
	}
}
