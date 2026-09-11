package factory_service

import (
	"senspace/domain/factory"
	factoryvo "senspace/domain/factory/vo"
)

// 发布 manifest。
type PluginManifest struct {
	// 插件名称。
	Name string `json:"name"`
	// 插件版本号。
	Version string `json:"version"`
	// 插件入口文件。
	Entry string `json:"entry"`
	// 插件描述。
	Description string `json:"description,omitempty"`
}

// 作者摘要。
type AuthorProfile struct {
	// 作者用户 ID。
	Id string `json:"id"`
	// 作者展示名。
	Name string `json:"name"`
	// 作者头像。
	Avatar string `json:"avatar,omitempty"`
}

// 可变市场信息。
type MutableMarketMetadata struct {
	// 市场摘要。
	Summary string `json:"summary"`
	// 市场分类。
	Category string `json:"category"`
	// 市场标签列表。
	Tags []string `json:"tags"`
	// 市场封面图地址。
	CoverUrl string `json:"coverUrl,omitempty"`
}

// 发布配置。
type ReleasePayload struct {
	MutableMarketMetadata
	// 当前版本总发行量。
	TotalSupply int64 `json:"totalSupply"`
	// 单次允许铸造的最大份数。
	MintPer int64 `json:"mintPer"`
	// 铸造价格，统一按字符串传输。
	MintPrice string `json:"mintPrice"`
	// 目标版本声明的升级策略。
	UpgradePolicy factory.ReleaseUpgradePolicy `json:"upgradePolicy,omitempty"`
	// 付费升级金额。
	UpgradePrice string `json:"upgradePrice,omitempty"`
}

// 发布请求。
type PublishRequest struct {
	// 插件业务 ID。
	PluginId string `json:"pluginId"`
	// 请求提交的 manifest 快照。
	Manifest PluginManifest `json:"manifest"`
	// 发布配置。
	Release ReleasePayload `json:"release"`
}

// 发布资产冻结结果。
type FreezeReleaseAssetsResponse struct {
	// 发布记录 ID。
	ReleaseId string `json:"releaseId"`
	// 插件业务 ID。
	PluginId string `json:"pluginId"`
	// 发布版本号。
	Version string `json:"version"`
	// 冻结状态。
	Status string `json:"status"`
	// 返回给前端的提示文案。
	Message string `json:"message"`
	// 发布快照入口地址。
	ReleaseUrl string `json:"releaseUrl"`
	// 复杂价格模板地址；简单 NFT 可为空。
	AssetMetaUrl string `json:"assetMetaUrl"`
	// 当前发布冻结出的库存池摘要。
	Pools []FreezeReleaseInventoryPool `json:"pools"`
}

// 发布资产冻结清除结果。
type ClearFreezeReleaseAssetsResponse struct {
	// 发布记录 ID。
	ReleaseId string `json:"releaseId"`
	// 插件业务 ID。
	PluginId string `json:"pluginId"`
	// 发布版本号。
	Version string `json:"version"`
	// 清除结果文案。
	Message string `json:"message"`
	// 删除的库存池数量。
	RemovedPools int64 `json:"removedPools"`
	// 删除的库存项数量。
	RemovedItems int64 `json:"removedItems"`
}

// 发布清除结果。
type ClearReleaseResponse struct {
	// 发布记录 ID。
	ReleaseId string `json:"releaseId"`
	// 插件业务 ID。
	PluginId string `json:"pluginId"`
	// 发布版本号。
	Version string `json:"version"`
	// 清除结果文案。
	Message string `json:"message"`
}

// 单个库存池冻结摘要。
type FreezeReleaseInventoryPool struct {
	// 集合业务键。
	CollectionKey string `json:"collectionKey"`
	// 资产类型。
	AssetKind factory.AssetKind `json:"assetKind"`
	// 元数据引用位置。
	MetadataRef string `json:"metadataRef"`
	// 库存发放策略。
	Strategy factory.NFTInventoryStrategy `json:"strategy"`
	// 当前集合总供应量。
	TotalSupply int64 `json:"totalSupply"`
	// 已铸造数量。
	MintedCount int64 `json:"mintedCount"`
	// 库存池状态。
	Status factory.NFTInventoryPoolStatus `json:"status"`
	// 当前集合内容签名。
	CollectionHash string `json:"collectionHash,omitempty"`
	// 当前集合的根哈希。
	MerkleRoot string `json:"merkleRoot,omitempty"`
}

// 资产生成器数据模式。
type GenerateReleaseAssetDataMode string

const (
	GenerateReleaseAssetDataModeTest   GenerateReleaseAssetDataMode = "test"
	GenerateReleaseAssetDataModeFormal GenerateReleaseAssetDataMode = "formal"
)

// 资产生成请求。
type GenerateReleaseAssetDataRequest struct {
	// 正式批次种子；留空使用配置中的固定种子。
	Seed string `json:"seed,omitempty"`
	// 生成模式。
	Mode GenerateReleaseAssetDataMode `json:"mode"`
	// 只生成指定等级。
	Tier string `json:"tier,omitempty"`
	// 限制生成数量。
	Count int `json:"count,omitempty"`
}

// 资产生成响应。
type GenerateReleaseAssetDataResponse struct {
	// 正式批次编号。
	BatchID string `json:"batchId,omitempty"`
	// 实际使用的种子。
	Seed string `json:"seed,omitempty"`
	// 实际执行的模式。
	Mode GenerateReleaseAssetDataMode `json:"mode"`
	// 生成结果子目录名。
	DataDirName string `json:"dataDirName"`
	// 生成输出目录。
	OutputDir string `json:"outputDir"`
	// 实际生成数量。
	Total int `json:"total"`
	// 返回给前端的提示文案。
	Message string `json:"message"`
}

// 我的发布筛选。
type ReleaseQuery struct {
	// 按插件业务 ID 过滤。
	PluginId string
	// 按发布状态过滤。
	Status string
	// 是否只返回当前主推版本。
	CurrentOnly *bool
}

// 市场筛选。
type MarketQuery struct {
	// 按插件业务 ID 过滤。
	PluginId string
	// 按市场分类过滤。
	Category string
	// 按标签交集过滤。
	Tags []string
	// 按发布状态过滤。
	Status string
	// 是否只返回当前主推版本。
	CurrentOnly *bool
}

// 市场信息更新请求。
type UpdateReleaseRequest struct {
	// 发布记录 ID。
	Id string `json:"id"`
	// 允许更新的市场信息。
	Market MutableMarketMetadata `json:"market"`
}

// 价格更新请求。
type UpdateReleasePriceRequest struct {
	// 发布记录 ID。
	Id string `json:"id"`
	// 新铸造价格。
	MintPrice string `json:"mintPrice"`
	// 调价原因。
	Reason string `json:"reason,omitempty"`
}

// 状态更新请求。
type UpdateReleaseStatusRequest struct {
	// 发布记录 ID。
	Id string `json:"id"`
	// 目标状态。
	Status factory.ReleaseStatus `json:"status"`
	// 状态变更原因。
	Reason string `json:"reason,omitempty"`
}

// 铸造记录请求。
type RecordMintRequest struct {
	// 发布记录 ID。
	ReleaseId string
	// 平台用户 ID。
	UserId uint64
	// 钱包地址。
	WalletAddress string
	// 铸造数量。
	Quantity int64
	// 支付总额。
	TotalPaid string
	// 链 ID。
	ChainId *int64
	// 链上交易哈希。
	TxHash string
}

// 按发布价值模板铸造独立 NFT。
type MintAssetRequest struct {
	// 按 collection.key 分组的数量输入。
	Inputs map[string]map[string]int64 `json:"inputs"`
	// 插件属性面板参数。
	PluginOptions map[string]any `json:"pluginOptions,omitempty"`
	// 铸造前插件资源草稿ID。
	PluginAssetDraftId string `json:"pluginAssetDraftId,omitempty"`
	// 前端计算后的总支付金额。
	TotalPaid string `json:"totalPaid"`
	// 链 ID。
	ChainId *int64 `json:"chainId,omitempty"`
	// 链上交易哈希。
	TxHash string `json:"txHash,omitempty"`
}

// 铸造生成的单个 NFT 入口。
type MintAssetResponseAsset struct {
	// 资产 ID。
	AssetId string `json:"assetId"`
	// 资产类型。
	AssetKind factory.AssetKind `json:"assetKind"`
	// 集合业务键。
	CollectionKey string `json:"collectionKey,omitempty"`
	// 组件角色。
	ComponentRole factory.ComponentRole `json:"componentRole,omitempty"`
	// 父组件集合键。
	ParentKey string `json:"parentKey,omitempty"`
	// 资产快照地址。
	AssetUrl string `json:"assetUrl"`
	// 模板项序号。
	ItemIndex *int `json:"itemIndex,omitempty"`
	// 稀有度等级。
	Tier string `json:"tier,omitempty"`
	// 属性哈希。
	TraitHash string `json:"traitHash,omitempty"`
	// NFT metadata 地址。
	MetadataUri string `json:"metadataUri,omitempty"`
	// NFT proof 地址。
	ProofUri string `json:"proofUri,omitempty"`
}

// 铸造生成的快照入口。
type MintAssetResponse struct {
	// 本次生成的资产列表。
	Assets []MintAssetResponseAsset `json:"assets"`
	// 持有人资产索引地址。
	OwnerIndexUrl string `json:"ownerIndexUrl"`
	// 持有人组合关系地址。
	OwnerCompositionUrl string `json:"ownerCompositionUrl"`
	// 本次支付总额。
	TotalPaid string `json:"totalPaid"`
}

// 价格历史视图。
type PriceHistoryRecord struct {
	// 历史记录 ID。
	Id string `json:"id"`
	// 发布记录 ID。
	ReleaseId string `json:"releaseId"`
	// 调价前价格。
	PreviousMintPrice string `json:"previousMintPrice,omitempty"`
	// 调价后价格。
	NextMintPrice string `json:"nextMintPrice"`
	// 调价原因。
	Reason string `json:"reason,omitempty"`
	// 操作人标识。
	ChangedBy string `json:"changedBy,omitempty"`
	// 变更时间。
	ChangedAt string `json:"changedAt"`
}

// 发布记录视图。
type PublishRecord struct {
	// 发布记录 ID。
	Id string `json:"id"`
	// 插件业务 ID。
	PluginId string `json:"pluginId"`
	// 作者摘要。
	Author AuthorProfile `json:"author"`
	// 市场展示名。
	Name string `json:"name"`
	// 发布版本号。
	Version string `json:"version"`
	// 当前发布状态。
	Status factory.ReleaseStatus `json:"status"`
	// 审核状态。
	ReviewStatus factory.ReviewStatus `json:"reviewStatus,omitempty"`
	// 是否为当前主推版本。
	CurrentRelease bool `json:"currentRelease,omitempty"`
	// 总发行量。
	TotalSupply int64 `json:"totalSupply"`
	// 已铸造数量。
	MintedCount int64 `json:"mintedCount"`
	// 单次最大铸造量。
	MintPer int64 `json:"mintPer"`
	// 铸造价格。
	MintPrice string `json:"mintPrice"`
	// 市场摘要。
	Summary string `json:"summary,omitempty"`
	// 市场分类。
	Category string `json:"category,omitempty"`
	// 标签列表。
	Tags []string `json:"tags,omitempty"`
	// 市场封面图。
	CoverUrl string `json:"coverUrl,omitempty"`
	// 源码哈希。
	SourceHash string `json:"sourceHash,omitempty"`
	// 构建包哈希。
	BundleHash string `json:"bundleHash,omitempty"`
	// 运行入口完整性校验值。
	Integrity string `json:"integrity,omitempty"`
	// 构建状态。
	BuildStatus factory.BuildStatus `json:"buildStatus,omitempty"`
	// 构建失败原因。
	BuildError string `json:"buildError,omitempty"`
	// 最近一次构建完成时间。
	BuiltAt string `json:"builtAt,omitempty"`
	// 运行来源类型。
	RuntimeKind factory.ReleaseRuntimeKind `json:"runtimeKind"`
	// 发布快照入口地址。
	ReleaseUrl string `json:"releaseUrl,omitempty"`
	// 升级策略。
	UpgradePolicy factory.ReleaseUpgradePolicy `json:"upgradePolicy,omitempty"`
	// 升级费用。
	UpgradePrice string `json:"upgradePrice,omitempty"`
	// 发布时间。
	PublishedAt string `json:"publishedAt,omitempty"`
	// 暂停时间。
	PausedAt string `json:"pausedAt,omitempty"`
	// 关闭时间。
	ClosedAt string `json:"closedAt,omitempty"`
	// 最后更新时间。
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// 聚合后的版本候选。
type MarketReleaseVersion struct {
	PublishRecord
}

// 市场聚合卡片。
type MarketPluginCard struct {
	// 插件业务 ID。
	PluginId string `json:"pluginId"`
	// 当前默认展示版本。
	CurrentVersion string `json:"currentVersion"`
	// 可切换的版本列表，按版本号从大到小排序。
	Versions []MarketReleaseVersion `json:"versions"`
}

// 发布详情视图。
type PublishDetail struct {
	PublishRecord
	// 发布时锁定的 manifest 快照。
	ManifestSnapshot PluginManifest `json:"manifestSnapshot"`
	// 价格变更历史。
	PriceHistory []PriceHistoryRecord `json:"priceHistory,omitempty"`
}

// 用户插件资产视图。
type UserPluginOwnershipView = factoryvo.UserPluginOwnershipView
