package factory_service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"senspace/domain"
	"senspace/domain/d_util"
	"senspace/domain/factory"
	"senspace/pkg/app/security"
	"senspace/pkg/setting"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 组件 NFT 铸造快照。
func TestMintReleaseAssetCreatesFishNFTSnapshots(t *testing.T) {
	env := setupMintAssetServiceTest(t)
	release := createFishTankMintTestRelease(t, env.db)
	user := security.JwtUser{
		Id:   990000000000101,
		Addr: fmt.Sprintf("0x%040x", release.Id),
	}
	ownerKey := factory.OwnerIndexKey(user.Addr)
	t.Cleanup(func() {
		cleanupMintTestData(t, env.db, release.Id, user.Id, ownerKey)
	})

	freezeResponse, stagingDir, err := freezeReleaseAssets(env.db, release)
	require.NoError(t, err)
	if stagingDir != "" {
		backupDir, activatedSnapshot, err := activateStagedReleaseSnapshot(release, stagingDir)
		require.NoError(t, err)
		require.True(t, activatedSnapshot)
		commitFreezeStaticSnapshot(backupDir, activatedSnapshot)
	}
	require.Equal(t, "ready", freezeResponse.Status)
	require.NotEmpty(t, freezeResponse.Pools)

	response, err := MintReleaseAsset(user, strconv.FormatInt(release.Id, 10), MintAssetRequest{
		Inputs: map[string]map[string]int64{
			"fish": {
				"common": 2,
				"rare":   1,
			},
		},
		TotalPaid: "60",
	})
	require.NoError(t, err)
	require.Equal(t, "60", response.TotalPaid)
	require.Len(t, response.Assets, 3)
	require.Equal(t, factory.OwnerIndexStaticURL(ownerKey), response.OwnerIndexUrl)
	require.Equal(t, factory.OwnerCompositionStaticURL(ownerKey), response.OwnerCompositionUrl)

	var assets []factory.Asset
	require.NoError(t, env.db.Where("release_id = ?", release.Id).Find(&assets).Error)
	require.Len(t, assets, 3)
	require.Equal(t, int64(3), countAssetsByCollection(assets, "fish"))
	seenItemIds := map[string]struct{}{}
	for _, asset := range assets {
		if asset.CollectionKey != "fish" {
			continue
		}
		require.Equal(t, factory.AssetKindComponent, asset.AssetKind)
		require.Equal(t, factory.ComponentRoleChild, asset.ComponentRole)
		require.Equal(t, "tank", asset.ParentKey)
		require.NotEmpty(t, asset.ItemId)
		require.NotNil(t, asset.ItemIndex)
		require.NotEmpty(t, asset.Tier)
		require.NotEmpty(t, asset.TraitHash)
		require.NotEmpty(t, asset.MetadataUri)
		require.NotEmpty(t, asset.ProofUri)
		require.NotContains(t, seenItemIds, asset.ItemId)
		seenItemIds[asset.ItemId] = struct{}{}
	}
	var fishPool factory.NFTInventoryPool
	require.NoError(t, env.db.First(&fishPool, "release_id = ? AND collection_key = ?", release.Id, "fish").Error)
	require.Equal(t, factory.NFTInventoryStrategyShuffled, fishPool.Strategy)
	require.Equal(t, int64(3), fishPool.MintedCount)

	var mintedInventoryItems []factory.NFTInventoryItem
	require.NoError(t, env.db.Where("release_id = ? AND collection_key = ? AND status = ?", release.Id, "fish", factory.NFTInventoryItemStatusMinted).Find(&mintedInventoryItems).Error)
	require.Len(t, mintedInventoryItems, 3)

	var record factory.MintRecord
	require.NoError(t, env.db.First(&record, "release_id = ? AND user_id = ?", release.Id, user.Id).Error)
	require.Equal(t, int64(3), record.Quantity)
	require.Equal(t, "60.000000000000000000", record.TotalPaid)

	var refreshedRelease factory.Release
	require.NoError(t, env.db.First(&refreshedRelease, "id = ?", release.Id).Error)
	require.Equal(t, int64(3), refreshedRelease.MintedCount)

	var relations []factory.AssetRelation
	require.NoError(t, env.db.Where("owner_key = ?", ownerKey).Find(&relations).Error)
	require.Empty(t, relations)

	var index ownerFactoryAssetIndex
	readJSONFileForTest(t, factory.OwnerIndexStaticPath(ownerKey), &index)
	require.Equal(t, "senspace.factory.owner-assets.v2", index.Schema)
	require.Equal(t, ownerKey, index.OwnerKey)
	require.Len(t, index.Assets, 3)

	var composition ownerFactoryAssetComposition
	readJSONFileForTest(t, factory.OwnerCompositionStaticPath(ownerKey), &composition)
	require.Equal(t, "senspace.factory.owner-composition.v1", composition.Schema)
	require.Equal(t, ownerKey, composition.OwnerKey)
	require.Empty(t, composition.Relations)

	for _, asset := range assets {
		var snapshot map[string]any
		readJSONFileForTest(t, factory.AssetStaticPath(asset.PluginId, asset.Id), &snapshot)
		require.Equal(t, "senspace.factory.asset.v2", snapshot["schema"])
		require.Equal(t, string(asset.AssetKind), snapshot["assetKind"])
		require.Equal(t, asset.ItemId, snapshot["itemId"])
		require.NotContains(t, snapshot, "ownerKey")
		var metadata map[string]any
		readJSONFileForTest(t, factory.MetadataStaticPath(asset.PluginId, asset.TokenId), &metadata)
		require.NotEmpty(t, metadata["name"])
		var proof map[string]any
		readJSONFileForTest(t, factory.ProofStaticPath(asset.PluginId, asset.TokenId), &proof)
		require.NotEmpty(t, proof["metadataHash"])
		if asset.CollectionKey == "fish" {
			require.Equal(t, asset.CollectionKey, snapshot["collectionKey"])
			require.Equal(t, string(asset.ComponentRole), snapshot["componentRole"])
			require.Equal(t, asset.ParentKey, snapshot["parentKey"])
			require.Equal(t, asset.ItemId, proof["itemId"])
			require.Equal(t, asset.CollectionKey, proof["collectionKey"])
			require.Equal(t, asset.Tier, snapshot["tier"])
			require.Equal(t, asset.TraitHash, snapshot["traitHash"])
		}
	}

	_, err = MintReleaseAsset(user, strconv.FormatInt(release.Id, 10), MintAssetRequest{
		Inputs: map[string]map[string]int64{
			"fish": {
				"legendary": 1,
			},
		},
		TotalPaid: "5000",
	})
	require.ErrorContains(t, err, "暂未开放铸造")
}

func TestMintReleaseAssetCreatesSimplePluginNFTWithoutAssetMeta(t *testing.T) {
	env := setupMintAssetServiceTest(t)
	release := factory.Release{
		Id:             generateID(),
		PluginId:       "SimpleBookPlugin",
		AuthorId:       990000000000104,
		Name:           "Simple Book",
		Version:        "1.0.0-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Status:         factory.ReleaseStatusPublished,
		CurrentRelease: true,
		ManifestSnapshot: factory.PluginManifestSnapshot{
			Name:        "Simple Book",
			Version:     "1.0.0",
			Entry:       "BookPreview",
			Description: "test",
		},
		Summary:       "test",
		Category:      "test",
		TotalSupply:   10,
		MintPer:       3,
		MintPrice:     "0",
		BuildStatus:   factory.BuildStatusReady,
		RuntimeKind:   factory.ReleaseRuntimeKindBook,
		UpgradePolicy: factory.ReleaseUpgradePolicyNone,
		UpgradePrice:  "0",
	}
	require.NoError(t, env.db.Create(&release).Error)
	require.NoError(t, factory.EnsureReleaseStaticSnapshot(release))

	user := security.JwtUser{
		Id:   990000000000105,
		Addr: fmt.Sprintf("0x%040x", release.Id+1),
	}
	ownerKey := factory.OwnerIndexKey(user.Addr)
	t.Cleanup(func() {
		cleanupMintTestData(t, env.db, release.Id, user.Id, ownerKey)
		require.NoError(t, os.RemoveAll(factory.ReleaseStaticDir(release)))
	})

	response, err := MintReleaseAsset(user, strconv.FormatInt(release.Id, 10), MintAssetRequest{
		Inputs: map[string]map[string]int64{
			"default": {
				"default": 2,
			},
		},
		TotalPaid: "0",
	})
	require.NoError(t, err)
	require.Equal(t, "0", response.TotalPaid)
	require.Len(t, response.Assets, 2)

	var assets []factory.Asset
	require.NoError(t, env.db.Where("release_id = ?", release.Id).Find(&assets).Error)
	require.Len(t, assets, 2)
	for _, asset := range assets {
		require.Equal(t, factory.AssetKindWhole, asset.AssetKind)
		require.Equal(t, "release.json", asset.TemplateRef)
		require.Equal(t, release.PluginId, asset.ItemId)
		require.NotEmpty(t, asset.MetadataUri)
	}

	var manifest map[string]any
	readJSONFileForTest(t, filepath.Join(factory.ReleaseStaticDir(release), "release.json"), &manifest)
	require.Equal(t, "0", manifest["mintPrice"])
	require.EqualValues(t, 10, manifest["totalSupply"])
	require.EqualValues(t, 3, manifest["mintPer"])
	require.Equal(t, "0", manifest["basePrice"])
	require.NotContains(t, manifest, "assetMetaUrl")
}

func TestMintReleaseAssetTreatsEmptyAssetMetaAsSimpleMode(t *testing.T) {
	env := setupMintAssetServiceTest(t)
	release := factory.Release{
		Id:             generateID(),
		PluginId:       "SimpleCubePlugin",
		AuthorId:       990000000000204,
		Name:           "Simple Cube",
		Version:        "1.0.0-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Status:         factory.ReleaseStatusPublished,
		CurrentRelease: true,
		ManifestSnapshot: factory.PluginManifestSnapshot{
			Name:        "Simple Cube",
			Version:     "1.0.0",
			Entry:       "src/index.ts",
			Description: "test",
		},
		Summary:       "test",
		Category:      "test",
		TotalSupply:   10,
		MintPer:       2,
		MintPrice:     "0",
		BuildStatus:   factory.BuildStatusReady,
		RuntimeKind:   factory.ReleaseRuntimeKindArtifact,
		UpgradePolicy: factory.ReleaseUpgradePolicyNone,
		UpgradePrice:  "0",
	}
	require.NoError(t, env.db.Create(&release).Error)
	require.NoError(t, factory.EnsureReleaseStaticSnapshot(release))
	require.NoError(t, os.WriteFile(filepath.Join(factory.ReleaseStaticDir(release), "asset.meta.json"), []byte("{\n  \"collections\": [],\n  \"pluginId\": \"SimpleCubePlugin\",\n  \"schema\": \"senspace.asset-meta.v1\"\n}\n"), 0644))

	user := security.JwtUser{
		Id:   990000000000205,
		Addr: fmt.Sprintf("0x%040x", release.Id+1),
	}
	ownerKey := factory.OwnerIndexKey(user.Addr)
	t.Cleanup(func() {
		cleanupMintTestData(t, env.db, release.Id, user.Id, ownerKey)
		require.NoError(t, os.RemoveAll(factory.ReleaseStaticDir(release)))
	})

	response, err := MintReleaseAsset(user, strconv.FormatInt(release.Id, 10), MintAssetRequest{
		Inputs: map[string]map[string]int64{
			"default": {
				"default": 2,
			},
		},
		TotalPaid: "0",
	})
	require.NoError(t, err)
	require.Equal(t, "0", response.TotalPaid)
	require.Len(t, response.Assets, 2)

	var assets []factory.Asset
	require.NoError(t, env.db.Where("release_id = ?", release.Id).Find(&assets).Error)
	require.Len(t, assets, 2)
	for _, asset := range assets {
		require.Equal(t, factory.AssetKindWhole, asset.AssetKind)
		require.Equal(t, "release.json", asset.TemplateRef)
	}
}

func TestCollectionHashesChanged(t *testing.T) {
	changed, keys := collectionHashesChanged([]factory.NFTInventoryPool{
		{CollectionKey: "fish", CollectionHash: "fish-hash"},
		{CollectionKey: "tank", CollectionHash: "tank-hash"},
	}, map[string]string{
		"fish": "fish-hash",
		"tank": "tank-hash",
	})
	require.False(t, changed)
	require.Empty(t, keys)

	changed, keys = collectionHashesChanged([]factory.NFTInventoryPool{
		{CollectionKey: "fish", CollectionHash: "old-fish-hash"},
		{CollectionKey: "tank", CollectionHash: "tank-hash"},
	}, map[string]string{
		"fish": "new-fish-hash",
		"tank": "tank-hash",
	})
	require.True(t, changed)
	require.Equal(t, []string{"fish"}, keys)
}

// 资产集合 hash 不变时，仍应同步发布快照中的 asset.meta.json。
func TestFreezeReleaseAssetsSyncsAssetMetaWithoutCollectionHashChange(t *testing.T) {
	env := setupMintAssetServiceTest(t)
	repoRoot := factoryServiceRepoRoot(t)

	workspace := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspace, "asset", "factory-templates", "SyncMetaPlugin"), 0o755))
	t.Cleanup(func() { require.NoError(t, os.Chdir(repoRoot)) })
	require.NoError(t, os.Chdir(workspace))

	assetMetaPath := filepath.Join(workspace, "asset", "factory-templates", "SyncMetaPlugin", "asset.meta.json")
	itemsPath := filepath.Join(workspace, "asset", "factory-templates", "SyncMetaPlugin", "items.json")
	require.NoError(t, os.WriteFile(assetMetaPath, []byte(`{
  "schema": "senspace.asset-meta.v2",
  "pluginId": "SyncMetaPlugin",
  "collections": [
    {
      "label": "收藏集",
      "key": "items",
      "assetKind": "component",
      "componentRole": "root",
      "metadataRef": "items.json",
      "unitPrice": "1"
    }
  ]
}`), 0o644))
	require.NoError(t, os.WriteFile(itemsPath, []byte(`[
  { "id": "item-1" }
]`), 0o644))

	release := factory.Release{
		Id:             generateID(),
		PluginId:       "SyncMetaPlugin",
		AuthorId:       990000000000102,
		Name:           "Sync Meta Plugin",
		Version:        "1.0.0-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Status:         factory.ReleaseStatusPublished,
		CurrentRelease: false,
		ManifestSnapshot: factory.PluginManifestSnapshot{
			Name:        "Sync Meta Plugin",
			Version:     "1.0.0",
			Entry:       "src/index.ts",
			Description: "test",
		},
		Summary:       "test",
		Category:      "test",
		TotalSupply:   10,
		MintPer:       5,
		MintPrice:     "0",
		BuildStatus:   factory.BuildStatusReady,
		RuntimeKind:   factory.ReleaseRuntimeKindArtifact,
		UpgradePolicy: factory.ReleaseUpgradePolicyNone,
		UpgradePrice:  "0",
	}
	require.NoError(t, env.db.Create(&release).Error)
	t.Cleanup(func() {
		cleanupFreezeReleaseTestData(t, env.db, release.Id)
		require.NoError(t, os.RemoveAll(factory.ReleaseStaticDir(release)))
	})

	freezeResponse, stagingDir, err := freezeReleaseAssets(env.db, release)
	require.NoError(t, err)
	require.Equal(t, "ready", freezeResponse.Status)
	require.NotEmpty(t, stagingDir)

	backupDir, activatedSnapshot, err := activateStagedReleaseSnapshot(release, stagingDir)
	require.NoError(t, err)
	require.True(t, activatedSnapshot)
	commitFreezeStaticSnapshot(backupDir, activatedSnapshot)

	initialMetaPath := filepath.Join(factory.ReleaseStaticDir(release), "asset.meta.json")
	var initialMeta map[string]any
	readJSONFileForTest(t, initialMetaPath, &initialMeta)
	collections := initialMeta["collections"].([]any)
	firstCollection := collections[0].(map[string]any)
	require.Equal(t, "1", firstCollection["unitPrice"])

	require.NoError(t, os.WriteFile(assetMetaPath, []byte(`{
  "schema": "senspace.asset-meta.v2",
  "pluginId": "SyncMetaPlugin",
  "collections": [
    {
      "label": "收藏集",
      "key": "items",
      "assetKind": "component",
      "componentRole": "root",
      "metadataRef": "items.json",
      "unitPrice": "2"
    }
  ]
}`), 0o644))

	freezeResponse, stagingDir, err = freezeReleaseAssets(env.db, release)
	require.NoError(t, err)
	require.Equal(t, "unchanged", freezeResponse.Status)
	require.NotEmpty(t, stagingDir)

	backupDir, activatedSnapshot, err = activateStagedReleaseSnapshot(release, stagingDir)
	require.NoError(t, err)
	require.True(t, activatedSnapshot)
	commitFreezeStaticSnapshot(backupDir, activatedSnapshot)

	var refreshedMeta map[string]any
	readJSONFileForTest(t, filepath.Join(factory.ReleaseStaticDir(release), "asset.meta.json"), &refreshedMeta)
	collections = refreshedMeta["collections"].([]any)
	firstCollection = collections[0].(map[string]any)
	require.Equal(t, "2", firstCollection["unitPrice"])
}

func TestFreezeReleaseAssetsSkipsInventoryRebuildForPriceOnlyTemplateChange(t *testing.T) {
	env := setupMintAssetServiceTest(t)
	repoRoot := factoryServiceRepoRoot(t)

	workspace := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspace, "asset", "factory-templates", "PriceOnlyPlugin"), 0o755))
	t.Cleanup(func() { require.NoError(t, os.Chdir(repoRoot)) })
	require.NoError(t, os.Chdir(workspace))

	assetMetaPath := filepath.Join(workspace, "asset", "factory-templates", "PriceOnlyPlugin", "asset.meta.json")
	itemsPath := filepath.Join(workspace, "asset", "factory-templates", "PriceOnlyPlugin", "items.json")
	require.NoError(t, os.WriteFile(itemsPath, []byte(`[
  { "id": "item-1", "tier": "common" },
  { "id": "item-2", "tier": "common" }
]`), 0o644))
	require.NoError(t, os.WriteFile(assetMetaPath, []byte(`{
  "schema": "senspace.asset-meta.v2",
  "pluginId": "PriceOnlyPlugin",
  "collections": [
    {
      "label": "鱼",
      "key": "fish",
      "assetKind": "component",
      "componentRole": "root",
      "metadataRef": "items.json",
      "tierConfig": {
        "common": { "price": "5", "supply": 2, "mintLimit": 1 }
      }
    }
  ]
}`), 0o644))

	release := factory.Release{
		Id:       generateID(),
		PluginId: "PriceOnlyPlugin",
		AuthorId: 990000000000103,
		Name:     "Price Only Plugin",
		Version:  "1.0.0-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Status:   factory.ReleaseStatusPublished,
		ManifestSnapshot: factory.PluginManifestSnapshot{
			Name:        "Price Only Plugin",
			Version:     "1.0.0",
			Entry:       "src/index.ts",
			Description: "test",
		},
		Summary:       "test",
		Category:      "test",
		TotalSupply:   10,
		MintPer:       5,
		MintPrice:     "0",
		BuildStatus:   factory.BuildStatusReady,
		RuntimeKind:   factory.ReleaseRuntimeKindArtifact,
		UpgradePolicy: factory.ReleaseUpgradePolicyNone,
		UpgradePrice:  "0",
	}
	require.NoError(t, env.db.Create(&release).Error)
	t.Cleanup(func() {
		cleanupFreezeReleaseTestData(t, env.db, release.Id)
		require.NoError(t, os.RemoveAll(factory.ReleaseStaticDir(release)))
	})

	freezeResponse, stagingDir, err := freezeReleaseAssets(env.db, release)
	require.NoError(t, err)
	require.Equal(t, "ready", freezeResponse.Status)
	backupDir, activatedSnapshot, err := activateStagedReleaseSnapshot(release, stagingDir)
	require.NoError(t, err)
	require.True(t, activatedSnapshot)
	commitFreezeStaticSnapshot(backupDir, activatedSnapshot)

	var beforePools []factory.NFTInventoryPool
	require.NoError(t, env.db.Where("release_id = ?", release.Id).Find(&beforePools).Error)
	require.Len(t, beforePools, 1)
	beforePoolID := beforePools[0].Id
	beforeHash := beforePools[0].CollectionHash
	beforeRoot := beforePools[0].MerkleRoot

	require.NoError(t, os.WriteFile(assetMetaPath, []byte(`{
  "schema": "senspace.asset-meta.v2",
  "pluginId": "PriceOnlyPlugin",
  "collections": [
    {
      "label": "鱼",
      "key": "fish",
      "assetKind": "component",
      "componentRole": "root",
      "metadataRef": "items.json",
      "tierConfig": {
        "common": { "price": "50", "supply": 2, "mintLimit": 9 }
      }
    }
  ]
}`), 0o644))

	freezeResponse, stagingDir, err = freezeReleaseAssets(env.db, release)
	require.NoError(t, err)
	require.Equal(t, "unchanged", freezeResponse.Status)
	require.Contains(t, freezeResponse.Message, "价格模板已同步")
	require.NotEmpty(t, stagingDir)

	backupDir, activatedSnapshot, err = activateStagedReleaseSnapshot(release, stagingDir)
	require.NoError(t, err)
	require.True(t, activatedSnapshot)
	commitFreezeStaticSnapshot(backupDir, activatedSnapshot)

	var afterPools []factory.NFTInventoryPool
	require.NoError(t, env.db.Where("release_id = ?", release.Id).Find(&afterPools).Error)
	require.Len(t, afterPools, 1)
	require.Equal(t, beforePoolID, afterPools[0].Id)
	require.Equal(t, beforeHash, afterPools[0].CollectionHash)
	require.Equal(t, beforeRoot, afterPools[0].MerkleRoot)

	var refreshedMeta map[string]any
	readJSONFileForTest(t, filepath.Join(factory.ReleaseStaticDir(release), "asset.meta.json"), &refreshedMeta)
	collections := refreshedMeta["collections"].([]any)
	tierConfig := collections[0].(map[string]any)["tierConfig"].(map[string]any)
	common := tierConfig["common"].(map[string]any)
	require.Equal(t, "50", common["price"])
	require.EqualValues(t, 9, common["mintLimit"])
}

func TestClearReleaseDevRemovesPublishedArtifactDirectories(t *testing.T) {
	env := setupMintAssetServiceTest(t)
	setting.Config.App.Env = "ldev"

	now := time.Now()
	release := factory.Release{
		Id:             generateID(),
		PluginId:       "ClearReleasePlugin",
		AuthorId:       990000000000106,
		Name:           "Clear Release Plugin",
		Version:        "1.0.0",
		Status:         factory.ReleaseStatusPublished,
		CurrentRelease: false,
		ManifestSnapshot: factory.PluginManifestSnapshot{
			Name:        "Clear Release Plugin",
			Version:     "1.0.0",
			Entry:       "src/index.ts",
			Description: "test",
		},
		Summary:       "test",
		Category:      "test",
		TotalSupply:   10,
		MintPer:       2,
		MintPrice:     "0",
		BuildStatus:   factory.BuildStatusReady,
		RuntimeKind:   factory.ReleaseRuntimeKindArtifact,
		UpgradePolicy: factory.ReleaseUpgradePolicyNone,
		UpgradePrice:  "0",
		PublishedAt:   &now,
		BuiltAt:       &now,
	}
	require.NoError(t, env.db.Create(&release).Error)
	t.Cleanup(func() {
		_ = env.db.Exec("DELETE FROM fact_release WHERE id = ?", release.Id).Error
		_ = os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "assets", release.PluginId))
		_ = os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "metadata", release.PluginId))
		_ = os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "proofs", release.PluginId))
		_ = os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "owners"))
		_ = os.RemoveAll(factory.ReleaseStaticDir(release))
		_ = os.RemoveAll(factory.ReleaseStaticStagingDir(release))
		_ = os.RemoveAll(factory.ReleaseStaticDir(release) + ".previous")
	})

	sourceDir := getPluginSourceSnapshotRoot(release.PluginId, release.Id)
	require.NoError(t, os.MkdirAll(sourceDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "manifest.snapshot.json"), []byte(`{}`), 0o644))

	runtimeDir := getPluginRuntimeReleaseRoot(release.PluginId, release.Version, release.Id)
	require.NoError(t, os.MkdirAll(runtimeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "runtime-manifest.json"), []byte(`{}`), 0o644))

	releaseDir := factory.ReleaseStaticDir(release)
	require.NoError(t, os.MkdirAll(releaseDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(releaseDir, "release.json"), []byte(`{}`), 0o644))

	releaseStagingDir := factory.ReleaseStaticStagingDir(release)
	require.NoError(t, os.MkdirAll(releaseStagingDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(releaseStagingDir, "release.json"), []byte(`{}`), 0o644))

	releasePreviousDir := factory.ReleaseStaticDir(release) + ".previous"
	require.NoError(t, os.MkdirAll(releasePreviousDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(releasePreviousDir, "release.json"), []byte(`{}`), 0o644))

	_, err := ClearReleaseDev(security.JwtUser{Id: release.AuthorId}, strconv.FormatInt(release.Id, 10))
	require.NoError(t, err)

	var remaining factory.Release
	require.ErrorIs(t, env.db.First(&remaining, "id = ?", release.Id).Error, gorm.ErrRecordNotFound)

	require.NoDirExists(t, sourceDir)
	require.NoDirExists(t, runtimeDir)
	require.NoDirExists(t, releaseDir)
	require.NoDirExists(t, releaseStagingDir)
	require.NoDirExists(t, releasePreviousDir)

	require.NoDirExists(t, filepath.Dir(sourceDir))
	require.NoDirExists(t, filepath.Dir(runtimeDir))
	require.NoDirExists(t, filepath.Dir(releaseDir))
	require.NoDirExists(t, filepath.Dir(releaseStagingDir))
}

func TestClearReleaseRejectsMintedRelease(t *testing.T) {
	env := setupMintAssetServiceTest(t)
	setting.Config.App.Env = "ldev"

	release := factory.Release{
		Id:             generateID(),
		PluginId:       "ClearReleaseLockedPlugin",
		AuthorId:       990000000000107,
		Name:           "Clear Release Locked Plugin",
		Version:        "1.0.0",
		Status:         factory.ReleaseStatusPublished,
		CurrentRelease: false,
		ManifestSnapshot: factory.PluginManifestSnapshot{
			Name:        "Clear Release Locked Plugin",
			Version:     "1.0.0",
			Entry:       "src/index.ts",
			Description: "test",
		},
		Summary:       "test",
		Category:      "test",
		TotalSupply:   10,
		MintPer:       2,
		MintPrice:     "0",
		BuildStatus:   factory.BuildStatusReady,
		RuntimeKind:   factory.ReleaseRuntimeKindArtifact,
		UpgradePolicy: factory.ReleaseUpgradePolicyNone,
		UpgradePrice:  "0",
		MintedCount:   1,
	}
	require.NoError(t, env.db.Create(&release).Error)
	t.Cleanup(func() {
		_ = env.db.Exec("DELETE FROM fact_release WHERE id = ?", release.Id).Error
	})

	_, err := ClearRelease(security.JwtUser{Id: release.AuthorId}, strconv.FormatInt(release.Id, 10))
	require.Error(t, err)
	require.ErrorContains(t, err, "已有铸造记录")
}

type mintAssetServiceTestEnv struct {
	db *gorm.DB
}

// 本地 ldev MySQL。
func setupMintAssetServiceTest(t *testing.T) mintAssetServiceTestEnv {
	t.Helper()
	// 本组使用少量手写资产模板；正式生成批次另由全量集成测试覆盖。
	originalTooling := pluginFactoryToolingRegistry["FishTank"]
	fixtureTooling := originalTooling
	fixtureTooling.Generator = nil
	pluginFactoryToolingRegistry["FishTank"] = fixtureTooling
	t.Cleanup(func() { pluginFactoryToolingRegistry["FishTank"] = originalTooling })

	originalConfig := *setting.Config
	originalDB := domain.Db
	originalWd, err := os.Getwd()
	require.NoError(t, err)

	repoRoot := factoryServiceRepoRoot(t)
	require.NoError(t, os.Chdir(repoRoot))

	setting.Config.Database = setting.Database{
		Type:     "mysql",
		User:     envOrDefault("SENSPACE_TEST_DB_USER", "root"),
		Password: envOrDefault("SENSPACE_TEST_DB_PASSWORD", "smart@vserp"),
		Host:     envOrDefault("SENSPACE_TEST_DB_HOST", "127.0.0.1:3307"),
		Name:     envOrDefault("SENSPACE_TEST_DB_NAME", "senspace_factory_test"),
	}
	setting.Config.App.FilePath.Factory = filepath.Join(t.TempDir(), "factory")
	setting.Config.App.RuntimeRootPath = filepath.Join(t.TempDir(), "runtime")

	require.NoError(t, d_util.EnsureDatabaseExists(setting.Config.Database.Name))
	require.NoError(t, domain.Setup())
	require.NotNil(t, domain.Db, "无法连接测试数据库，请确认 ldev MySQL 运行在 127.0.0.1:3307")
	d_util.InitTable(domain.Db)

	t.Cleanup(func() {
		if domain.Db != nil {
			if sqlDB, err := domain.Db.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
		domain.Db = originalDB
		*setting.Config = originalConfig
		require.NoError(t, os.Chdir(originalWd))
	})

	return mintAssetServiceTestEnv{db: domain.Db}
}

// 创建 FishTank 铸造测试发布。
func createFishTankMintTestRelease(t *testing.T, db *gorm.DB) factory.Release {
	t.Helper()

	now := time.Now()
	release := factory.Release{
		Id:             generateID(),
		PluginId:       "FishTank",
		AuthorId:       990000000000101,
		AuthorSnapshot: factory.AuthorSnapshot{Id: "mint-test", Name: "Mint Test"},
		Name:           "FishTank Mint Test",
		Version:        "test-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Status:         factory.ReleaseStatusPublished,
		ReviewStatus:   factory.ReviewStatusApproved,
		CurrentRelease: false,
		ManifestSnapshot: factory.PluginManifestSnapshot{
			Name:        "FishTank Mint Test",
			Version:     "test",
			Entry:       "FishTank",
			Description: "组合资产测试",
		},
		Summary:       "组合资产测试",
		Category:      "test",
		Tags:          factory.StringList{"test"},
		TotalSupply:   100,
		MintPer:       10,
		MintPrice:     "0",
		BuildStatus:   factory.BuildStatusReady,
		RuntimeKind:   factory.ReleaseRuntimeKindBuiltin,
		UpgradePolicy: factory.ReleaseUpgradePolicyNone,
		UpgradePrice:  "0",
		PublishedAt:   &now,
		BuiltAt:       &now,
	}
	require.NoError(t, db.Create(&release).Error)
	return release
}

// 清理铸造测试数据。
func cleanupMintTestData(t *testing.T, db *gorm.DB, releaseId int64, userId uint64, ownerKey string) {
	t.Helper()

	var release factory.Release
	_ = db.First(&release, "id = ?", releaseId).Error
	pluginId := release.PluginId

	var assets []factory.Asset
	require.NoError(t, db.Where("release_id = ?", releaseId).Find(&assets).Error)

	require.NoError(t, db.Exec("DELETE FROM fact_asset_relation WHERE owner_key = ?", ownerKey).Error)
	require.NoError(t, db.Exec("DELETE FROM fact_nft_inventory_item WHERE release_id = ?", releaseId).Error)
	require.NoError(t, db.Exec("DELETE FROM fact_nft_inventory_pool WHERE release_id = ?", releaseId).Error)
	require.NoError(t, db.Exec("DELETE FROM fact_asset WHERE release_id = ?", releaseId).Error)
	require.NoError(t, db.Exec("DELETE FROM fact_user_ownership WHERE user_id = ? AND plugin_id = ?", userId, pluginId).Error)
	require.NoError(t, db.Exec("DELETE FROM fact_mint_record WHERE release_id = ?", releaseId).Error)
	require.NoError(t, db.Exec("DELETE FROM sys_async_task WHERE biz_type = ? AND biz_id = ?", staticTaskBizTypeFactoryOwner, releaseId).Error)
	require.NoError(t, db.Exec("DELETE FROM sys_async_task WHERE biz_type = ? AND task_key = ?", staticTaskBizTypeFactoryOwner, "owner:"+ownerKey).Error)
	require.NoError(t, db.Exec("DELETE FROM sys_async_task WHERE biz_type = ? AND biz_id = ?", staticTaskBizTypeFactoryRelease, releaseId).Error)
	require.NoError(t, db.Exec("DELETE FROM fact_release WHERE id = ?", releaseId).Error)

	for _, asset := range assets {
		if err := os.Remove(factory.AssetStaticPath(asset.PluginId, asset.Id)); err != nil && !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		if err := os.Remove(factory.MetadataStaticPath(asset.PluginId, asset.TokenId)); err != nil && !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		if err := os.Remove(factory.ProofStaticPath(asset.PluginId, asset.TokenId)); err != nil && !os.IsNotExist(err) {
			require.NoError(t, err)
		}
	}

	ownerDir := filepath.Dir(factory.OwnerIndexStaticPath(ownerKey))
	require.NoError(t, os.RemoveAll(ownerDir))
	require.NoError(t, os.RemoveAll(ownerDir+".staging"))
	require.NoError(t, os.RemoveAll(ownerDir+".previous"))

	if release.Id != 0 {
		require.NoError(t, os.RemoveAll(factory.ReleaseStaticDir(release)))
		require.NoError(t, os.RemoveAll(factory.ReleaseStaticStagingDir(release)))
		require.NoError(t, os.RemoveAll(factory.ReleaseStaticDir(release)+".previous"))
	}
	if pluginId != "" {
		require.NoError(t, os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "assets", pluginId)))
		require.NoError(t, os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "metadata", pluginId)))
		require.NoError(t, os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "proofs", pluginId)))
		require.NoError(t, os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "owners", ownerKey)))
	}
}

// 清理发布冻结测试数据。
func cleanupFreezeReleaseTestData(t *testing.T, db *gorm.DB, releaseId int64) {
	t.Helper()

	var release factory.Release
	_ = db.First(&release, "id = ?", releaseId).Error
	require.NoError(t, db.Exec("DELETE FROM fact_nft_inventory_item WHERE release_id = ?", releaseId).Error)
	require.NoError(t, db.Exec("DELETE FROM fact_nft_inventory_pool WHERE release_id = ?", releaseId).Error)
	require.NoError(t, db.Exec("DELETE FROM fact_asset WHERE release_id = ?", releaseId).Error)
	require.NoError(t, db.Exec("DELETE FROM fact_mint_record WHERE release_id = ?", releaseId).Error)
	require.NoError(t, db.Exec("DELETE FROM fact_release_status_history WHERE release_id = ?", releaseId).Error)
	require.NoError(t, db.Exec("DELETE FROM fact_release_price_history WHERE release_id = ?", releaseId).Error)
	require.NoError(t, db.Exec("DELETE FROM sys_async_task WHERE biz_type = ? AND biz_id = ?", "factory_release", releaseId).Error)
	require.NoError(t, db.Exec("DELETE FROM fact_release WHERE id = ?", releaseId).Error)
	if release.PluginId != "" {
		require.NoError(t, os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "assets", release.PluginId)))
		require.NoError(t, os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "metadata", release.PluginId)))
		require.NoError(t, os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "proofs", release.PluginId)))
		require.NoError(t, os.RemoveAll(factory.ReleaseStaticDir(release)))
		require.NoError(t, os.RemoveAll(factory.ReleaseStaticStagingDir(release)))
		require.NoError(t, os.RemoveAll(factory.ReleaseStaticDir(release)+".previous"))
	}
}

// 统计指定集合资产数量。
func countAssetsByCollection(assets []factory.Asset, collectionKey string) int64 {
	var count int64
	for _, asset := range assets {
		if asset.CollectionKey == collectionKey {
			count++
		}
	}
	return count
}

// 读取测试 JSON 文件。
func readJSONFileForTest(t *testing.T, path string, target any) {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, target))
}

// 定位 factory service 仓库根目录。
func factoryServiceRepoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		next := filepath.Dir(wd)
		require.NotEqual(t, wd, next, "未找到仓库根目录")
		wd = next
	}
}

// 返回环境变量或默认值。
func envOrDefault(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
