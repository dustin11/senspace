package factory_service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"senspace/domain/factory"
	"senspace/pkg/app/security"
	"senspace/pkg/merkle"
	"senspace/pkg/setting"

	"github.com/stretchr/testify/require"
)

// 后端启动的实际 Node 进程使用全量预算，不依赖服务环境的默认堆上限。
func TestGeneratorCommandHeapBudget(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "--max-old-space-size=128")
	stdout, stderr, err := runAssetGeneratorCommand(t.TempDir(), []string{"-e", "process.stdout.write(String(require('v8').getHeapStatistics().heap_size_limit))"})
	require.NoError(t, err, stderr)
	limit, err := strconv.ParseInt(strings.TrimSpace(stdout), 10, 64)
	require.NoError(t, err)
	require.GreaterOrEqual(t, limit, int64(6144)*1024*1024)
	require.Less(t, limit, int64(6400)*1024*1024)
}

// 模拟 Node 的失败输出，不实际耗尽测试机内存；普通脚本错误继续保留 stderr。
func TestGeneratorCommandFailure(t *testing.T) {
	_, stderr, err := runAssetGeneratorCommand(t.TempDir(), []string{"-e", "process.stderr.write('FATAL ERROR: JavaScript heap out of memory'); process.exit(1)"})
	require.Contains(t, stderr, "heap out of memory")
	var serviceErr *ServiceError
	require.ErrorAs(t, err, &serviceErr)
	require.Equal(t, ErrorKindConflict, serviceErr.Kind)
	require.Contains(t, serviceErr.Message, "内存不足")
	_, stderr, err = runAssetGeneratorCommand(t.TempDir(), []string{"-e", "process.stderr.write('invalid fixture'); process.exit(2)"})
	require.Error(t, err)
	require.Equal(t, "invalid fixture", stderr)
}

// 不连接数据库，校验正式接口权限、跨进程锁和批次文件完整性。
func TestGeneratorBatchGuards(t *testing.T) {
	_, err := GenerateReleaseAssetData(security.JwtUser{Id: 1, Addr: "unauthorized"}, "FishTank", GenerateReleaseAssetDataRequest{Mode: GenerateReleaseAssetDataModeFormal})
	require.ErrorContains(t, err, "无权限")
	dir := t.TempDir()
	unlock, err := lockGeneratorBatch(dir)
	require.NoError(t, err)
	_, err = lockGeneratorBatch(dir)
	require.ErrorContains(t, err, "正在进行")
	require.NoError(t, unlock())
	_, _, err = readCurrentGeneratorBatch(dir)
	require.ErrorContains(t, err, "先生成正式批次")
	root := filepath.Join(dir, "generated", "batches", "batch-test")
	require.NoError(t, os.MkdirAll(root, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "data.json"), []byte("{}"), 0644))
	files, err := hashBatchFiles(root)
	require.NoError(t, err)
	require.NoError(t, factory.WriteJSONAtomic(filepath.Join(root, "batch.json"), generatorBatchManifest{Schema: "senspace.generator-batch.v1", BatchID: "batch-test", Files: files}))
	hash, err := hashBatchFile(filepath.Join(root, "batch.json"))
	require.NoError(t, err)
	require.NoError(t, factory.WriteJSONAtomic(filepath.Join(dir, "generated", "current-batch.json"), generatorBatchPointer{BatchID: "batch-test", ManifestHash: hash}))
	actual, _, err := readCurrentGeneratorBatch(dir)
	require.NoError(t, err)
	require.Equal(t, root, actual)
	require.NoError(t, os.WriteFile(filepath.Join(root, "data.json"), []byte("{\"changed\":true}"), 0644))
	_, _, err = readCurrentGeneratorBatch(dir)
	require.ErrorContains(t, err, "文件已变化")
	require.NoError(t, factory.WriteJSONAtomic(filepath.Join(dir, "generated", "current-batch.json"), generatorBatchPointer{BatchID: "../escape"}))
	_, _, err = readCurrentGeneratorBatch(dir)
	require.ErrorContains(t, err, "编号非法")
}

// 冻结承诺必须包含完整纹理和变形参数，而不是只检查提供的 traitHash。
func TestInventoryCommitmentIncludesAllParameters(t *testing.T) {
	release := factory.Release{Id: 1, PluginId: "Fixture", Version: "1"}
	collection := assetValueCollection{Key: "fish", AssetKind: factory.AssetKindComponent, ComponentRole: factory.ComponentRoleChild, ParentKey: "tank", TraitHashField: "traitHash"}
	item := map[string]any{"id": "fish-1", "traitHash": "same", "bodyMorph": []float64{1, 1, 1}, "patternLayers": []any{map[string]any{"pattern": "spots", "placement": []float64{0.5, 1, 1}}}}
	first, err := normalizeInventoryMetadataItems(release, collection, "fish.json", []map[string]any{item}, "")
	require.NoError(t, err)
	item["bodyMorph"] = []float64{1.1, 1, 1}
	changed, err := normalizeInventoryMetadataItems(release, collection, "fish.json", []map[string]any{item}, "")
	require.NoError(t, err)
	require.NotEqual(t, first[0].MetadataHash, changed[0].MetadataHash)
	root, proofs, err := merkle.Build([]string{first[0].LeafHash, changed[0].LeafHash})
	require.NoError(t, err)
	require.True(t, merkle.Verify(first[0].LeafHash, root, proofs[0]))
	require.False(t, merkle.Verify(sha256Hex([]byte("changed")), root, proofs[0]))
	proofJSON, err := json.Marshal(proofs[1])
	require.NoError(t, err)
	inventory := factory.NFTInventoryItem{CollectionKey: collection.Key, ItemId: changed[0].ItemId, ItemIndex: changed[0].ItemIndex, TraitHash: changed[0].TraitHash, MetadataHash: changed[0].MetadataHash, LeafHash: changed[0].LeafHash, ProofJson: string(proofJSON)}
	proof, err := verifyInventoryProof(release, collection, "fish.json", item, inventory, root, "token", "token-hash")
	require.NoError(t, err)
	require.Equal(t, changed[0].MetadataHash, sha256Hex([]byte(proof.MetadataCanonical)))
	require.True(t, merkle.Verify(proof.Leaf, proof.MerkleRoot, proof.Proof))
	item["bodyMorph"] = []float64{1.2, 1, 1}
	_, err = verifyInventoryProof(release, collection, "fish.json", item, inventory, root, "token", "token-hash")
	require.ErrorContains(t, err, "参数与库存承诺不一致")
	inventory.ProofJson = "[]"
	_, err = verifyInventoryProof(release, collection, "fish.json", item, inventory, root, "token", "token-hash")
	require.ErrorContains(t, err, "Merkle 证明不匹配")
}

// 价格只影响交易，名称和批次承诺变化则必须重建库存。
func TestInventorySourceSignatureBoundaries(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, factory.WriteJSONAtomic(filepath.Join(root, "items.json"), []map[string]any{{"id": "one"}}))
	collection := assetValueCollection{Key: "fish", Label: "原名", AssetKind: factory.AssetKindComponent, ComponentRole: factory.ComponentRoleRoot, MetadataRef: "items.json", UnitPrice: "5"}
	template := assetValueTemplate{Collections: []assetValueCollection{collection}}
	before, err := collectReleaseInventorySourceSignatures(template, root)
	require.NoError(t, err)
	template.Collections[0].UnitPrice = "10"
	priced, err := collectReleaseInventorySourceSignatures(template, root)
	require.NoError(t, err)
	require.Equal(t, before, priced)
	template.Collections[0].Label = "改名"
	renamed, err := collectReleaseInventorySourceSignatures(template, root)
	require.NoError(t, err)
	require.NotEqual(t, before, renamed)
	require.NoError(t, factory.WriteJSONAtomic(filepath.Join(root, "generator-batch", "seal.json"), map[string]string{"batchId": "batch-new"}))
	sealed, err := collectReleaseInventorySourceSignatures(template, root)
	require.NoError(t, err)
	changed, keys := collectionSignatureChanged(renamed, sealed)
	require.True(t, changed)
	require.Contains(t, keys, "generator-batch")
}

// 显式启用的文件系统集成测试：临时副本完整生成、审计、绑定和封存，不写真实库存。
func TestFormalBatchFullCollection(t *testing.T) {
	if os.Getenv("FISH_BATCH_INTEGRATION") != "1" {
		t.Skip("设置 FISH_BATCH_INTEGRATION=1 执行六万条文件系统集成测试")
	}
	web, err := filepath.Abs("../../../senspace-web")
	require.NoError(t, err)
	plugin := filepath.Join(web, "src/components/StarSky/Desktop/Plugins/FishTank")
	workspace := t.TempDir()
	fixture := filepath.Join(workspace, "Fixture")
	dir := filepath.Join(fixture, "fish-generator")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.Symlink(filepath.Join(web, "node_modules"), filepath.Join(workspace, "node_modules")))
	for _, name := range []string{"config", "src"} {
		require.NoError(t, copyDirectory(filepath.Join(plugin, "fish-generator", name), filepath.Join(dir, name)))
	}
	require.NoError(t, copyFile(filepath.Join(plugin, "fish-generator", "tsconfig.json"), filepath.Join(dir, "tsconfig.json"), 0644))
	require.NoError(t, copyDirectory(filepath.Join(plugin, "fish"), filepath.Join(fixture, "fish")))
	response, err := generateFormalBatch(dir, "formal-batch-regression")
	require.NoError(t, err)
	require.Equal(t, 60000, response.Total)
	require.Equal(t, "formal-batch-regression", response.Seed)
	batch, pointer, err := readCurrentGeneratorBatch(dir)
	require.NoError(t, err)
	require.Equal(t, response.BatchID, pointer.BatchID)
	_, err = os.Stat(filepath.Join(dir, "generated", "fish"))
	require.True(t, os.IsNotExist(err), "正式生成不得覆盖旧目录")

	original := *setting.Config
	t.Cleanup(func() { *setting.Config = original; delete(pluginFactoryToolingRegistry, "Fixture") })
	setting.Config.App.FilePath.Factory = filepath.Join(workspace, "factory")
	setting.Config.App.PluginSourceRoot = workspace
	pluginFactoryToolingRegistry["Fixture"] = pluginFactoryTooling{Generator: &pluginAssetGeneratorTooling{DirCandidates: []string{dir}}}
	// 发布模板指向实际生成的四等级文件；额外数据无需硬编码鱼的规则。
	var template map[string]any
	data, err := os.ReadFile(filepath.Join(plugin, "asset.meta.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &template))
	template["pluginId"] = "Fixture"
	collections := template["collections"].([]any)
	template["collections"] = []any{collections[1]}
	require.NoError(t, factory.WriteJSONAtomic(filepath.Join(fixture, "990001", "asset.meta.json"), template))
	release := factory.Release{Id: 990001, PluginId: "Fixture", Version: "1", SourceHash: "source-test", BundleHash: "bundle-test"}
	_, err = stageReleaseGeneratorBatch(release, "batch-stale")
	require.ErrorContains(t, err, "批次已变化")
	stage, err := stageReleaseGeneratorBatch(release, pointer.BatchID)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, factory.CleanupReleaseStaticStagingDir(stage)) })
	var seal map[string]any
	require.NoError(t, readJSONFile(filepath.Join(stage, "generator-batch", "seal.json"), &seal))
	require.Equal(t, response.BatchID, seal["batchId"])
	require.Equal(t, "bundle-test", seal["bundleHash"])
	require.FileExists(t, filepath.Join(stage, "generator-batch", "freeze-audit", "validation-summary.json"))
	// 等级文件被篡改，即使 all.json 保持正确也必须失败。
	require.NoError(t, os.WriteFile(filepath.Join(stage, "generated", "fish", "rare.json"), []byte("[]"), 0644))
	require.Error(t, auditBatchFiles(batch, filepath.Join(stage, "generated"), filepath.Join(stage, "tamper-audit")))
	// 下一次生成失败不改变当前成功批次，并释放锁。
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config", "collection.json"), []byte("invalid"), 0644))
	_, err = generateFormalBatch(dir, "failed-run")
	require.Error(t, err)
	_, retained, err := readCurrentGeneratorBatch(dir)
	require.NoError(t, err)
	require.Equal(t, pointer, retained)
	_, err = os.Stat(filepath.Join(dir, "generated", ".formal-lock"))
	require.True(t, os.IsNotExist(err))
	// 批次本身被改动时，在复制和执行归档程序之前拒绝。
	require.NoError(t, os.WriteFile(filepath.Join(batch, "generated", "fish", "rare.json"), []byte("[]"), 0644))
	_, _, err = readCurrentGeneratorBatch(dir)
	require.ErrorContains(t, err, "文件已变化")
}
