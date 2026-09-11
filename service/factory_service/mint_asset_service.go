package factory_service

import (
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"senspace/domain/factory"
	"senspace/domain/task"
	"senspace/pkg/app/security"
	"senspace/pkg/merkle"
	"senspace/pkg/setting"
	"senspace/service/ds_service"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 对应 asset.meta.json。
type assetValueTemplate struct {
	// 模板协议版本。
	Schema string `json:"schema"`
	// 模板所属插件 ID。
	PluginId string `json:"pluginId"`
	// 数组顺序即前端展示顺序。
	Collections []assetValueCollection `json:"collections"`
}

// 可铸造模板集合。
type assetValueCollection struct {
	// 集合展示名。
	Label string `json:"label"`
	// 集合业务键，mint 请求按该 key 提交数量。
	Key string `json:"key"`
	// 工厂 NFT 资产类型。
	AssetKind factory.AssetKind `json:"assetKind"`
	// 组件角色，assetKind 为 component 时必填。
	ComponentRole factory.ComponentRole `json:"componentRole"`
	// 子组件挂载到的父组件 collection key。
	ParentKey string `json:"parentKey"`
	// 例如 generated/fish/{tier}.json。
	MetadataRef string `json:"metadataRef"`
	// 配置后强制校验 item 上的属性哈希字段。
	TraitHashField string `json:"traitHashField"`
	// 不分等级 collection 的统一单价。
	UnitPrice string `json:"unitPrice"`
	// 按等级合并价格、发行量和单次铸造上限；key 必须匹配模板项的 tier 字段。
	TierConfig map[string]assetTierConfig `json:"tierConfig"`
}

// 单个等级的铸造配置。
type assetTierConfig struct {
	// 值为 "-" 表示该等级暂不开放铸造。
	Price string `json:"price"`
	// 等级总量，后续库存校验使用。
	Supply int64 `json:"supply"`
	// 单次铸造上限，0 表示不可选。
	MintLimit int64 `json:"mintLimit"`
}

// 计价后的铸造明细。
type mintSelectionResult struct {
	// 最终选中的待铸造资产列表。
	Items []mintSelectionItem
	// 按模板规则计算出的应付总额。
	ExpectedPaid string
	// 本次铸造的总资产数量。
	TotalCount int64
}

// 单个待生成 NFT。
type mintSelectionItem struct {
	// 资产结构类型。
	AssetKind factory.AssetKind
	// 所属 collection key。
	CollectionKey string
	// 组件在组合结构中的角色。
	ComponentRole factory.ComponentRole
	// 子组件要挂载到的父 collection key。
	ParentKey string
	// 对应冻结库存项 ID；可复用模板时允许为空。
	InventoryItemId int64
	// 模板项所在文件引用，例如 generated/fish/common.json。
	TemplateRef string
	// 库存项业务 ID，用于回查原始模板内容。
	ItemId string
	// 模板项在集合中的稳定序号。
	ItemIndex *int
	// 稀有度等级。
	Tier string
	// 模板项冻结后的属性哈希。
	TraitHash string
}

// 模板项定位结果。
type templateItemMatch struct {
	// 命中的模板项内容。
	Item map[string]any
	// 命中的模板项下标。
	Index int
}

// 归一化后的插件属性面板参数 JSON。
type normalizedPluginOptions struct {
	Raw  string
	Data map[string]any
}

// 静态目录中的单个 NFT 快照。
type mintedFactoryAsset struct {
	// 快照协议版本。
	Schema string `json:"schema"`
	// 资产 ID。
	AssetId string `json:"assetId"`
	// 链上 token ID，未上链时可为空。
	TokenId string `json:"tokenId,omitempty"`
	// 插件 ID。
	PluginId string `json:"pluginId"`
	// 发布记录 ID。
	ReleaseId string `json:"releaseId"`
	// 发布版本号。
	Version string `json:"version"`
	// 运行来源类型。
	RuntimeKind factory.ReleaseRuntimeKind `json:"runtimeKind"`
	// 资产结构类型。
	AssetKind factory.AssetKind `json:"assetKind"`
	// 所属 collection key。
	CollectionKey string `json:"collectionKey,omitempty"`
	// 组件角色。
	ComponentRole factory.ComponentRole `json:"componentRole,omitempty"`
	// 父组件 collection key。
	ParentKey string `json:"parentKey,omitempty"`
	// 模板项来源引用。
	TemplateRef string `json:"templateRef"`
	// 库存项业务 ID。
	ItemId string `json:"itemId"`
	// 模板项稳定序号。
	ItemIndex *int `json:"itemIndex,omitempty"`
	// 插件属性面板参数。
	PluginOptions map[string]any `json:"pluginOptions,omitempty"`
	// 稀有度等级。
	Tier string `json:"tier,omitempty"`
	// 冻结后的属性哈希。
	TraitHash string `json:"traitHash,omitempty"`
	// 发布快照入口地址。
	ReleaseUrl string `json:"releaseUrl"`
	// 铸造记录 ID。
	MintRecordId string `json:"mintRecordId"`
	// 铸造时间。
	MintedAt string `json:"mintedAt"`
	// NFT metadata 地址。
	MetadataUri string `json:"metadataUri,omitempty"`
	// NFT proof 地址。
	ProofUri string `json:"proofUri,omitempty"`
}

// 标准 NFT metadata。
type nftMetadata struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Image        string                 `json:"image,omitempty"`
	AnimationURL string                 `json:"animation_url,omitempty"`
	ExternalURL  string                 `json:"external_url,omitempty"`
	Attributes   []nftMetadataAttribute `json:"attributes"`
	Properties   map[string]any         `json:"properties"`
}

// 市场属性项。
type nftMetadataAttribute struct {
	TraitType string `json:"trait_type"`
	Value     any    `json:"value"`
}

// 当前阶段的可验证声明。
type nftProofSnapshot struct {
	// proof 协议版本。
	Schema string `json:"schema"`
	// 链上 token ID。
	TokenId string `json:"tokenId"`
	// 所属 collection key。
	CollectionKey string `json:"collectionKey,omitempty"`
	// 库存项业务 ID。
	ItemId string `json:"itemId,omitempty"`
	// 模板项稳定序号。
	ItemIndex *int `json:"itemIndex,omitempty"`
	// 稀有度等级。
	Tier string `json:"tier,omitempty"`
	// 冻结后的属性哈希。
	TraitHash string `json:"traitHash,omitempty"`
	// metadata 文件内容哈希。
	MetadataHash string `json:"metadataHash"`
	// 当前 token 的 leaf 哈希。
	Leaf string `json:"leaf"`
	// 当前阶段的根哈希。
	MerkleRoot string `json:"merkleRoot"`
	// Merkle proof 列表。
	Proof []string `json:"proof"`
}

// 钱包地址对应的可重建资产索引。
type ownerFactoryAssetIndex struct {
	Schema    string                   `json:"schema"`
	OwnerKey  string                   `json:"ownerKey"`
	UpdatedAt string                   `json:"updatedAt"`
	Assets    []ownerFactoryAssetEntry `json:"assets"`
}

// 索引中的资产摘要。
type ownerFactoryAssetEntry struct {
	// 资产 ID。
	AssetId string `json:"assetId"`
	// 插件 ID。
	PluginId string `json:"pluginId"`
	// 发布记录 ID。
	ReleaseId string `json:"releaseId"`
	// 发布版本号。
	Version string `json:"version"`
	// 运行来源类型。
	RuntimeKind factory.ReleaseRuntimeKind `json:"runtimeKind"`
	// 资产结构类型。
	AssetKind factory.AssetKind `json:"assetKind"`
	// 所属 collection key。
	CollectionKey string `json:"collectionKey,omitempty"`
	// 组件角色。
	ComponentRole factory.ComponentRole `json:"componentRole,omitempty"`
	// 父组件 collection key。
	ParentKey string `json:"parentKey,omitempty"`
	// 模板项来源引用。
	TemplateRef string `json:"templateRef"`
	// 库存项业务 ID。
	ItemId string `json:"itemId"`
	// 模板项稳定序号。
	ItemIndex *int `json:"itemIndex,omitempty"`
	// 插件属性面板参数。
	PluginOptions map[string]any `json:"pluginOptions,omitempty"`
	// 稀有度等级。
	Tier string `json:"tier,omitempty"`
	// 冻结后的属性哈希。
	TraitHash string `json:"traitHash,omitempty"`
	// 资产快照地址。
	AssetUrl string `json:"assetUrl"`
	// 发布快照地址。
	ReleaseUrl string `json:"releaseUrl"`
	// 铸造时间。
	MintedAt string `json:"mintedAt"`
}

// 钱包地址对应的组合快照。
type ownerFactoryAssetComposition struct {
	Schema    string                             `json:"schema"`
	OwnerKey  string                             `json:"ownerKey"`
	UpdatedAt string                             `json:"updatedAt"`
	Relations []ownerFactoryAssetCompositionEdge `json:"relations"`
}

// NFT 组合边。
type ownerFactoryAssetCompositionEdge struct {
	Id            string `json:"id"`
	RelationType  string `json:"relationType"`
	SourceAssetId string `json:"sourceAssetId"`
	TargetAssetId string `json:"targetAssetId"`
	Metadata      any    `json:"metadata,omitempty"`
}

// 已提交但仍需补偿回滚的铸造上下文。
type committedMintContext struct {
	ReleaseId    int64
	PluginId     string
	PluginName   string
	MintRecordId int64
	UserId       uint64
	OwnerKey     string
}

// 按发布快照生成独立 NFT，并用 DB 保持权威状态。
func MintReleaseAsset(user security.JwtUser, releaseIdRaw string, req MintAssetRequest) (*MintAssetResponse, error) {
	releaseId, err := parseID(releaseIdRaw, "发布记录ID")
	if err != nil {
		return nil, err
	}
	if user.Id == 0 {
		return nil, newParameterError("用户ID不能为空")
	}
	walletAddress := strings.TrimSpace(user.Addr)
	if walletAddress == "" {
		return nil, newParameterError("钱包地址不能为空")
	}

	totalPaid, err := validateDecimalString(req.TotalPaid, "支付总额", false)
	if err != nil {
		return nil, err
	}
	req.TotalPaid = totalPaid
	pluginOptions, err := normalizePluginOptions(req.PluginOptions)
	if err != nil {
		return nil, err
	}

	tx, err := db()
	if err != nil {
		return nil, err
	}

	var response MintAssetResponse
	var ownerKey string
	var committedMint committedMintContext
	err = tx.Transaction(func(tx *gorm.DB) error {
		var release factory.Release
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&release, "id = ?", releaseId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return newNotFoundError("发布记录不存在")
			}
			return err
		}
		if release.Status != factory.ReleaseStatusPublished {
			return newConflictError("当前发布记录不可铸造")
		}
		if defaultBuildStatus(release.BuildStatus) != factory.BuildStatusReady {
			return newConflictError("当前发布记录构建未完成")
		}
		valueTemplate, hasValueTemplate, err := loadReleaseMintTemplateIfExists(release)
		if err != nil {
			return err
		}
		var mintSelection mintSelectionResult
		if hasValueTemplate {
			mintSelection, err = resolveMintSelection(tx, release, valueTemplate, req.Inputs)
		} else {
			mintSelection, err = resolveSimpleMintSelection(release, req.Inputs)
		}
		if err != nil {
			return err
		}
		if release.MintedCount+mintSelection.TotalCount > release.TotalSupply {
			return newConflictError("铸造数量超过可发行数量")
		}
		if mintSelection.ExpectedPaid != req.TotalPaid {
			return newParameterError(fmt.Sprintf("支付总额应为 %s", mintSelection.ExpectedPaid))
		}

		ownerKey = factory.OwnerIndexKey(walletAddress)
		record := factory.MintRecord{
			Id:            generateID(),
			PluginId:      release.PluginId,
			ReleaseId:     release.Id,
			UserId:        user.Id,
			WalletAddress: walletAddress,
			Quantity:      mintSelection.TotalCount,
			TotalPaid:     req.TotalPaid,
			ChainId:       req.ChainId,
			TxHash:        strings.TrimSpace(req.TxHash),
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		committedMint = committedMintContext{
			ReleaseId:    release.Id,
			PluginId:     release.PluginId,
			PluginName:   strings.TrimSpace(release.Name),
			MintRecordId: record.Id,
			UserId:       user.Id,
			OwnerKey:     ownerKey,
		}

		var createdAssets []factory.Asset
		for _, selected := range mintSelection.Items {
			assetId := generateID()
			tokenId := strconv.FormatInt(assetId, 10)
			asset := factory.Asset{
				Id:            assetId,
				PluginId:      release.PluginId,
				ReleaseId:     release.Id,
				Version:       release.Version,
				RuntimeKind:   defaultRuntimeKind(release.RuntimeKind),
				AssetKind:     selected.AssetKind,
				CollectionKey: selected.CollectionKey,
				ComponentRole: selected.ComponentRole,
				ParentKey:     selected.ParentKey,
				TemplateRef:   selected.TemplateRef,
				ItemId:        selected.ItemId,
				ItemIndex:     selected.ItemIndex,
				PluginOptions: pluginOptions.Raw,
				Tier:          selected.Tier,
				TraitHash:     selected.TraitHash,
				OwnerAddress:  walletAddress,
				OwnerKey:      ownerKey,
				MintRecordId:  record.Id,
				ChainId:       req.ChainId,
				TokenId:       tokenId,
				MetadataUri:   factory.MetadataStaticURL(release.PluginId, tokenId),
				ProofUri:      factory.ProofStaticURL(release.PluginId, tokenId),
				Status:        factory.AssetStatusActive,
			}
			if err := tx.Create(&asset).Error; err != nil {
				return err
			}
			if selected.InventoryItemId != 0 {
				now := time.Now()
				if err := tx.Model(&factory.NFTInventoryItem{}).
					Where("id = ? AND status = ?", selected.InventoryItemId, factory.NFTInventoryItemStatusAvailable).
					Updates(map[string]any{
						"status":         factory.NFTInventoryItemStatusMinted,
						"asset_id":       asset.Id,
						"token_id":       asset.TokenId,
						"mint_record_id": record.Id,
						"owner_key":      ownerKey,
						"minted_at":      &now,
						"metadata_uri":   asset.MetadataUri,
						"proof_uri":      asset.ProofUri,
					}).Error; err != nil {
					return err
				}
				if err := tx.Model(&factory.NFTInventoryPool{}).
					Where("release_id = ? AND collection_key = ?", release.Id, selected.CollectionKey).
					UpdateColumn("minted_count", gorm.Expr("minted_count + ?", 1)).Error; err != nil {
					return err
				}
			}
			createdAssets = append(createdAssets, asset)
		}

		if err := createMintRelations(tx, ownerKey, createdAssets); err != nil {
			return err
		}

		release.MintedCount += mintSelection.TotalCount
		if release.MintedCount >= release.TotalSupply {
			release.Status = factory.ReleaseStatusSoldOut
		}
		if err := tx.Save(&release).Error; err != nil {
			return err
		}

		if err := ensurePluginOwnership(tx, user.Id, release); err != nil {
			return err
		}

		response = MintAssetResponse{
			Assets:              make([]MintAssetResponseAsset, 0, len(createdAssets)),
			OwnerIndexUrl:       factory.OwnerIndexStaticURL(ownerKey),
			OwnerCompositionUrl: factory.OwnerCompositionStaticURL(ownerKey),
			TotalPaid:           req.TotalPaid,
		}
		for _, asset := range createdAssets {
			response.Assets = append(response.Assets, MintAssetResponseAsset{
				AssetId:       strconv.FormatInt(asset.Id, 10),
				AssetKind:     asset.AssetKind,
				CollectionKey: asset.CollectionKey,
				ComponentRole: asset.ComponentRole,
				ParentKey:     asset.ParentKey,
				AssetUrl:      factory.AssetStaticURL(asset.PluginId, asset.Id),
				ItemIndex:     asset.ItemIndex,
				Tier:          asset.Tier,
				TraitHash:     asset.TraitHash,
				MetadataUri:   asset.MetadataUri,
				ProofUri:      asset.ProofUri,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.PluginAssetDraftId) != "" {
		factAssetIds := make([]int64, 0, len(response.Assets))
		for _, asset := range response.Assets {
			factAssetId, err := strconv.ParseInt(asset.AssetId, 10, 64)
			if err != nil {
				if rollbackErr := rollbackCommittedMintArtifacts(committedMint); rollbackErr != nil {
					return nil, fmt.Errorf("插件资源草稿归档失败: %w；补偿回滚失败: %v", err, rollbackErr)
				}
				return nil, err
			}
			factAssetIds = append(factAssetIds, factAssetId)
		}
		if err := ds_service.ArchiveDraftPluginAssetsToFactAssets(user, releaseId, req.PluginAssetDraftId, factAssetIds, pluginOptions.Data); err != nil {
			if rollbackErr := rollbackCommittedMintArtifacts(committedMint); rollbackErr != nil {
				return nil, fmt.Errorf("插件资源草稿归档失败: %w；补偿回滚失败: %v", err, rollbackErr)
			}
			return nil, err
		}
	}
	if err := rebuildOwnerFactorySnapshots(ownerKey); err != nil {
		if rollbackErr := rollbackCommittedMintArtifacts(committedMint); rollbackErr != nil {
			return nil, fmt.Errorf("铸造静态快照生成失败: %w；补偿回滚失败: %v", err, rollbackErr)
		}
		return nil, err
	}
	if err := enqueueFactoryOwnerAssetsSnapshot(committedMint, committedMint.PluginName); err != nil {
		if rollbackErr := rollbackCommittedMintArtifacts(committedMint); rollbackErr != nil {
			return nil, fmt.Errorf("铸造后续任务创建失败: %w；补偿回滚失败: %v", err, rollbackErr)
		}
		return nil, err
	}
	return &response, nil
}

// 冻结当前插件发布的资产快照与库存池。
func FreezeCurrentPluginReleaseAssets(user security.JwtUser, pluginIdRaw string, expectedBatch ...string) (*FreezeReleaseAssetsResponse, error) {
	pluginId := strings.TrimSpace(pluginIdRaw)
	if pluginId == "" {
		return nil, newParameterError("插件ID不能为空")
	}
	if err := requireReleaseFreezeOperator(user, pluginId); err != nil {
		return nil, err
	}
	if tooling, ok := pluginTooling(pluginId); ok && tooling.Generator != nil && (len(expectedBatch) == 0 || !batchIDPattern.MatchString(expectedBatch[0])) {
		return nil, newParameterError("请先预览正式批次，再提交批次编号冻结")
	}

	tx, err := db()
	if err != nil {
		return nil, err
	}

	var response *FreezeReleaseAssetsResponse
	var release factory.Release
	var stagingDir string
	var backupDir string
	activatedSnapshot := false
	err = tx.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("plugin_id = ? AND current_release = ?", pluginId, true).
			Order("published_at DESC").
			Order("updated_at DESC").
			First(&release).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return newNotFoundError("当前插件发布记录不存在")
			}
			return err
		}
		if release.AuthorId != 0 && release.AuthorId != user.Id {
			return newForbiddenError("无权冻结该发布记录")
		}

		freezeResponse, releaseStagingDir, err := freezeReleaseAssets(tx, release, expectedBatch...)
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
		response = freezeResponse
		return nil
	})
	if err != nil {
		rollbackFreezeStaticSnapshot(release, stagingDir, backupDir, activatedSnapshot)
		return nil, err
	}
	commitFreezeStaticSnapshot(backupDir, activatedSnapshot)
	return response, nil
}

// 清除当前插件发布的冻结快照和库存数据。
func ClearCurrentPluginReleaseAssetsFreeze(user security.JwtUser, pluginIdRaw string) (*ClearFreezeReleaseAssetsResponse, error) {
	pluginId := strings.TrimSpace(pluginIdRaw)
	if pluginId == "" {
		return nil, newParameterError("插件ID不能为空")
	}
	if err := requireReleaseFreezeOperator(user, pluginId); err != nil {
		return nil, err
	}

	tx, err := db()
	if err != nil {
		return nil, err
	}

	var release factory.Release
	var response *ClearFreezeReleaseAssetsResponse
	err = tx.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("plugin_id = ? AND current_release = ?", pluginId, true).
			Order("published_at DESC").
			Order("updated_at DESC").
			First(&release).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return newNotFoundError("当前插件发布记录不存在")
			}
			return err
		}

		removedItems, removedPools, err := clearReleaseFreezeData(tx, &release)
		if err != nil {
			return err
		}
		response = &ClearFreezeReleaseAssetsResponse{
			ReleaseId:    strconv.FormatInt(release.Id, 10),
			PluginId:     release.PluginId,
			Version:      release.Version,
			Message:      "发布冻结已清除",
			RemovedPools: removedPools,
			RemovedItems: removedItems,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := removeReleaseFreezeStaticFiles(release); err != nil {
		return nil, err
	}
	return response, nil
}

// 清除单个发布记录及其生成产物；仅开发环境允许。
func ClearReleaseDev(user security.JwtUser, releaseIdRaw string) (*ClearReleaseResponse, error) {
	if !isDevFactoryEnvironment() {
		return nil, newForbiddenError("仅开发环境允许清除发布")
	}

	releaseId, err := parseID(releaseIdRaw, "发布记录ID")
	if err != nil {
		return nil, err
	}

	tx, err := db()
	if err != nil {
		return nil, err
	}

	var release factory.Release
	var response *ClearReleaseResponse
	err = tx.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&release, "id = ?", releaseId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return newNotFoundError("发布记录不存在")
			}
			return err
		}
		if release.AuthorId != 0 && release.AuthorId != user.Id {
			return newForbiddenError("无权清除该发布记录")
		}

		response = &ClearReleaseResponse{
			ReleaseId: strconv.FormatInt(release.Id, 10),
			PluginId:  release.PluginId,
			Version:   release.Version,
			Message:   "发布已清除",
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := purgeReleaseGeneratedArtifactsByID(releaseId); err != nil {
		return nil, err
	}
	if err := ds_service.DeleteReleaseDraftArtifacts(release.Id); err != nil {
		return nil, err
	}

	err = tx.Transaction(func(tx *gorm.DB) error {
		if release.CurrentRelease {
			if err := promoteFallbackCurrentRelease(tx, release.PluginId, release.Id); err != nil {
				return err
			}
		}
		if err := tx.Where("release_id = ?", release.Id).Delete(&factory.ReleaseStatusHistory{}).Error; err != nil {
			return err
		}
		if err := tx.Where("release_id = ?", release.Id).Delete(&factory.ReleasePriceHistory{}).Error; err != nil {
			return err
		}
		if err := tx.Where("biz_type = ? AND biz_id = ?", staticTaskBizTypeFactoryRelease, release.Id).
			Delete(&task.AsyncTask{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&factory.Release{}, "id = ?", release.Id).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := removeReleaseBuildArtifactFiles(release); err != nil {
		return nil, err
	}
	return response, nil
}

func ClearRelease(user security.JwtUser, releaseIdRaw string) (*ClearReleaseResponse, error) {
	releaseId, err := parseID(releaseIdRaw, "发布记录ID")
	if err != nil {
		return nil, err
	}

	tx, err := db()
	if err != nil {
		return nil, err
	}

	var release factory.Release
	var response *ClearReleaseResponse
	err = tx.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&release, "id = ?", releaseId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return newNotFoundError("发布记录不存在")
			}
			return err
		}
		if release.AuthorId != 0 && release.AuthorId != user.Id {
			return newForbiddenError("无权清除该发布记录")
		}
		hasMinted, err := releaseHasMintedRecords(tx, release, nil)
		if err != nil {
			return err
		}
		if hasMinted || release.MintedCount > 0 {
			return newConflictError("已有铸造记录，不能清除该发布")
		}

		response = &ClearReleaseResponse{
			ReleaseId: strconv.FormatInt(release.Id, 10),
			PluginId:  release.PluginId,
			Version:   release.Version,
			Message:   "发布已清除",
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := purgeReleaseGeneratedArtifactsByID(releaseId); err != nil {
		return nil, err
	}
	if err := ds_service.DeleteReleaseDraftArtifacts(release.Id); err != nil {
		return nil, err
	}

	err = tx.Transaction(func(tx *gorm.DB) error {
		if release.CurrentRelease {
			if err := promoteFallbackCurrentRelease(tx, release.PluginId, release.Id); err != nil {
				return err
			}
		}
		if err := tx.Where("release_id = ?", release.Id).Delete(&factory.ReleaseStatusHistory{}).Error; err != nil {
			return err
		}
		if err := tx.Where("release_id = ?", release.Id).Delete(&factory.ReleasePriceHistory{}).Error; err != nil {
			return err
		}
		if err := tx.Where("biz_type = ? AND biz_id = ?", staticTaskBizTypeFactoryRelease, release.Id).
			Delete(&task.AsyncTask{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&factory.Release{}, "id = ?", release.Id).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := removeReleaseBuildArtifactFiles(release); err != nil {
		return nil, err
	}
	return response, nil
}

func clearReleaseFreezeData(tx *gorm.DB, release *factory.Release) (int64, int64, error) {
	existingPools, err := releaseInventoryPools(tx, *release)
	if err != nil {
		return 0, 0, err
	}
	hasMinted, err := releaseHasMintedRecords(tx, *release, existingPools)
	if err != nil {
		return 0, 0, err
	}
	if hasMinted && !isDevFactoryEnvironment() {
		return 0, 0, newConflictError("已有铸造记录，不能清除冻结数据")
	}
	if hasMinted {
		if err := clearDevReleaseMintData(tx, release); err != nil {
			return 0, 0, err
		}
	}
	return clearReleaseInventoryWithCount(tx, release.Id)
}

func purgeReleaseGeneratedArtifactsByID(releaseId int64) error {
	if releaseId == 0 {
		return nil
	}
	tx, err := db()
	if err != nil {
		return err
	}

	var release factory.Release
	var ownerTaskIDs []int64
	err = tx.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&release, "id = ?", releaseId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}

		ownerKeys, err := releaseOwnerKeys(tx, release.Id)
		if err != nil {
			return err
		}
		hasMinted, err := releaseHasMintedRecords(tx, release, nil)
		if err != nil {
			return err
		}
		if hasMinted {
			if err := clearDevReleaseMintData(tx, &release); err != nil {
				return err
			}
		} else if len(ownerKeys) > 0 {
			if err := cleanupOwnerFactoryArtifacts(tx, ownerKeys); err != nil {
				return err
			}
		}
		if _, _, err := clearReleaseInventoryWithCount(tx, release.Id); err != nil {
			return err
		}
		if err := tx.Model(&task.AsyncTask{}).
			Where("task_type = ? AND biz_type = ? AND biz_id = ?", task.TypeMint, staticTaskBizTypeFactoryOwner, release.Id).
			Pluck("id", &ownerTaskIDs).Error; err != nil {
			return err
		}
		if len(ownerTaskIDs) > 0 {
			if err := tx.Delete(&task.AsyncTask{}, "id IN ?", ownerTaskIDs).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if release.Id != 0 {
		if err := removeReleaseFreezeStaticFiles(release); err != nil {
			return err
		}
		if err := removeReleaseBuildArtifactFiles(release); err != nil {
			return err
		}
	}
	return nil
}

func isDevFactoryEnvironment() bool {
	return setting.IsDevLikeEnv()
}

func clearDevReleaseMintData(tx *gorm.DB, release *factory.Release) error {
	ownerKeys, err := releaseOwnerKeys(tx, release.Id)
	if err != nil {
		return err
	}
	assets, err := releaseAssets(tx, release.Id)
	if err != nil {
		return err
	}
	if err := removeMintedStaticFiles(assets); err != nil {
		return err
	}
	if err := clearReleaseAssetRelations(tx, assets); err != nil {
		return err
	}
	if err := tx.Where("release_id = ?", release.Id).Delete(&factory.Asset{}).Error; err != nil {
		return err
	}
	if err := tx.Where("release_id = ?", release.Id).Delete(&factory.MintRecord{}).Error; err != nil {
		return err
	}
	if err := tx.Where("plugin_id = ? AND (minted_release_id = ? OR effective_release_id = ?)", release.PluginId, release.Id, release.Id).
		Delete(&factory.UserOwnership{}).Error; err != nil {
		return err
	}
	if err := cleanupOwnerFactoryArtifacts(tx, ownerKeys); err != nil {
		return err
	}
	release.MintedCount = 0
	if release.Status == factory.ReleaseStatusSoldOut {
		release.Status = factory.ReleaseStatusPublished
	}
	return tx.Save(release).Error
}

func releaseOwnerKeys(tx *gorm.DB, releaseId int64) ([]string, error) {
	var ownerKeys []string
	if err := tx.Model(&factory.Asset{}).
		Where("release_id = ?", releaseId).
		Distinct("owner_key").
		Pluck("owner_key", &ownerKeys).Error; err != nil {
		return nil, err
	}
	return ownerKeys, nil
}

func releaseAssets(tx *gorm.DB, releaseId int64) ([]factory.Asset, error) {
	var assets []factory.Asset
	if err := tx.Where("release_id = ?", releaseId).Find(&assets).Error; err != nil {
		return nil, err
	}
	return assets, nil
}

func clearReleaseAssetRelations(tx *gorm.DB, assets []factory.Asset) error {
	assetIDs := make([]int64, 0, len(assets))
	ownerKeys := make([]string, 0, len(assets))
	seenOwnerKeys := map[string]struct{}{}
	for _, asset := range assets {
		assetIDs = append(assetIDs, asset.Id)
		if asset.OwnerKey == "" {
			continue
		}
		if _, ok := seenOwnerKeys[asset.OwnerKey]; ok {
			continue
		}
		ownerKeys = append(ownerKeys, asset.OwnerKey)
		seenOwnerKeys[asset.OwnerKey] = struct{}{}
	}
	if len(assetIDs) > 0 {
		if err := tx.Where("source_asset_id IN ? OR target_asset_id IN ?", assetIDs, assetIDs).
			Delete(&factory.AssetRelation{}).Error; err != nil {
			return err
		}
	}
	if len(ownerKeys) > 0 {
		return tx.Where("owner_key IN ?", ownerKeys).Delete(&factory.AssetRelation{}).Error
	}
	return nil
}

func removeMintedStaticFiles(assets []factory.Asset) error {
	pluginIDs := map[string]struct{}{}
	for _, asset := range assets {
		if err := os.Remove(factory.AssetStaticPath(asset.PluginId, asset.Id)); err != nil && !os.IsNotExist(err) {
			return err
		}
		tokenId := strings.TrimSpace(asset.TokenId)
		if tokenId != "" {
			if err := os.Remove(factory.MetadataStaticPath(asset.PluginId, tokenId)); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := os.Remove(factory.ProofStaticPath(asset.PluginId, tokenId)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		pluginID := strings.TrimSpace(asset.PluginId)
		if pluginID != "" {
			pluginIDs[pluginID] = struct{}{}
		}
	}

	for pluginID := range pluginIDs {
		if err := os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "assets", pluginID)); err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "metadata", pluginID)); err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(factory.FactoryStaticRoot(), "proofs", pluginID)); err != nil {
			return err
		}
	}
	return nil
}

func cleanupOwnerFactoryArtifacts(tx *gorm.DB, ownerKeys []string) error {
	normalizedOwnerKeys := normalizeOwnerKeys(ownerKeys)
	if len(normalizedOwnerKeys) == 0 {
		return nil
	}
	for _, ownerKey := range normalizedOwnerKeys {
		var assets []factory.Asset
		if err := tx.Where("owner_key = ? AND status = ?", ownerKey, factory.AssetStatusActive).
			Order("created_at DESC").
			Find(&assets).Error; err != nil {
			return err
		}
		var relations []factory.AssetRelation
		if err := tx.Where("owner_key = ? AND status = ?", ownerKey, factory.AssetRelationStatusActive).
			Order("created_at ASC").
			Find(&relations).Error; err != nil {
			return err
		}
		if len(assets) == 0 && len(relations) == 0 {
			if err := removeOwnerFactorySnapshotArtifacts(ownerKey); err != nil {
				return err
			}
			continue
		}
		if err := removeOwnerFactoryTransientArtifacts(ownerKey); err != nil {
			return err
		}
		stageDir, err := stageOwnerFactorySnapshots(ownerKey, assets, relations)
		if err != nil {
			return err
		}
		if err := activateOwnerFactorySnapshots(ownerKey, stageDir); err != nil {
			_ = os.RemoveAll(stageDir)
			return err
		}
	}
	return nil
}

func normalizeOwnerKeys(ownerKeys []string) []string {
	normalized := make([]string, 0, len(ownerKeys))
	seen := map[string]struct{}{}
	for _, ownerKey := range ownerKeys {
		trimmed := strings.TrimSpace(ownerKey)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

// 回滚已提交但后置静态步骤失败的单次铸造。
func rollbackCommittedMintArtifacts(ctx committedMintContext) error {
	if ctx.ReleaseId == 0 || ctx.MintRecordId == 0 {
		return nil
	}

	tx, err := db()
	if err != nil {
		return err
	}

	var mintedAssets []factory.Asset
	removeOwnerSnapshots := false
	err = tx.Transaction(func(tx *gorm.DB) error {
		var release factory.Release
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&release, "id = ?", ctx.ReleaseId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("关联发布记录不存在")
			}
			return err
		}
		if err := tx.Where("mint_record_id = ?", ctx.MintRecordId).Find(&mintedAssets).Error; err != nil {
			return err
		}
		if err := clearReleaseAssetRelations(tx, mintedAssets); err != nil {
			return err
		}

		collectionCounts := map[string]int64{}
		for _, asset := range mintedAssets {
			if err := tx.Model(&factory.NFTInventoryItem{}).
				Where("asset_id = ?", asset.Id).
				Updates(map[string]any{
					"status":         factory.NFTInventoryItemStatusAvailable,
					"asset_id":       nil,
					"token_id":       "",
					"mint_record_id": nil,
					"owner_key":      "",
					"minted_at":      nil,
					"metadata_uri":   "",
					"proof_uri":      "",
				}).Error; err != nil {
				return err
			}
			if asset.CollectionKey != "" {
				collectionCounts[asset.CollectionKey] += 1
			}
		}
		for collectionKey, count := range collectionCounts {
			if err := tx.Model(&factory.NFTInventoryPool{}).
				Where("release_id = ? AND collection_key = ?", ctx.ReleaseId, collectionKey).
				UpdateColumn("minted_count", gorm.Expr("GREATEST(minted_count - ?, 0)", count)).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("mint_record_id = ?", ctx.MintRecordId).Delete(&factory.Asset{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&factory.MintRecord{}, "id = ?", ctx.MintRecordId).Error; err != nil {
			return err
		}

		var remainingPluginAssets int64
		if err := tx.Model(&factory.Asset{}).
			Where("owner_key = ? AND plugin_id = ? AND status = ?", ctx.OwnerKey, ctx.PluginId, factory.AssetStatusActive).
			Count(&remainingPluginAssets).Error; err != nil {
			return err
		}
		if remainingPluginAssets == 0 {
			if err := tx.Where("user_id = ? AND plugin_id = ?", ctx.UserId, ctx.PluginId).
				Delete(&factory.UserOwnership{}).Error; err != nil {
				return err
			}
		}

		rolledBackCount := int64(len(mintedAssets))
		if release.MintedCount >= rolledBackCount {
			release.MintedCount -= rolledBackCount
		} else {
			release.MintedCount = 0
		}
		if release.Status == factory.ReleaseStatusSoldOut && release.MintedCount < release.TotalSupply {
			release.Status = factory.ReleaseStatusPublished
		}
		if err := tx.Save(&release).Error; err != nil {
			return err
		}
		var remainingOwnerAssets int64
		if err := tx.Model(&factory.Asset{}).
			Where("owner_key = ? AND status = ?", ctx.OwnerKey, factory.AssetStatusActive).
			Count(&remainingOwnerAssets).Error; err != nil {
			return err
		}
		var remainingOwnerRelations int64
		if err := tx.Model(&factory.AssetRelation{}).
			Where("owner_key = ? AND status = ?", ctx.OwnerKey, factory.AssetRelationStatusActive).
			Count(&remainingOwnerRelations).Error; err != nil {
			return err
		}
		removeOwnerSnapshots = remainingOwnerAssets == 0 && remainingOwnerRelations == 0
		return nil
	})
	if err != nil {
		return err
	}

	if err := removeMintedStaticFiles(mintedAssets); err != nil {
		return err
	}
	for _, asset := range mintedAssets {
		if err := ds_service.DeleteFactPluginAssetArtifacts(asset.Id); err != nil {
			return err
		}
	}
	if removeOwnerSnapshots {
		return removeOwnerFactorySnapshotArtifacts(ctx.OwnerKey)
	}
	if err := removeOwnerFactoryTransientArtifacts(ctx.OwnerKey); err != nil {
		return err
	}
	return rebuildOwnerFactorySnapshots(ctx.OwnerKey)
}

func removeOwnerFactorySnapshotArtifacts(ownerKey string) error {
	finalDir := filepath.Dir(factory.OwnerIndexStaticPath(ownerKey))
	legacyStageDir := factory.OwnerIndexStaticPath(ownerKey) + ".staging"
	if err := os.RemoveAll(finalDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(finalDir + ".staging"); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(finalDir + ".previous"); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(legacyStageDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func removeOwnerFactoryTransientArtifacts(ownerKey string) error {
	finalDir := filepath.Dir(factory.OwnerIndexStaticPath(ownerKey))
	legacyStageDir := factory.OwnerIndexStaticPath(ownerKey) + ".staging"
	if err := os.RemoveAll(finalDir + ".staging"); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(finalDir + ".previous"); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(legacyStageDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func requireReleaseFreezeOperator(user security.JwtUser, pluginId string) error {
	if user.Id == 0 {
		return newParameterError("用户ID不能为空")
	}
	tooling, ok := pluginTooling(pluginId)
	if !ok || len(tooling.FreezeOperators) == 0 {
		return newParameterError("当前插件未配置冻结操作权限")
	}
	userAddr := strings.ToLower(strings.TrimSpace(user.Addr))
	for _, operatorAddr := range tooling.FreezeOperators {
		if userAddr == strings.ToLower(strings.TrimSpace(operatorAddr)) {
			return nil
		}
	}
	return newForbiddenError("无权限操作")
}

func activateStagedReleaseSnapshot(release factory.Release, stagingDir string) (string, bool, error) {
	if stagingDir == "" {
		return "", false, nil
	}
	backupDir, err := factory.ActivateReleaseStaticSnapshot(release, stagingDir)
	if err != nil {
		return "", false, err
	}
	return backupDir, true, nil
}

func rollbackFreezeStaticSnapshot(release factory.Release, stagingDir string, backupDir string, activatedSnapshot bool) {
	if activatedSnapshot {
		_ = factory.RollbackActivatedReleaseStaticSnapshot(release, backupDir)
	}
	if stagingDir != "" {
		_ = factory.CleanupReleaseStaticStagingDir(stagingDir)
	}
}

func commitFreezeStaticSnapshot(backupDir string, activatedSnapshot bool) {
	if activatedSnapshot {
		_ = factory.CommitActivatedReleaseStaticSnapshot(backupDir)
	}
}

// 按 release 静态快照和 asset.meta.json 冻结库存。
func freezeReleaseAssets(tx *gorm.DB, release factory.Release, expectedBatch ...string) (*FreezeReleaseAssetsResponse, string, error) {
	if release.Status != factory.ReleaseStatusPublished && release.Status != factory.ReleaseStatusPaused {
		return nil, "", newConflictError("当前发布记录不可冻结")
	}
	if defaultBuildStatus(release.BuildStatus) != factory.BuildStatusReady {
		return nil, "", newConflictError("当前发布记录构建未完成")
	}

	var existingPools []factory.NFTInventoryPool
	if err := tx.Where("release_id = ?", release.Id).
		Order("collection_key ASC").
		Find(&existingPools).Error; err != nil {
		return nil, "", err
	}
	stagingDir, err := stageReleaseGeneratorBatch(release, expectedBatch...)
	if err != nil {
		return nil, stagingDir, err
	}
	valueTemplate, err := loadReleaseMintTemplateFromDir(stagingDir)
	if err != nil {
		return nil, stagingDir, err
	}

	if len(existingPools) > 0 {
		// 先对比前后发布快照的库存源签名，只要元数据文件和供给结构没变，就只同步价格模板。
		currentSignatures, err := collectReleaseInventorySourceSignatures(valueTemplate, stagingDir)
		if err != nil {
			return nil, stagingDir, err
		}
		previousTemplate, err := loadReleaseMintTemplate(release)
		if err != nil {
			return nil, stagingDir, err
		}
		previousSignatures, err := collectReleaseInventorySourceSignatures(previousTemplate, factory.ReleaseStaticDir(release))
		if err != nil {
			return nil, stagingDir, err
		}
		changed, changedKeys := collectionSignatureChanged(previousSignatures, currentSignatures)
		// 尚未铸造的旧库存允许显式升级证明；历史铸造记录保留原根。
		if !changed {
			for _, pool := range existingPools {
				if pool.MerkleRoot != pool.CollectionHash {
					continue
				}
				hasMinted, err := releaseHasMintedRecords(tx, release, existingPools)
				if err != nil {
					return nil, stagingDir, err
				}
				if !hasMinted {
					changed = true
					changedKeys = append(changedKeys, "proof-version")
				}
				break
			}
		}
		if !changed {
			pools, err := collectReleaseInventoryPools(tx, release)
			if err != nil {
				return nil, stagingDir, err
			}
			if err := writeInventoryCommitment(stagingDir, release, pools); err != nil {
				return nil, stagingDir, err
			}
			return freezeReleaseResponse(release, "unchanged", "价格模板已同步，库存结构未变动", pools), stagingDir, nil
		}

		hasMinted, err := releaseHasMintedRecords(tx, release, existingPools)
		if err != nil {
			return nil, stagingDir, err
		}
		if hasMinted {
			if err := factory.CleanupReleaseStaticStagingDir(stagingDir); err != nil {
				return nil, stagingDir, err
			}
			pools, err := collectReleaseInventoryPools(tx, release)
			if err != nil {
				return nil, "", err
			}
			return freezeReleaseResponse(release, "blocked_minted", "资产集合已变动，铸造记录已生成，无法重新生成库存："+strings.Join(changedKeys, "、"), pools), "", nil
		}
	}

	if len(existingPools) > 0 {
		if err := clearReleaseInventory(tx, release.Id); err != nil {
			return nil, stagingDir, err
		}
	}

	if err := ensureReleaseInventory(tx, release, valueTemplate, stagingDir); err != nil {
		return nil, stagingDir, err
	}

	pools, err := collectReleaseInventoryPools(tx, release)
	if err != nil {
		return nil, stagingDir, err
	}
	status := "ready"
	if err := writeInventoryCommitment(stagingDir, release, pools); err != nil {
		return nil, stagingDir, err
	}
	message := "发布资产已冻结，库存池已准备完成"
	if len(existingPools) > 0 {
		status = "rebuilt"
		message = "资产集合已变动，已清空旧库存并重新冻结"
	}

	return freezeReleaseResponse(release, status, message, pools), stagingDir, nil
}

// 用于判断库存是否需要重建的签名。
// 只包含会影响库存内容的字段，不包含价格和 mintLimit。
type inventorySourceSignature struct {
	// 展示名参与冻结 metadata 哈希，变更时必须重建库存。
	Label string `json:"label"`
	// 集合业务键。
	CollectionKey string `json:"collectionKey"`
	// NFT 资产类型。
	AssetKind string `json:"assetKind"`
	// 组件角色。
	ComponentRole string `json:"componentRole,omitempty"`
	// 父组件 collection key。
	ParentKey string `json:"parentKey,omitempty"`
	// 模板声明的 metadataRef。
	MetadataRef string `json:"metadataRef"`
	// 属性哈希字段名。
	TraitHashField string `json:"traitHashField"`
	// 库存发放策略。 对应 NFTInventoryStrategy
	Strategy string `json:"strategy"`
	// metadata 源文件内容聚合哈希。
	MetadataHash string `json:"metadataHash"`
	// 各等级的供给签名。
	TierSupplies []inventoryTierSupplySignature `json:"tierSupplies,omitempty"`
}

// 记录 tierConfig 中会影响库存结构的供给配置。
type inventoryTierSupplySignature struct {
	// 等级标识。
	Tier string `json:"tier"`
	// 等级供给数量。
	Supply int64 `json:"supply"`
}

// 汇总当前快照中每个 collection 的库存源签名。
func collectReleaseInventorySourceSignatures(valueTemplate assetValueTemplate, snapshotDir string) (map[string]string, error) {
	fileHashes := map[string]string{}
	result := make(map[string]string, len(valueTemplate.Collections))
	seal, err := os.ReadFile(filepath.Join(snapshotDir, "generator-batch", "seal.json"))
	if err == nil {
		result["generator-batch"] = sha256Hex(seal)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for _, collection := range valueTemplate.Collections {
		collectionKey := strings.TrimSpace(collection.Key)
		if collectionKey == "" {
			return nil, newParameterError("资产集合缺少 key")
		}
		signature, err := buildInventorySourceSignature(collection, snapshotDir, fileHashes)
		if err != nil {
			return nil, err
		}
		signature.CollectionKey = collectionKey
		data, err := json.Marshal(signature)
		if err != nil {
			return nil, err
		}
		result[collectionKey] = sha256Hex(data)
	}
	return result, nil
}

// 构建单个 collection 的库存源签名。
func buildInventorySourceSignature(
	collection assetValueCollection,
	snapshotDir string,
	fileHashes map[string]string,
) (inventorySourceSignature, error) {
	assetKind, err := resolveCollectionAssetKind(collection)
	if err != nil {
		return inventorySourceSignature{}, err
	}
	componentRole, err := resolveCollectionComponentRole(collection)
	if err != nil {
		return inventorySourceSignature{}, err
	}
	metadataRefs, err := resolveInventorySignatureMetadataRefs(collection)
	if err != nil {
		return inventorySourceSignature{}, err
	}
	metadataHashes := make([]string, 0, len(metadataRefs))
	for _, metadataRef := range metadataRefs {
		file, _, _, err := parseMetadataRef(metadataRef)
		if err != nil {
			return inventorySourceSignature{}, err
		}
		hash, err := hashInventorySignatureFile(snapshotDir, file, fileHashes)
		if err != nil {
			return inventorySourceSignature{}, err
		}
		metadataHashes = append(metadataHashes, metadataRef+"@"+hash)
	}
	sort.Strings(metadataHashes)

	signature := inventorySourceSignature{
		Label:          collectionDisplayName(collection),
		AssetKind:      string(assetKind),
		ComponentRole:  string(componentRole),
		ParentKey:      strings.TrimSpace(collection.ParentKey),
		MetadataRef:    strings.TrimSpace(collection.MetadataRef),
		TraitHashField: strings.TrimSpace(collection.TraitHashField),
		Strategy:       string(inventoryStrategyForCollection(collection)),
		MetadataHash:   sha256Hex([]byte(strings.Join(metadataHashes, "|"))),
	}
	if len(collection.TierConfig) > 0 {
		signature.TierSupplies = make([]inventoryTierSupplySignature, 0, len(collection.TierConfig))
		for _, tier := range sortedTierKeys(collection.TierConfig) {
			config := collection.TierConfig[tier]
			signature.TierSupplies = append(signature.TierSupplies, inventoryTierSupplySignature{
				Tier:   tier,
				Supply: config.Supply,
			})
		}
	}
	return signature, nil
}

// 展开 collection 依赖的 metadataRef 列表。
func resolveInventorySignatureMetadataRefs(collection assetValueCollection) ([]string, error) {
	if len(collection.TierConfig) > 0 && strings.Contains(collection.MetadataRef, "{tier}") {
		refs := make([]string, 0, len(collection.TierConfig))
		for _, tier := range sortedTierKeys(collection.TierConfig) {
			refs = append(refs, strings.ReplaceAll(strings.TrimSpace(collection.MetadataRef), "{tier}", tier))
		}
		return refs, nil
	}
	metadataRef := strings.TrimSpace(collection.MetadataRef)
	if metadataRef == "" {
		return nil, newParameterError(collectionDisplayName(collection) + "缺少 metadataRef")
	}
	return []string{metadataRef}, nil
}

// 读取并缓存 metadataRef 对应文件的字节哈希。
func hashInventorySignatureFile(snapshotDir string, file string, fileHashes map[string]string) (string, error) {
	if hash, ok := fileHashes[file]; ok {
		return hash, nil
	}
	data, err := os.ReadFile(filepath.Join(snapshotDir, file))
	if err != nil {
		return "", err
	}
	hash := sha256Hex(data)
	fileHashes[file] = hash
	return hash, nil
}

// 对比前后快照的 collection 源签名。
func collectionSignatureChanged(previous map[string]string, current map[string]string) (bool, []string) {
	seen := map[string]struct{}{}
	changedKeys := []string{}
	for key, previousHash := range previous {
		seen[key] = struct{}{}
		if current[key] != previousHash {
			changedKeys = append(changedKeys, key)
		}
	}
	for key := range current {
		if _, ok := seen[key]; !ok {
			changedKeys = append(changedKeys, key)
		}
	}
	sort.Strings(changedKeys)
	return len(changedKeys) > 0, changedKeys
}

func freezeReleaseResponse(release factory.Release, status string, message string, pools []FreezeReleaseInventoryPool) *FreezeReleaseAssetsResponse {
	return &FreezeReleaseAssetsResponse{
		ReleaseId:    strconv.FormatInt(release.Id, 10),
		PluginId:     release.PluginId,
		Version:      release.Version,
		Status:       status,
		Message:      message,
		ReleaseUrl:   factory.ReleaseStaticURL(release),
		AssetMetaUrl: releaseStaticFileURL(release, "asset.meta.json"),
		Pools:        pools,
	}
}

func collectionHashesChanged(existingPools []factory.NFTInventoryPool, currentHashes map[string]string) (bool, []string) {
	seen := map[string]struct{}{}
	changedKeys := []string{}
	for _, pool := range existingPools {
		key := strings.TrimSpace(pool.CollectionKey)
		seen[key] = struct{}{}
		if currentHashes[key] != strings.TrimSpace(pool.CollectionHash) {
			changedKeys = append(changedKeys, key)
		}
	}
	for key := range currentHashes {
		if _, ok := seen[key]; !ok {
			changedKeys = append(changedKeys, key)
		}
	}
	sort.Strings(changedKeys)
	return len(changedKeys) > 0, changedKeys
}

func releaseHasMintedRecords(tx *gorm.DB, release factory.Release, pools []factory.NFTInventoryPool) (bool, error) {
	if release.MintedCount > 0 {
		return true, nil
	}
	for _, pool := range pools {
		if pool.MintedCount > 0 {
			return true, nil
		}
	}

	var count int64
	if err := tx.Model(&factory.MintRecord{}).Where("release_id = ?", release.Id).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	if err := tx.Model(&factory.Asset{}).Where("release_id = ?", release.Id).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func clearReleaseInventory(tx *gorm.DB, releaseId int64) error {
	_, _, err := clearReleaseInventoryWithCount(tx, releaseId)
	return err
}

func clearReleaseInventoryWithCount(tx *gorm.DB, releaseId int64) (int64, int64, error) {
	itemResult := tx.Where("release_id = ?", releaseId).Delete(&factory.NFTInventoryItem{})
	if itemResult.Error != nil {
		return 0, 0, itemResult.Error
	}
	poolResult := tx.Where("release_id = ?", releaseId).Delete(&factory.NFTInventoryPool{})
	if poolResult.Error != nil {
		return 0, 0, poolResult.Error
	}
	return itemResult.RowsAffected, poolResult.RowsAffected, nil
}

func releaseInventoryPools(tx *gorm.DB, release factory.Release) ([]factory.NFTInventoryPool, error) {
	var pools []factory.NFTInventoryPool
	if err := tx.Where("release_id = ?", release.Id).
		Order("collection_key ASC").
		Find(&pools).Error; err != nil {
		return nil, err
	}
	return pools, nil
}

func removeReleaseFreezeStaticFiles(release factory.Release) error {
	if err := os.RemoveAll(factory.ReleaseStaticDir(release)); err != nil {
		return err
	}
	if err := os.RemoveAll(factory.ReleaseStaticStagingDir(release)); err != nil {
		return err
	}
	if err := os.RemoveAll(factory.ReleaseStaticDir(release) + ".previous"); err != nil {
		return err
	}

	finalPluginDir := filepath.Dir(factory.ReleaseStaticDir(release))
	if err := pruneEmptyDirs(finalPluginDir, filepath.Dir(finalPluginDir)); err != nil {
		return err
	}

	stagingPluginDir := filepath.Dir(factory.ReleaseStaticStagingDir(release))
	return pruneEmptyDirs(stagingPluginDir, filepath.Dir(stagingPluginDir))
}

func removeReleaseBuildArtifactFiles(release factory.Release) error {
	sourceDir := getPluginSourceSnapshotRoot(release.PluginId, release.Id)
	if err := os.RemoveAll(sourceDir); err != nil {
		return err
	}
	if err := pruneEmptyDirs(filepath.Dir(sourceDir), filepath.Dir(filepath.Dir(sourceDir))); err != nil {
		return err
	}

	runtimeDir := getPluginRuntimeReleaseRoot(release.PluginId, release.Version, release.Id)
	if err := os.RemoveAll(runtimeDir); err != nil {
		return err
	}
	return pruneEmptyDirs(filepath.Dir(runtimeDir), filepath.Dir(filepath.Dir(runtimeDir)))
}

func pruneEmptyDirs(startDir string, stopDir string) error {
	current := filepath.Clean(startDir)
	stop := filepath.Clean(stopDir)

	for current != stop {
		entries, err := os.ReadDir(current)
		if err != nil {
			if os.IsNotExist(err) {
				parent := filepath.Dir(current)
				if parent == current {
					return nil
				}
				current = parent
				continue
			}
			return err
		}
		if len(entries) > 0 {
			return nil
		}
		if err := os.Remove(current); err != nil && !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
	return nil
}

// 返回发布快照内文件的静态 URL。
func releaseStaticFileURL(release factory.Release, file string) string {
	return factory.FactoryStaticURL(
		"releases",
		release.PluginId,
		fmt.Sprintf("%s-%d", release.Version, release.Id),
		file,
	)
}

// 收集发布库存池摘要。
func collectReleaseInventoryPools(tx *gorm.DB, release factory.Release) ([]FreezeReleaseInventoryPool, error) {
	var pools []factory.NFTInventoryPool
	if err := tx.Where("release_id = ?", release.Id).
		Order("collection_key ASC").
		Find(&pools).Error; err != nil {
		return nil, err
	}

	result := make([]FreezeReleaseInventoryPool, 0, len(pools))
	for _, pool := range pools {
		result = append(result, FreezeReleaseInventoryPool{
			CollectionKey:  pool.CollectionKey,
			AssetKind:      pool.AssetKind,
			MetadataRef:    pool.MetadataRef,
			Strategy:       pool.Strategy,
			TotalSupply:    pool.TotalSupply,
			MintedCount:    pool.MintedCount,
			Status:         pool.Status,
			CollectionHash: pool.CollectionHash,
			MerkleRoot:     pool.MerkleRoot,
		})
	}
	return result, nil
}

// 确保用户拥有该插件的权益记录。
func ensurePluginOwnership(tx *gorm.DB, userId uint64, release factory.Release) error {
	var ownership factory.UserOwnership
	err := tx.Where("user_id = ? AND plugin_id = ?", userId, release.PluginId).First(&ownership).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil {
		return nil
	}
	ownership = factory.UserOwnership{
		Id:                 generateID(),
		UserId:             userId,
		PluginId:           release.PluginId,
		MintedReleaseId:    release.Id,
		EffectiveReleaseId: release.Id,
	}
	return tx.Create(&ownership).Error
}

// 读取发布快照中的铸造模板。
func loadReleaseMintTemplate(release factory.Release) (assetValueTemplate, error) {
	return loadReleaseMintTemplateFromDir(factory.ReleaseStaticDir(release))
}

func loadReleaseMintTemplateIfExists(release factory.Release) (assetValueTemplate, bool, error) {
	valueTemplate, err := loadReleaseMintTemplate(release)
	if err != nil {
		if os.IsNotExist(err) {
			return valueTemplate, false, nil
		}
		return valueTemplate, false, err
	}
	if len(valueTemplate.Collections) == 0 {
		return valueTemplate, false, nil
	}
	return valueTemplate, true, nil
}

func loadReleaseMintTemplateFromDir(dir string) (assetValueTemplate, error) {
	var valueTemplate assetValueTemplate
	if err := readJSONFile(filepath.Join(dir, "asset.meta.json"), &valueTemplate); err != nil {
		return valueTemplate, err
	}
	if err := validateAssetValueTemplate(valueTemplate); err != nil {
		return valueTemplate, err
	}
	return valueTemplate, nil
}

// 校验 asset.meta.json 中的通用组件声明。
func validateAssetValueTemplate(valueTemplate assetValueTemplate) error {
	// 先收集所有 root，第二轮再校验 child 的 parentKey 是否能对上。
	roots := map[string]struct{}{}
	for _, collection := range valueTemplate.Collections {
		if _, err := resolveCollectionAssetKind(collection); err != nil {
			return err
		}
		role, err := resolveCollectionComponentRole(collection)
		if err != nil {
			return err
		}
		if role == factory.ComponentRoleRoot {
			roots[strings.TrimSpace(collection.Key)] = struct{}{}
		}
	}
	for _, collection := range valueTemplate.Collections {
		if collection.ComponentRole != factory.ComponentRoleChild {
			continue
		}
		parentKey := strings.TrimSpace(collection.ParentKey)
		if _, ok := roots[parentKey]; !ok {
			return newParameterError(collectionDisplayName(collection) + " parentKey 未指向 root 组件")
		}
	}
	return nil
}

// 读取 JSON 文件到目标结构。
func readJSONFile(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// 校验铸造输入并计算待生成资产。
func resolveMintSelection(
	tx *gorm.DB,
	release factory.Release,
	valueTemplate assetValueTemplate,
	inputs map[string]map[string]int64,
) (mintSelectionResult, error) {
	// 复杂资产先确保库存池已冻结，后续计价和发放都只基于库存执行。
	if err := requireReleaseInventory(tx, release, valueTemplate); err != nil {
		return mintSelectionResult{}, err
	}

	total := new(big.Rat)
	var result mintSelectionResult
	for _, collection := range valueTemplate.Collections {
		collectionKey := strings.TrimSpace(collection.Key)
		if collectionKey == "" {
			return mintSelectionResult{}, newParameterError("资产集合缺少 key")
		}
		if strings.TrimSpace(collection.MetadataRef) == "" {
			return mintSelectionResult{}, newParameterError(collectionDisplayName(collection) + "缺少 metadataRef")
		}
		counts := inputs[collectionKey]
		if len(counts) == 0 {
			continue
		}
		assetKind, err := resolveCollectionAssetKind(collection)
		if err != nil {
			return mintSelectionResult{}, err
		}
		if _, err := resolveCollectionComponentRole(collection); err != nil {
			return mintSelectionResult{}, err
		}

		if len(collection.TierConfig) > 0 {
			if err := appendTieredSelection(tx, release, &result, total, collection, assetKind, counts); err != nil {
				return mintSelectionResult{}, err
			}
			continue
		}
		if err := appendFlatSelection(tx, release, &result, total, collection, assetKind, counts); err != nil {
			return mintSelectionResult{}, err
		}
	}
	if result.TotalCount <= 0 {
		return mintSelectionResult{}, newParameterError("请选择要铸造的资产数量")
	}
	result.ExpectedPaid = normalizeDecimal(total.FloatString(18))
	return result, nil
}

func resolveSimpleMintSelection(
	release factory.Release,
	inputs map[string]map[string]int64,
) (mintSelectionResult, error) {
	count := int64(0)
	for _, counts := range inputs {
		for _, value := range counts {
			count += value
		}
	}
	if count <= 0 {
		return mintSelectionResult{}, newParameterError("请选择要铸造的资产数量")
	}
	if count > release.MintPer {
		return mintSelectionResult{}, newParameterError("铸造数量不能超过单次最大铸造量")
	}
	unit, err := parsePriceRat(release.MintPrice, "基础价格")
	if err != nil {
		return mintSelectionResult{}, err
	}
	total := new(big.Rat).Mul(unit, big.NewRat(count, 1))
	items := make([]mintSelectionItem, 0, count)
	templateRef := "release.json"
	itemId := strings.TrimSpace(release.PluginId)
	if itemId == "" {
		itemId = "default"
	}
	for i := int64(0); i < count; i++ {
		items = append(items, mintSelectionItem{
			AssetKind:   factory.AssetKindWhole,
			TemplateRef: templateRef,
			ItemId:      itemId,
		})
	}
	return mintSelectionResult{
		Items:        items,
		ExpectedPaid: normalizeDecimal(total.FloatString(18)),
		TotalCount:   count,
	}, nil
}

// 返回集合的展示名称。
func collectionDisplayName(collection assetValueCollection) string {
	label := strings.TrimSpace(collection.Label)
	if label != "" {
		return label
	}
	if key := strings.TrimSpace(collection.Key); key != "" {
		return key
	}
	return strings.TrimSpace(collection.MetadataRef)
}

// 校验并返回集合资产类型。
func resolveCollectionAssetKind(collection assetValueCollection) (factory.AssetKind, error) {
	switch collection.AssetKind {
	case factory.AssetKindWhole, factory.AssetKindComponent:
		return collection.AssetKind, nil
	default:
		return "", newParameterError(collectionDisplayName(collection) + "缺少资产类型")
	}
}

// 校验并返回组件角色。
func resolveCollectionComponentRole(collection assetValueCollection) (factory.ComponentRole, error) {
	// whole 资产不参与组合关系，不能再带组件字段。
	if collection.AssetKind != factory.AssetKindComponent {
		if collection.ComponentRole != "" {
			return "", newParameterError(collectionDisplayName(collection) + "整件资产不能配置 componentRole")
		}
		if strings.TrimSpace(collection.ParentKey) != "" {
			return "", newParameterError(collectionDisplayName(collection) + "整件资产不能配置 parentKey")
		}
		return "", nil
	}
	switch collection.ComponentRole {
	case factory.ComponentRoleRoot:
		if strings.TrimSpace(collection.ParentKey) != "" {
			return "", newParameterError(collectionDisplayName(collection) + "根组件不能配置 parentKey")
		}
		return collection.ComponentRole, nil
	case factory.ComponentRoleChild:
		if strings.TrimSpace(collection.ParentKey) == "" {
			return "", newParameterError(collectionDisplayName(collection) + "子组件缺少 parentKey")
		}
		return collection.ComponentRole, nil
	default:
		return "", newParameterError(collectionDisplayName(collection) + "缺少组件角色")
	}
}

// 追加按等级计价的资产选择项。
func appendTieredSelection(
	tx *gorm.DB,
	release factory.Release,
	result *mintSelectionResult,
	total *big.Rat,
	collection assetValueCollection,
	assetKind factory.AssetKind,
	counts map[string]int64,
) error {
	for tier, count := range counts {
		tier = strings.TrimSpace(tier)
		if tier == "" || count <= 0 {
			continue
		}
		tierConfig, ok := collection.TierConfig[tier]
		if !ok {
			return newParameterError("未知等级: " + tier)
		}
		if isDisabledTierPrice(tierConfig.Price) || tierConfig.MintLimit == 0 {
			return newParameterError(fmt.Sprintf("%s等级 %s 暂未开放铸造", collectionDisplayName(collection), tier))
		}
		if tierConfig.MintLimit > 0 && count > tierConfig.MintLimit {
			return newParameterError(fmt.Sprintf("%s等级 %s 不能超过 %d", collectionDisplayName(collection), tier, tierConfig.MintLimit))
		}
		// 等级价格只负责计价和开放状态，实际发放内容仍从冻结库存里取。
		unit, err := parsePriceRat(tierConfig.Price, tier+" 单价")
		if err != nil {
			return err
		}
		total.Add(total, new(big.Rat).Mul(unit, big.NewRat(count, 1)))
		items, err := claimInventoryItems(tx, release, collection, assetKind, tier, count)
		if err != nil {
			return err
		}
		appendSelectedInventoryItems(result, collection, assetKind, items)
	}
	return nil
}

// 判断等级价格是否表示禁用。
func isDisabledTierPrice(price string) bool {
	return strings.TrimSpace(price) == "-"
}

// 追加不分等级计价的资产选择项。
func appendFlatSelection(
	tx *gorm.DB,
	release factory.Release,
	result *mintSelectionResult,
	total *big.Rat,
	collection assetValueCollection,
	assetKind factory.AssetKind,
	counts map[string]int64,
) error {
	var count int64
	for _, itemCount := range counts {
		if itemCount > 0 {
			count += itemCount
		}
	}
	if count <= 0 {
		return nil
	}
	// 不分等级 collection 统一走 unitPrice，并从可复用模板中随机挑选。
	unit, err := parsePriceRat(collection.UnitPrice, collectionDisplayName(collection)+"单价")
	if err != nil {
		return err
	}
	total.Add(total, new(big.Rat).Mul(unit, big.NewRat(count, 1)))
	items, err := pickReusableInventoryItems(tx, release, collection, assetKind, count)
	if err != nil {
		return err
	}
	appendSelectedInventoryItems(result, collection, assetKind, items)
	return nil
}

// 元数据导入后的库存项摘要。
type inventorySelectionItem struct {
	// 冻结库存项 ID。
	Id int64
	// 模板项 ID。
	ItemId string
	// 模板项稳定序号。
	ItemIndex int64
	// 稀有度等级。
	Tier string
	// 冻结后的属性哈希。
	TraitHash string
	// 实际落到库存项上的模板引用。
	MetadataRef string
}

// 发布后生成通用 NFT 库存池。
func ensureReleaseInventory(tx *gorm.DB, release factory.Release, valueTemplate assetValueTemplate, snapshotDir string) error {
	for _, collection := range valueTemplate.Collections {
		if err := ensureCollectionInventory(tx, release, collection, snapshotDir); err != nil {
			return err
		}
	}
	return nil
}

// 确保发布库存池已由发布冻结流程准备好。
func requireReleaseInventory(tx *gorm.DB, release factory.Release, valueTemplate assetValueTemplate) error {
	for _, collection := range valueTemplate.Collections {
		collectionKey := strings.TrimSpace(collection.Key)
		if collectionKey == "" {
			return newParameterError("资产集合缺少 key")
		}

		var pool factory.NFTInventoryPool
		err := tx.First(&pool, "release_id = ? AND collection_key = ?", release.Id, collectionKey).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return newConflictError("发布库存未准备完成，请先执行发布冻结")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// 确保单个 collection 已导入库存池。
func ensureCollectionInventory(tx *gorm.DB, release factory.Release, collection assetValueCollection, snapshotDir string) error {
	collectionKey := strings.TrimSpace(collection.Key)
	if collectionKey == "" {
		return newParameterError("资产集合缺少 key")
	}
	if strings.TrimSpace(collection.MetadataRef) == "" {
		return newParameterError(collectionDisplayName(collection) + "缺少 metadataRef")
	}
	assetKind, err := resolveCollectionAssetKind(collection)
	if err != nil {
		return err
	}
	if _, err := resolveCollectionComponentRole(collection); err != nil {
		return err
	}

	var existing factory.NFTInventoryPool
	err = tx.First(&existing, "release_id = ? AND collection_key = ?", release.Id, collectionKey).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	items, err := resolveInventoryMetadataItems(release, collection, snapshotDir)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return newConflictError(collectionDisplayName(collection) + " metadata item 为空")
	}
	leaves := make([]string, len(items))
	for index, item := range items {
		leaves[index] = item.LeafHash
	}
	root, proofs, err := merkle.Build(leaves)
	if err != nil {
		return err
	}
	now := time.Now()
	pool := factory.NFTInventoryPool{
		Id:             generateID(),
		PluginId:       release.PluginId,
		ReleaseId:      release.Id,
		CollectionKey:  collectionKey,
		AssetKind:      assetKind,
		MetadataRef:    strings.TrimSpace(collection.MetadataRef),
		Strategy:       inventoryStrategyForCollection(collection),
		TotalSupply:    int64(len(items)),
		Status:         factory.NFTInventoryPoolStatusActive,
		CollectionHash: hashInventoryCollection(items),
		MerkleRoot:     root,
		GeneratedAt:    &now,
		FrozenAt:       &now,
	}
	if err := tx.Create(&pool).Error; err != nil {
		return err
	}

	shuffleIndexes, err := createShuffleIndexes(len(items))
	if err != nil {
		return err
	}
	dbItems := make([]factory.NFTInventoryItem, 0, len(items))
	for index, item := range items {
		proofJSON, err := json.Marshal(proofs[index])
		if err != nil {
			return err
		}
		dbItems = append(dbItems, factory.NFTInventoryItem{
			Id:            generateID(),
			PoolId:        pool.Id,
			PluginId:      release.PluginId,
			ReleaseId:     release.Id,
			CollectionKey: collectionKey,
			AssetKind:     assetKind,
			ItemId:        item.ItemId,
			ItemIndex:     item.ItemIndex,
			Tier:          item.Tier,
			TraitHash:     item.TraitHash,
			ShuffleIndex:  shuffleIndexes[index],
			MetadataHash:  item.MetadataHash,
			LeafHash:      item.LeafHash,
			ProofJson:     string(proofJSON),
			Status:        factory.NFTInventoryItemStatusAvailable,
			MetadataUri:   factory.ItemMetadataStaticURL(release.PluginId, collectionKey, item.ItemId),
			ProofUri:      factory.ItemProofStaticURL(release.PluginId, collectionKey, item.ItemId),
		})
	}
	return tx.CreateInBatches(dbItems, 500).Error
}

// 单个 metadata item 的标准化信息。
type inventoryMetadataItem struct {
	// 原始模板项内容。
	Item map[string]any
	// 模板项 ID。
	ItemId string
	// 模板项稳定序号。
	ItemIndex int64
	// 稀有度等级。
	Tier string
	// 冻结后的属性哈希。
	TraitHash string
	// 模板项来源引用。
	MetadataRef string
	// 标准化 metadata 哈希。
	MetadataHash string
	// 集合 leaf 哈希。
	LeafHash string
}

// 解析 collection 的 metadataRef 并校验 item。
func resolveInventoryMetadataItems(release factory.Release, collection assetValueCollection, snapshotDir string) ([]inventoryMetadataItem, error) {
	if len(collection.TierConfig) > 0 && strings.Contains(collection.MetadataRef, "{tier}") {
		return resolveTieredInventoryMetadataItems(release, collection, snapshotDir)
	}
	items, err := loadMetadataRefItems(snapshotDir, strings.TrimSpace(collection.MetadataRef))
	if err != nil {
		return nil, err
	}
	return normalizeInventoryMetadataItems(release, collection, strings.TrimSpace(collection.MetadataRef), items, "")
}

// 按 tierConfig 展开 metadataRef。
func resolveTieredInventoryMetadataItems(release factory.Release, collection assetValueCollection, snapshotDir string) ([]inventoryMetadataItem, error) {
	var result []inventoryMetadataItem
	for _, tier := range sortedTierKeys(collection.TierConfig) {
		ref := strings.ReplaceAll(strings.TrimSpace(collection.MetadataRef), "{tier}", tier)
		items, err := loadMetadataRefItems(snapshotDir, ref)
		if err != nil {
			return nil, err
		}
		normalized, err := normalizeInventoryMetadataItems(release, collection, ref, items, tier)
		if err != nil {
			return nil, err
		}
		tierConfig := collection.TierConfig[tier]
		if tierConfig.Supply > 0 && int64(len(normalized)) != tierConfig.Supply {
			return nil, newConflictError(fmt.Sprintf("%s 等级 %s 库存数量应为 %d，实际为 %d", collectionDisplayName(collection), tier, tierConfig.Supply, len(normalized)))
		}
		result = append(result, normalized...)
	}
	return result, nil
}

// 标准化 metadata item 并计算哈希。
func normalizeInventoryMetadataItems(
	release factory.Release,
	collection assetValueCollection,
	metadataRef string,
	items []map[string]any,
	expectedTier string,
) ([]inventoryMetadataItem, error) {
	seen := map[string]struct{}{}
	result := make([]inventoryMetadataItem, 0, len(items))
	for _, item := range items {
		itemId := stringFromJSONValue(item["id"])
		if itemId == "" {
			return nil, newParameterError(collectionDisplayName(collection) + " metadata item 缺少 id")
		}
		if _, ok := seen[itemId]; ok {
			return nil, newConflictError(collectionDisplayName(collection) + " metadata item id 重复: " + itemId)
		}
		seen[itemId] = struct{}{}

		tier := stringFromJSONValue(item["tier"])
		if len(collection.TierConfig) > 0 {
			if tier == "" {
				return nil, newParameterError(collectionDisplayName(collection) + " metadata item 缺少 tier")
			}
			if _, ok := collection.TierConfig[tier]; !ok {
				return nil, newParameterError(collectionDisplayName(collection) + " metadata item tier 未配置: " + tier)
			}
			if expectedTier != "" && tier != expectedTier {
				return nil, newParameterError(fmt.Sprintf("%s metadata item tier 应为 %s，实际为 %s", collectionDisplayName(collection), expectedTier, tier))
			}
		}

		traitHash := ""
		if field := strings.TrimSpace(collection.TraitHashField); field != "" {
			traitHash = stringFromJSONValue(item[field])
			if traitHash == "" {
				return nil, newParameterError(collectionDisplayName(collection) + " metadata item 缺少 " + field)
			}
		}
		itemIndex := int64(len(result) + 1)
		metadataHash, err := hashItemMetadata(release, collection, metadataRef, item, itemIndex, tier, traitHash)
		if err != nil {
			return nil, err
		}
		leafHash := hashInventoryLeaf(release, collection.Key, itemIndex, itemId, tier, traitHash, metadataHash)
		result = append(result, inventoryMetadataItem{
			Item:         item,
			ItemId:       itemId,
			ItemIndex:    itemIndex,
			Tier:         tier,
			TraitHash:    traitHash,
			MetadataRef:  metadataRef,
			MetadataHash: metadataHash,
			LeafHash:     leafHash,
		})
	}
	return result, nil
}

// 读取 metadataRef 指向的数组。
func loadMetadataRefItems(snapshotDir string, metadataRef string) ([]map[string]any, error) {
	file, field, hasField, err := parseMetadataRef(metadataRef)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(snapshotDir, file)
	if hasField {
		return loadTemplateItems(path, field)
	}
	return loadRootArrayTemplateItems(path)
}

// 解析 metadataRef，支持 file.json 和 file.json#field。
func parseMetadataRef(metadataRef string) (string, string, bool, error) {
	parts := strings.SplitN(strings.TrimSpace(metadataRef), "#", 2)
	file := filepath.Clean(strings.TrimSpace(parts[0]))
	if file == "" || filepath.IsAbs(file) || file == "." || strings.HasPrefix(file, ".."+string(filepath.Separator)) || file == ".." {
		return "", "", false, newParameterError("metadataRef 文件路径不合法")
	}
	if len(parts) == 1 {
		return file, "", false, nil
	}
	field := strings.TrimSpace(parts[1])
	if field == "" {
		return "", "", false, newParameterError("metadataRef 字段不能为空")
	}
	return file, field, true, nil
}

// 固定集合默认按打乱顺序发放，普通模板允许复用。
func inventoryStrategyForCollection(collection assetValueCollection) factory.NFTInventoryStrategy {
	if len(collection.TierConfig) > 0 {
		return factory.NFTInventoryStrategyShuffled
	}
	return factory.NFTInventoryStrategyAllowDuplicate
}

// 锁定固定库存项。
func claimInventoryItems(
	tx *gorm.DB,
	release factory.Release,
	collection assetValueCollection,
	assetKind factory.AssetKind,
	tier string,
	count int64,
) ([]inventorySelectionItem, error) {
	var items []factory.NFTInventoryItem
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("release_id = ? AND collection_key = ? AND asset_kind = ? AND tier = ? AND status = ?", release.Id, collection.Key, assetKind, tier, factory.NFTInventoryItemStatusAvailable).
		Order("shuffle_index ASC").
		Limit(int(count)).
		Find(&items).Error; err != nil {
		return nil, err
	}
	if int64(len(items)) < count {
		return nil, newConflictError(fmt.Sprintf("%s 等级 %s 库存不足", collectionDisplayName(collection), tier))
	}
	result := make([]inventorySelectionItem, 0, len(items))
	for _, item := range items {
		result = append(result, inventorySelectionItem{
			Id:          item.Id,
			ItemId:      item.ItemId,
			ItemIndex:   item.ItemIndex,
			Tier:        item.Tier,
			TraitHash:   item.TraitHash,
			MetadataRef: resolveCollectionMetadataRef(collection, item.Tier),
		})
	}
	return result, nil
}

// 从可复用模板库存中按数量挑选。
func pickReusableInventoryItems(
	tx *gorm.DB,
	release factory.Release,
	collection assetValueCollection,
	assetKind factory.AssetKind,
	count int64,
) ([]inventorySelectionItem, error) {
	var items []factory.NFTInventoryItem
	if err := tx.Where("release_id = ? AND collection_key = ? AND asset_kind = ?", release.Id, collection.Key, assetKind).
		Order("item_index ASC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, newConflictError(collectionDisplayName(collection) + "没有可用库存模板")
	}
	result := make([]inventorySelectionItem, 0, count)
	for i := int64(0); i < count; i++ {
		index, err := secureRandomIndex(len(items))
		if err != nil {
			return nil, err
		}
		item := items[index]
		result = append(result, inventorySelectionItem{
			ItemId:      item.ItemId,
			ItemIndex:   item.ItemIndex,
			Tier:        item.Tier,
			TraitHash:   item.TraitHash,
			MetadataRef: resolveCollectionMetadataRef(collection, item.Tier),
		})
	}
	return result, nil
}

// 按库存项等级返回实际模板引用。
func resolveCollectionMetadataRef(collection assetValueCollection, tier string) string {
	ref := strings.TrimSpace(collection.MetadataRef)
	if tier != "" {
		// generated/fish/{tier}.json 这类模板在真正落库和发放前展开成具体路径。
		ref = strings.ReplaceAll(ref, "{tier}", tier)
	}
	return ref
}

// 将库存项转换为待铸造资产。
func appendSelectedInventoryItems(
	result *mintSelectionResult,
	collection assetValueCollection,
	assetKind factory.AssetKind,
	items []inventorySelectionItem,
) {
	for _, item := range items {
		itemIndex := int(item.ItemIndex)
		// 库存项已经冻结了模板定位信息，这里直接转成待铸造资产快照。
		selected := mintSelectionItem{
			AssetKind:       assetKind,
			CollectionKey:   strings.TrimSpace(collection.Key),
			ComponentRole:   collection.ComponentRole,
			ParentKey:       strings.TrimSpace(collection.ParentKey),
			InventoryItemId: item.Id,
			TemplateRef:     strings.TrimSpace(item.MetadataRef),
			ItemId:          item.ItemId,
			ItemIndex:       &itemIndex,
			Tier:            item.Tier,
			TraitHash:       item.TraitHash,
		}
		result.Items = append(result.Items, selected)
		result.TotalCount++
	}
}

// 生成随机发放顺序。
func createShuffleIndexes(count int) ([]int64, error) {
	indexes := make([]int64, count)
	for index := range indexes {
		indexes[index] = int64(index + 1)
	}
	for index := len(indexes) - 1; index > 0; index-- {
		swapIndex, err := secureRandomIndex(index + 1)
		if err != nil {
			return nil, err
		}
		indexes[index], indexes[swapIndex] = indexes[swapIndex], indexes[index]
	}
	return indexes, nil
}

// 返回稳定排序后的 tier key。
func sortedTierKeys(tierConfig map[string]assetTierConfig) []string {
	keys := make([]string, 0, len(tierConfig))
	for key := range tierConfig {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// 计算发布集合哈希。
func hashInventoryCollection(items []inventoryMetadataItem) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.LeafHash)
	}
	return sha256Hex([]byte(strings.Join(parts, "\n")))
}

// 计算单个 item 的 metadata hash。
func hashItemMetadata(
	release factory.Release,
	collection assetValueCollection,
	metadataRef string,
	item map[string]any,
	itemIndex int64,
	tier string,
	traitHash string,
) (string, error) {
	metadata := buildItemNFTMetadata(release, collection, metadataRef, item, itemIndex, tier, traitHash)
	data, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return sha256Hex(data), nil
}

// 生成发布后 item metadata。
func buildItemNFTMetadata(
	release factory.Release,
	collection assetValueCollection,
	metadataRef string,
	item map[string]any,
	itemIndex int64,
	tier string,
	traitHash string,
) nftMetadata {
	itemId := stringFromJSONValue(item["id"])
	attributes := []nftMetadataAttribute{
		{TraitType: "Asset Kind", Value: string(collection.AssetKind)},
		{TraitType: "Collection", Value: strings.TrimSpace(collection.Key)},
		{TraitType: "Item ID", Value: itemId},
		{TraitType: "Item Index", Value: itemIndex},
	}
	if collection.ComponentRole != "" {
		attributes = append(attributes, nftMetadataAttribute{TraitType: "Component Role", Value: string(collection.ComponentRole)})
	}
	if strings.TrimSpace(collection.ParentKey) != "" {
		attributes = append(attributes, nftMetadataAttribute{TraitType: "Parent Collection", Value: strings.TrimSpace(collection.ParentKey)})
	}
	if tier != "" {
		attributes = append(attributes, nftMetadataAttribute{TraitType: "Tier", Value: tier})
	}
	if traitHash != "" {
		attributes = append(attributes, nftMetadataAttribute{TraitType: "Trait Hash", Value: traitHash})
	}
	attributes = appendMetadataAttribute(attributes, item, "paletteId", "Palette")
	attributes = appendMetadataAttribute(attributes, item, "archetypeId", "Pattern")
	attributes = appendMetadataAttribute(attributes, item, "themePreset", "Theme")
	attributes = appendMetadataAttribute(attributes, item, "finArchetypeId", "Fin Pattern")
	attributes = appendMetadataAttribute(attributes, item, "finStyle", "Fin Style")
	attributes = appendMetadataAttribute(attributes, item, "dorsalFinShape", "Dorsal Fin")
	attributes = appendMetadataAttribute(attributes, item, "personalityId", "Personality")
	attributes = appendMetadataAttribute(attributes, item, "color", "Body Color")
	attributes = appendMetadataAttribute(attributes, item, "tailColor", "Tail Color")
	attributes = appendMetadataAttribute(attributes, item, "eyeColor", "Eye Color")

	properties := map[string]any{
		"parameters":    item,
		"pluginId":      release.PluginId,
		"releaseId":     strconv.FormatInt(release.Id, 10),
		"collectionKey": strings.TrimSpace(collection.Key),
		"componentRole": string(collection.ComponentRole),
		"parentKey":     strings.TrimSpace(collection.ParentKey),
		"itemId":        itemId,
		"itemIndex":     itemIndex,
		"metadataRef":   metadataRef,
	}
	if tier != "" {
		properties["tier"] = tier
	}
	if traitHash != "" {
		properties["traitHash"] = traitHash
	}
	return nftMetadata{
		Name:         fmt.Sprintf("%s %s %s", release.PluginId, collectionDisplayName(collection), itemId),
		Description:  fmt.Sprintf("%s composable %s NFT.", release.PluginId, collection.AssetKind),
		AnimationURL: factory.ReleaseStaticURL(release),
		Attributes:   attributes,
		Properties:   properties,
	}
}

// 计算库存 leaf hash。
func hashInventoryLeaf(
	release factory.Release,
	collectionKey string,
	itemIndex int64,
	itemId string,
	tier string,
	traitHash string,
	metadataHash string,
) string {
	leaf := strings.Join([]string{
		strconv.FormatInt(release.Id, 10),
		strings.TrimSpace(collectionKey),
		strconv.FormatInt(itemIndex, 10),
		itemId,
		tier,
		traitHash,
		metadataHash,
	}, "|")
	return sha256Hex([]byte(leaf))
}

// 将 JSON 值转成稳定字符串。
func stringFromJSONValue(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

// 解析 file.json#field 格式的模板引用。
func parseAssetRef(ref string) (string, string, error) {
	parts := strings.SplitN(strings.TrimSpace(ref), "#", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", newParameterError("资产模板 ref 格式应为 file.json#field")
	}
	file := filepath.Clean(strings.TrimSpace(parts[0]))
	if filepath.IsAbs(file) || file == "." || strings.HasPrefix(file, ".."+string(filepath.Separator)) || file == ".." {
		return "", "", newParameterError("资产模板 ref 文件路径不合法")
	}
	return file, strings.TrimSpace(parts[1]), nil
}

// 读取模板文件中的数组字段。
func loadTemplateItems(path string, field string) ([]map[string]any, error) {
	var root map[string]json.RawMessage
	if err := readJSONFile(path, &root); err != nil {
		return nil, err
	}
	raw, ok := root[field]
	if !ok {
		return nil, newParameterError("资产模板缺少字段: " + field)
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// 解析价格字符串为有理数。
func parsePriceRat(raw string, field string) (*big.Rat, error) {
	normalized, err := validateDecimalString(raw, field, false)
	if err != nil {
		return nil, err
	}
	rat, ok := new(big.Rat).SetString(normalized)
	if !ok {
		return nil, newParameterError(field + "格式错误")
	}
	return rat, nil
}

// 按字段值随机选择 itemId。
func pickItemIdsByField(templates []map[string]any, field string, value string, count int64) ([]string, error) {
	candidates := make([]string, 0, len(templates))
	for _, item := range templates {
		id, _ := item["id"].(string)
		if strings.TrimSpace(id) != "" && fmt.Sprint(item[field]) == value {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		return nil, newParameterError("没有可用资产模板: " + value)
	}
	return pickItemIdsFromCandidates(candidates, count)
}

// 从所有模板中随机选择 itemId。
func pickAnyItemIds(templates []map[string]any, count int64) ([]string, error) {
	candidates := make([]string, 0, len(templates))
	for _, item := range templates {
		id, _ := item["id"].(string)
		if strings.TrimSpace(id) != "" {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		return nil, newParameterError("没有可用资产模板")
	}
	return pickItemIdsFromCandidates(candidates, count)
}

// 从候选 itemId 中随机抽取指定数量。
func pickItemIdsFromCandidates(candidates []string, count int64) ([]string, error) {
	result := make([]string, 0, count)
	for i := int64(0); i < count; i++ {
		index, err := secureRandomIndex(len(candidates))
		if err != nil {
			return nil, err
		}
		result = append(result, candidates[index])
	}
	return result, nil
}

// 返回安全随机下标。
func secureRandomIndex(max int) (int, error) {
	if max <= 0 {
		return 0, newParameterError("随机候选为空")
	}
	value, err := crand.Int(crand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0, err
	}
	return int(value.Int64()), nil
}

// 根据组件声明创建包含关系。
func createMintRelations(tx *gorm.DB, ownerKey string, assets []factory.Asset) error {
	rootIDsByCollection := map[string][]int64{}
	for _, asset := range assets {
		if asset.AssetKind == factory.AssetKindComponent &&
			asset.ComponentRole == factory.ComponentRoleRoot &&
			strings.TrimSpace(asset.CollectionKey) != "" {
			rootIDsByCollection[asset.CollectionKey] = append(rootIDsByCollection[asset.CollectionKey], asset.Id)
		}
	}
	if len(rootIDsByCollection) == 0 {
		return nil
	}

	childIndexByParent := map[string]int{}
	for _, asset := range assets {
		if asset.AssetKind != factory.AssetKindComponent ||
			asset.ComponentRole != factory.ComponentRoleChild ||
			strings.TrimSpace(asset.ParentKey) == "" {
			continue
		}
		rootIDs := rootIDsByCollection[asset.ParentKey]
		if len(rootIDs) == 0 {
			continue
		}
		// 同一 parentKey 下的 child 轮询挂到 root 上，保持通用组合行为。
		childIndex := childIndexByParent[asset.ParentKey]
		relation := factory.AssetRelation{
			Id:            generateID(),
			OwnerKey:      ownerKey,
			RelationType:  "contains",
			SourceAssetId: rootIDs[childIndex%len(rootIDs)],
			TargetAssetId: asset.Id,
			MetadataJson:  "{}",
			Status:        factory.AssetRelationStatusActive,
		}
		if err := tx.Create(&relation).Error; err != nil {
			return err
		}
		childIndexByParent[asset.ParentKey] = childIndex + 1
	}
	return nil
}

// 写入单个资产静态快照。
func writeFactoryAssetSnapshot(asset factory.Asset) error {
	snapshot := mintedFactoryAsset{
		Schema:        "senspace.factory.asset.v2",
		AssetId:       strconv.FormatInt(asset.Id, 10),
		TokenId:       asset.TokenId,
		PluginId:      asset.PluginId,
		ReleaseId:     strconv.FormatInt(asset.ReleaseId, 10),
		Version:       asset.Version,
		RuntimeKind:   defaultRuntimeKind(asset.RuntimeKind),
		AssetKind:     asset.AssetKind,
		CollectionKey: asset.CollectionKey,
		ComponentRole: asset.ComponentRole,
		ParentKey:     asset.ParentKey,
		TemplateRef:   asset.TemplateRef,
		ItemId:        asset.ItemId,
		ItemIndex:     asset.ItemIndex,
		PluginOptions: decodePluginOptions(asset.PluginOptions),
		Tier:          asset.Tier,
		TraitHash:     asset.TraitHash,
		ReleaseUrl:    releaseStaticURLFromAsset(asset),
		MintRecordId:  strconv.FormatInt(asset.MintRecordId, 10),
		MintedAt:      asset.CreatedAt.Format(time.RFC3339Nano),
		MetadataUri:   asset.MetadataUri,
		ProofUri:      asset.ProofUri,
	}
	if err := writeNFTMetadataAndProof(asset, snapshot); err != nil {
		return err
	}
	return factory.WriteJSONAtomic(factory.AssetStaticPath(asset.PluginId, asset.Id), snapshot)
}

// 写入标准 NFT metadata 和当前阶段的 proof。
func writeNFTMetadataAndProof(asset factory.Asset, snapshot mintedFactoryAsset) error {
	tokenId := asset.TokenId
	if strings.TrimSpace(tokenId) == "" {
		tokenId = strconv.FormatInt(asset.Id, 10)
	}
	metadata := buildNFTMetadata(asset, snapshot)
	metadataData, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	metadataHash := sha256Hex(metadataData)
	// WriteJSONAtomic 在文件末尾写入换行，v2 必须绑定实际文件字节。
	frozenProof, err := buildFrozenInventoryProof(asset, sha256Hex(append(metadataData, '\n')))
	if err != nil {
		return err
	}
	if err := factory.WriteJSONAtomic(factory.MetadataStaticPath(asset.PluginId, tokenId), metadata); err != nil {
		return err
	}

	itemIndex := 0
	if asset.ItemIndex != nil {
		itemIndex = *asset.ItemIndex
	}
	leaf := strings.Join([]string{
		tokenId,
		asset.CollectionKey,
		asset.ItemId,
		strconv.Itoa(itemIndex),
		asset.Tier,
		asset.TraitHash,
		metadataHash,
	}, "|")
	leafHash := sha256Hex([]byte(leaf))
	proof := nftProofSnapshot{
		Schema:        "senspace.factory.nft-proof.v1",
		TokenId:       tokenId,
		CollectionKey: asset.CollectionKey,
		ItemId:        asset.ItemId,
		ItemIndex:     asset.ItemIndex,
		Tier:          asset.Tier,
		TraitHash:     asset.TraitHash,
		MetadataHash:  metadataHash,
		Leaf:          leafHash,
		MerkleRoot:    leafHash,
		Proof:         []string{},
	}
	if frozenProof != nil {
		frozenProof.TokenID = tokenId
		return factory.WriteJSONAtomic(factory.ProofStaticPath(asset.PluginId, tokenId), frozenProof)
	}
	return factory.WriteJSONAtomic(factory.ProofStaticPath(asset.PluginId, tokenId), proof)
}

// 生成市场通用 NFT metadata。
func buildNFTMetadata(asset factory.Asset, snapshot mintedFactoryAsset) nftMetadata {
	templateItem := loadMintedAssetTemplateItem(asset)
	assetId := strconv.FormatInt(asset.Id, 10)
	name := fmt.Sprintf("%s %s", asset.PluginId, asset.ItemId)
	if asset.CollectionKey != "" {
		name = fmt.Sprintf("%s %s %s", asset.PluginId, asset.CollectionKey, asset.ItemId)
	}

	attributes := []nftMetadataAttribute{
		{TraitType: "Asset Kind", Value: string(asset.AssetKind)},
		{TraitType: "Collection", Value: asset.CollectionKey},
		{TraitType: "Item ID", Value: asset.ItemId},
	}
	if asset.ComponentRole != "" {
		attributes = append(attributes, nftMetadataAttribute{TraitType: "Component Role", Value: string(asset.ComponentRole)})
	}
	if asset.ParentKey != "" {
		attributes = append(attributes, nftMetadataAttribute{TraitType: "Parent Collection", Value: asset.ParentKey})
	}
	if asset.Tier != "" {
		attributes = append(attributes, nftMetadataAttribute{TraitType: "Tier", Value: asset.Tier})
	}
	if asset.ItemIndex != nil {
		attributes = append(attributes, nftMetadataAttribute{TraitType: "Item Index", Value: *asset.ItemIndex})
	}
	if asset.TraitHash != "" {
		attributes = append(attributes, nftMetadataAttribute{TraitType: "Trait Hash", Value: asset.TraitHash})
	}
	attributes = appendMetadataAttribute(attributes, templateItem, "paletteId", "Palette")
	attributes = appendMetadataAttribute(attributes, templateItem, "archetypeId", "Pattern")
	attributes = appendMetadataAttribute(attributes, templateItem, "themePreset", "Theme")
	attributes = appendMetadataAttribute(attributes, templateItem, "finArchetypeId", "Fin Pattern")
	attributes = appendMetadataAttribute(attributes, templateItem, "finStyle", "Fin Style")
	attributes = appendMetadataAttribute(attributes, templateItem, "dorsalFinShape", "Dorsal Fin")
	attributes = appendMetadataAttribute(attributes, templateItem, "personalityId", "Personality")
	attributes = appendMetadataAttribute(attributes, templateItem, "color", "Body Color")
	attributes = appendMetadataAttribute(attributes, templateItem, "tailColor", "Tail Color")
	attributes = appendMetadataAttribute(attributes, templateItem, "eyeColor", "Eye Color")

	properties := map[string]any{
		"assetId":       assetId,
		"pluginId":      asset.PluginId,
		"releaseId":     strconv.FormatInt(asset.ReleaseId, 10),
		"collectionKey": asset.CollectionKey,
		"componentRole": string(asset.ComponentRole),
		"parentKey":     asset.ParentKey,
		"templateRef":   asset.TemplateRef,
		"itemId":        asset.ItemId,
		"assetUrl":      factory.AssetStaticURL(asset.PluginId, asset.Id),
	}
	if options := decodePluginOptions(asset.PluginOptions); len(options) > 0 {
		properties["pluginOptions"] = options
	}
	if asset.ItemIndex != nil {
		properties["itemIndex"] = *asset.ItemIndex
	}
	if asset.Tier != "" {
		properties["tier"] = asset.Tier
	}
	if asset.TraitHash != "" {
		properties["traitHash"] = asset.TraitHash
	}

	return nftMetadata{
		Name:         name,
		Description:  fmt.Sprintf("%s composable %s asset minted from release %s.", asset.PluginId, asset.AssetKind, snapshot.ReleaseId),
		AnimationURL: snapshot.ReleaseUrl,
		ExternalURL:  factory.AssetStaticURL(asset.PluginId, asset.Id),
		Attributes:   attributes,
		Properties:   properties,
	}
}

// 附加模板属性到 metadata attributes。
func appendMetadataAttribute(attributes []nftMetadataAttribute, item map[string]any, key string, traitType string) []nftMetadataAttribute {
	if item == nil {
		return attributes
	}
	value, ok := item[key]
	if !ok || value == nil || stringFromJSONValue(value) == "" {
		return attributes
	}
	return append(attributes, nftMetadataAttribute{TraitType: traitType, Value: value})
}

// 读取已铸造资产对应的模板项，用于补充 metadata traits。
func loadMintedAssetTemplateItem(asset factory.Asset) map[string]any {
	templateFile, refField, err := parseAssetRef(asset.TemplateRef)
	if err != nil {
		return nil
	}
	items, err := loadTemplateItems(filepath.Join(releaseStaticDirFromAsset(asset), templateFile), refField)
	if err != nil {
		return nil
	}
	return findTemplateItemById(items, asset.ItemId)
}

// 读取根节点为数组的模板文件。
func loadRootArrayTemplateItems(path string) ([]map[string]any, error) {
	var items []map[string]any
	if err := readJSONFile(path, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// 按 id 查找模板项。
func findTemplateItemById(items []map[string]any, id string) map[string]any {
	for _, item := range items {
		if stringFromJSONValue(item["id"]) == id {
			return item
		}
	}
	return nil
}

// 返回资产对应的发布快照目录。
func releaseStaticDirFromAsset(asset factory.Asset) string {
	return filepath.Join(factory.FactoryStaticRoot(), "releases", asset.PluginId, fmt.Sprintf("%s-%d", asset.Version, asset.ReleaseId))
}

// SHA-256 十六进制。
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

// 返回资产对应的发布快照地址。
func releaseStaticURLFromAsset(asset factory.Asset) string {
	return factory.FactoryStaticURL("releases", asset.PluginId, fmt.Sprintf("%s-%d", asset.Version, asset.ReleaseId), "release.json")
}

// 解码关系扩展数据。
func decodeRelationMetadata(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" {
		return nil
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return nil
	}
	return value
}

func normalizePluginOptions(input map[string]any) (normalizedPluginOptions, error) {
	if len(input) == 0 {
		return normalizedPluginOptions{
			Raw:  "{}",
			Data: nil,
		}, nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return normalizedPluginOptions{}, newParameterError("插件属性参数格式错误")
	}
	normalized := map[string]any{}
	if err := json.Unmarshal(data, &normalized); err != nil {
		return normalizedPluginOptions{}, newParameterError("插件属性参数格式错误")
	}
	if len(normalized) == 0 {
		return normalizedPluginOptions{
			Raw:  "{}",
			Data: nil,
		}, nil
	}
	normalizedData, err := json.Marshal(normalized)
	if err != nil {
		return normalizedPluginOptions{}, newParameterError("插件属性参数格式错误")
	}
	return normalizedPluginOptions{
		Raw:  string(normalizedData),
		Data: normalized,
	}, nil
}

func decodePluginOptions(raw string) map[string]any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" {
		return nil
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return nil
	}
	if len(value) == 0 {
		return nil
	}
	return value
}
