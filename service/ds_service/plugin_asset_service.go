package ds_service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"senspace/domain"
	"senspace/domain/dev"
	"senspace/domain/ds"
	"senspace/domain/factory"
	"senspace/pkg/app/security"
	"senspace/pkg/bizerr"
	"senspace/pkg/setting"
	"senspace/pkg/util"
)

const (
	pluginAssetSchema = "senspace.plugin-assets.v1"
	pluginStateSchema = "senspace.plugin-state.v2"
	defaultImageKind  = "image"
	defaultFileKind   = "file"
	defaultFileMime   = "application/octet-stream"
)

// 资源绑定保存参数。
type ResourceStateBindingInput struct {
	AssetId       uint64          `json:"assetId,string"`   // 资源ID。
	CollectionKey string          `json:"collectionKey"`    // 资源集合键。
	SortOrder     int             `json:"sortOrder"`        // 展示排序。
	Config        json.RawMessage `json:"config,omitempty"` // 资源展示配置。
}

// 插件状态保存请求。
type PluginStateInput struct {
	ExpectedRevision *int64                      `json:"expectedRevision,omitempty"` // 期望状态版本。
	State            map[string]interface{}      `json:"state,omitempty"`            // 实例持久化状态。
	ResourceState    map[string]interface{}      `json:"resource_state,omitempty"`   // 资源相关状态。
	Bindings         []ResourceStateBindingInput `json:"bindings,omitempty"`         // 资源绑定配置。
}

// 实例状态保存参数。
type StateSaveInput struct {
	ExpectedRevision *int64                 `json:"expectedRevision,omitempty"` // 期望状态版本。
	State            map[string]interface{} `json:"state,omitempty"`            // 实例持久化状态。
}

// 资源状态保存参数。
type ResourceStateSaveInput struct {
	ExpectedRevision *int64                      `json:"expectedRevision,omitempty"` // 期望状态版本。
	ResourceState    map[string]interface{}      `json:"resource_state,omitempty"`   // 资源相关状态。
	Bindings         []ResourceStateBindingInput `json:"bindings,omitempty"`         // 资源绑定配置。
}

// 资源提交绑定参数。
type ResourceStateCommitBindingInput struct {
	AssetId       string          `json:"assetId"`          // 资源ID，支持真实ID或local-*。
	CollectionKey string          `json:"collectionKey"`    // 资源集合键。
	SortOrder     int             `json:"sortOrder"`        // 展示排序。
	Config        json.RawMessage `json:"config,omitempty"` // 资源展示配置。
}

// 本地文件提交参数。
type ResourceStateCommitLocalFileInput struct {
	LocalId       string          `json:"localId"`          // 本地临时资源ID。
	FileKey       string          `json:"fileKey"`          // multipart文件字段名。
	CollectionKey string          `json:"collectionKey"`    // 资源集合键。
	SortOrder     int             `json:"sortOrder"`        // 展示排序。
	Config        json.RawMessage `json:"config,omitempty"` // 资源展示配置。
}

// 插件实例资源提交参数。
type ResourceStateCommitInput struct {
	ExpectedRevision *int64                              `json:"expectedRevision,omitempty"` // 期望状态版本。
	ResourceState    map[string]interface{}              `json:"resource_state,omitempty"`   // 资源相关状态。
	Bindings         []ResourceStateCommitBindingInput   `json:"bindings,omitempty"`         // 最终资源绑定。
	LocalFiles       []ResourceStateCommitLocalFileInput `json:"localFiles,omitempty"`       // 本地待上传文件。
	DeletedAssetIds  []uint64                            `json:"deletedAssetIds,omitempty"`  // 待删除资源ID。
}

// 本地资源提交结果。
type ResourceStateCommittedLocalFile struct {
	LocalId string          `json:"localId"` // 本地临时资源ID。
	Asset   pluginAssetJSON `json:"asset"`   // 服务端资源信息。
}

// 插件实例资源提交结果。
type ResourceStateCommitResult struct {
	Snapshot      PluginAssetSnapshot               `json:"snapshot"`       // 最新静态快照。
	ResourceState map[string]interface{}            `json:"resource_state"` // 替换资源ID后的资源状态。
	Uploaded      []ResourceStateCommittedLocalFile `json:"uploaded"`       // 本次上传资源。
}

// 插件实例草稿快照。
type PluginInstanceDraftSnapshot struct {
	DraftId       string                 `json:"draftId"`        // 草稿ID。
	Scope         ds.PluginAssetScope    `json:"scope"`          // 草稿资源空间。
	Snapshot      PluginAssetSnapshot    `json:"snapshot"`       // 最新静态快照。
	ResourceState map[string]interface{} `json:"resource_state"` // 已保存资源状态。
}

// 静态快照返回信息。
type PluginAssetSnapshot struct {
	Scope       ds.PluginAssetScope `json:"scope"`       // 资源空间。
	ManifestUrl string              `json:"manifestUrl"` // 资源清单地址。
	StateUrl    string              `json:"stateUrl"`    // 实例状态地址。
	Revision    int64               `json:"revision"`    // 状态版本号。
}

// 上传资源返回信息。
type PluginAssetUploadResult struct {
	Snapshot PluginAssetSnapshot `json:"snapshot"` // 最新静态快照。
	Asset    pluginAssetJSON     `json:"asset"`    // 新增资源信息。
}

// 静态清单中的单个资源。
type pluginAssetJSON struct {
	AssetId   string `json:"assetId"`            // 资源ID。
	Kind      string `json:"kind"`               // 资源类型。
	Mime      string `json:"mime"`               // 媒体类型。
	Url       string `json:"url"`                // 原图地址。
	ThumbUrl  string `json:"thumbUrl,omitempty"` // 缩略图地址。
	Hash      string `json:"hash"`               // 内容哈希。
	Width     int    `json:"width,omitempty"`    // 图片宽度。
	Height    int    `json:"height,omitempty"`   // 图片高度。
	SizeBytes int64  `json:"sizeBytes"`          // 文件大小。
}

// 资源清单静态文件。
type pluginAssetManifestJSON struct {
	Schema    string              `json:"schema"`    // 协议版本。
	Scope     ds.PluginAssetScope `json:"scope"`     // 资源空间。
	UpdatedAt string              `json:"updatedAt"` // 更新时间。
	Assets    []pluginAssetJSON   `json:"assets"`    // 资源列表。
}

// 状态快照中的资源绑定。
type pluginAssetBindingJSON struct {
	AssetId string      `json:"assetId"`          // 资源ID。
	Order   int         `json:"order"`            // 展示排序。
	Config  interface{} `json:"config,omitempty"` // 展示配置。
}

// 插件实例状态静态文件。
type pluginInstanceStateJSON struct {
	Schema        string                       `json:"schema"`         // 协议版本。
	Scope         ds.PluginAssetScope          `json:"scope"`          // 资源空间。
	Revision      int64                        `json:"revision"`       // 状态版本号。
	UpdatedAt     string                       `json:"updatedAt"`      // 更新时间。
	State         interface{}                  `json:"state"`          // 实例持久化状态。
	ResourceState pluginAssetResourceStateJSON `json:"resource_state"` // 资源相关状态。
}

// 静态文件中的资源状态分区。
type pluginAssetResourceStateJSON struct {
	State       interface{}                         `json:"state"`       // 资源配置状态。
	Collections map[string][]pluginAssetBindingJSON `json:"collections"` // 分组资源配置。
}

// 处理后的上传资源。
type processedPluginAsset struct {
	Kind     string // 资源类型。
	Original []byte // 原始文件字节。
	Thumb    []byte // 缩略图字节；非图片为空。
	Mime     string // 输出媒体类型。
	Ext      string // 输出文件后缀。
	Hash     string // 内容哈希。
	Width    int    // 图片宽度。
	Height   int    // 图片高度。
}

// ResolveFactPluginAssetScope 校验并返回 fact 资源空间。
func ResolveFactPluginAssetScope(user security.JwtUser, factAssetId int64) (ds.PluginAssetScope, error) {
	asset, err := resolveOwnedFactoryAsset(domain.Db, user, factAssetId)
	if err != nil {
		return ds.PluginAssetScope{}, err
	}
	return buildFactPluginAssetScope(*asset), nil
}

// ResolveDevPluginAssetScope 校验并返回 dev 资源空间。
func ResolveDevPluginAssetScope(user security.JwtUser, pluginId string, version string) (ds.PluginAssetScope, error) {
	walletAddress := strings.TrimSpace(user.Addr)
	if walletAddress == "" {
		return ds.PluginAssetScope{}, errors.New("请先登录钱包")
	}
	pluginId = strings.TrimSpace(pluginId)
	version = strings.TrimSpace(version)
	if err := ds.ValidatePluginAssetPathSegment("pluginId", pluginId); err != nil {
		return ds.PluginAssetScope{}, err
	}
	if err := ds.ValidatePluginAssetPathSegment("pluginVersion", version); err != nil {
		return ds.PluginAssetScope{}, err
	}
	scope := ds.PluginAssetScope{
		Kind:          ds.PluginAssetScopeDev,
		OwnerKey:      factory.OwnerIndexKey(walletAddress),
		OwnerAddress:  walletAddress,
		PluginId:      pluginId,
		PluginVersion: version,
	}
	return scope, scope.Validate()
}

// ResolveDraftPluginAssetScope 校验并返回 draft 资源空间。
func ResolveDraftPluginAssetScope(user security.JwtUser, releaseId int64, draftId string) (ds.PluginAssetScope, error) {
	walletAddress := strings.TrimSpace(user.Addr)
	if walletAddress == "" {
		return ds.PluginAssetScope{}, errors.New("请先登录钱包")
	}
	draftId = strings.TrimSpace(draftId)
	if err := ds.ValidatePluginAssetPathSegment("draftId", draftId); err != nil {
		return ds.PluginAssetScope{}, err
	}
	var release factory.Release
	if err := domain.Db.First(&release, "id = ?", releaseId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ds.PluginAssetScope{}, errors.New("发布记录不存在")
		}
		return ds.PluginAssetScope{}, err
	}
	scope := ds.PluginAssetScope{
		Kind:          ds.PluginAssetScopeDraft,
		OwnerKey:      factory.OwnerIndexKey(walletAddress),
		OwnerAddress:  walletAddress,
		ReleaseId:     release.Id,
		DraftId:       draftId,
		PluginId:      release.PluginId,
		PluginVersion: release.Version,
	}
	return scope, scope.Validate()
}

// UploadPluginAsset 上传并绑定插件实例资源。
func UploadPluginAsset(user security.JwtUser, factAssetId int64, collectionKey string, fileHeader *multipart.FileHeader) (*PluginAssetUploadResult, error) {
	scope, err := ResolveFactPluginAssetScope(user, factAssetId)
	if err != nil {
		return nil, err
	}
	return UploadPluginAssetInScope(user, scope, collectionKey, fileHeader)
}

// UploadPluginAssetInScope 上传并绑定指定资源空间下的插件实例资源。
func UploadPluginAssetInScope(user security.JwtUser, scope ds.PluginAssetScope, collectionKey string, fileHeader *multipart.FileHeader) (*PluginAssetUploadResult, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if fileHeader == nil {
		return nil, errors.New("上传文件不能为空")
	}
	processed, err := processUploadedPluginAsset(fileHeader)
	if err != nil {
		return nil, err
	}
	return saveProcessedPluginAsset(user, scope, collectionKey, processed)
}

// ImportPluginAssetImageInScope 从用户素材库导入图片到插件资源空间。
func ImportPluginAssetImageInScope(user security.JwtUser, scope ds.PluginAssetScope, collectionKey string, imageID uint64) (*PluginAssetUploadResult, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if imageID == 0 {
		return nil, errors.New("图片不能为空")
	}
	imageRecord := ds.Image{Id: imageID}.GetById()
	if imageRecord.Id == 0 || imageRecord.CreatedBy != user.Id {
		return nil, errors.New("图片不存在或无权访问")
	}
	imagePath, err := resolveUserImageStoragePath(imageRecord.Url)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, err
	}
	processed, err := processPluginImageBytes(data)
	if err != nil {
		return nil, err
	}
	return saveProcessedPluginAsset(user, scope, collectionKey, processed)
}

func saveProcessedPluginAsset(user security.JwtUser, scope ds.PluginAssetScope, collectionKey string, processed *processedPluginAsset) (*PluginAssetUploadResult, error) {
	collectionKey, err := normalizeCollectionKey(collectionKey)
	if err != nil {
		return nil, err
	}
	if processed == nil {
		return nil, errors.New("资源处理结果为空")
	}
	assetID := generatePluginAssetID()
	assetDir := ds.PluginAssetDir(scope, assetID)
	originalName := "original" + processed.Ext
	publicURL := ds.PluginAssetStaticURL(append(scope.StaticPathParts(), "assets", strconv.FormatUint(assetID, 10), originalName)...)
	thumbURL := ""
	thumbName := ""
	if len(processed.Thumb) > 0 {
		thumbName = "thumb" + processed.Ext
		thumbURL = ds.PluginAssetStaticURL(append(scope.StaticPathParts(), "assets", strconv.FormatUint(assetID, 10), thumbName)...)
	}
	storagePath := filepath.Join(assetDir, originalName)
	if err := writeUploadedPluginAssetFiles(assetDir, originalName, thumbName, processed); err != nil {
		return nil, err
	}

	var createdAsset ds.PluginAsset
	err = domain.Db.Transaction(func(tx *gorm.DB) error {
		createdAsset = ds.PluginAsset{
			Id:            assetID,
			ScopeKind:     scope.Kind,
			OwnerKey:      scope.OwnerKey,
			OwnerAddress:  scope.OwnerAddress,
			FactAssetId:   nullableInt64(scope.FactAssetId),
			PluginId:      scope.PluginId,
			PluginVersion: scope.PluginVersion,
			ReleaseId:     nullableInt64(scope.ReleaseId),
			DraftId:       scope.DraftId,
			Kind:          processed.Kind,
			Mime:          processed.Mime,
			Hash:          processed.Hash,
			SizeBytes:     int64(len(processed.Original)),
			Width:         processed.Width,
			Height:        processed.Height,
			StoragePath:   storagePath,
			PublicUrl:     publicURL,
			ThumbUrl:      thumbURL,
			Status:        ds.PluginAssetStatusActive,
		}
		createdAsset.CreatedBy = user.Id
		createdAsset.UpdatedBy = user.Id
		if err := tx.Create(&createdAsset).Error; err != nil {
			return err
		}
		sortOrder, err := nextPluginAssetSortOrder(tx, scope, collectionKey)
		if err != nil {
			return err
		}
		binding := ds.PluginAssetBinding{
			ScopeKind:     scope.Kind,
			OwnerKey:      scope.OwnerKey,
			FactAssetId:   nullableInt64(scope.FactAssetId),
			PluginId:      scope.PluginId,
			PluginVersion: scope.PluginVersion,
			ReleaseId:     nullableInt64(scope.ReleaseId),
			DraftId:       scope.DraftId,
			AssetId:       assetID,
			CollectionKey: collectionKey,
			SortOrder:     sortOrder,
			ConfigJson:    "{}",
		}
		binding.CreatedBy = user.Id
		binding.UpdatedBy = user.Id
		if err := tx.Create(&binding).Error; err != nil {
			return err
		}
		return bumpPluginInstanceState(tx, user.Id, scope)
	})
	if err != nil {
		_ = os.RemoveAll(assetDir)
		return nil, err
	}
	snapshot, err := RebuildPluginAssetSnapshot(scope)
	if err != nil {
		return nil, err
	}
	assetJSON := buildPluginAssetJSON(createdAsset)
	return &PluginAssetUploadResult{Snapshot: *snapshot, Asset: assetJSON}, nil
}

// SaveState 保存实例状态并刷新静态快照。
func SaveState(user security.JwtUser, factAssetId int64, input StateSaveInput) (*PluginAssetSnapshot, error) {
	scope, err := ResolveFactPluginAssetScope(user, factAssetId)
	if err != nil {
		return nil, err
	}
	return SaveStateInScope(user, scope, input)
}

// SaveStateInScope 保存指定空间下的实例状态并刷新静态快照。
func SaveStateInScope(user security.JwtUser, scope ds.PluginAssetScope, input StateSaveInput) (*PluginAssetSnapshot, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	err := domain.Db.Transaction(func(tx *gorm.DB) error {
		state, err := lockPluginInstanceState(tx, scope)
		if err != nil {
			return err
		}
		if input.ExpectedRevision != nil && state.Revision != *input.ExpectedRevision {
			return fmt.Errorf("状态版本已变化，请刷新后重试")
		}
		applyScopeToPluginInstanceState(&state, scope)
		if input.State != nil {
			state.StateJson = encodeJSONOrEmpty(input.State)
		}
		state.Revision += 1
		state.UpdatedBy = user.Id
		if state.Id == 0 {
			state.CreatedBy = user.Id
			if err := tx.Create(&state).Error; err != nil {
				return err
			}
			return upsertActivePluginInstanceDraft(tx, user, scope)
		}
		if err := tx.Save(&state).Error; err != nil {
			return err
		}
		return upsertActivePluginInstanceDraft(tx, user, scope)
	})
	if err != nil {
		return nil, err
	}
	return RebuildPluginAssetSnapshot(scope)
}

// SaveResourceState 保存资源状态并刷新静态快照。
func SaveResourceState(user security.JwtUser, factAssetId int64, input ResourceStateSaveInput) (*PluginAssetSnapshot, error) {
	scope, err := ResolveFactPluginAssetScope(user, factAssetId)
	if err != nil {
		return nil, err
	}
	return SaveResourceStateInScope(user, scope, input)
}

// SaveResourceStateInScope 保存指定空间下的资源状态并刷新静态快照。
func SaveResourceStateInScope(user security.JwtUser, scope ds.PluginAssetScope, input ResourceStateSaveInput) (*PluginAssetSnapshot, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	err := domain.Db.Transaction(func(tx *gorm.DB) error {
		state, err := lockPluginInstanceState(tx, scope)
		if err != nil {
			return err
		}
		if input.ExpectedRevision != nil && state.Revision != *input.ExpectedRevision {
			return fmt.Errorf("状态版本已变化，请刷新后重试")
		}
		if input.Bindings != nil {
			if err := replacePluginAssetBindings(tx, user.Id, scope, input.Bindings); err != nil {
				return err
			}
		}
		applyScopeToPluginInstanceState(&state, scope)
		if input.ResourceState != nil {
			state.ResourceStateJson = encodeJSONOrEmpty(input.ResourceState)
		}
		state.Revision += 1
		state.UpdatedBy = user.Id
		if state.Id == 0 {
			state.CreatedBy = user.Id
			if err := tx.Create(&state).Error; err != nil {
				return err
			}
			return upsertActivePluginInstanceDraft(tx, user, scope)
		}
		if err := tx.Save(&state).Error; err != nil {
			return err
		}
		return upsertActivePluginInstanceDraft(tx, user, scope)
	})
	if err != nil {
		return nil, err
	}
	return RebuildPluginAssetSnapshot(scope)
}

// CommitResourceStateInScope 提交本地资源、删除资源并保存资源状态。
func CommitResourceStateInScope(user security.JwtUser, scope ds.PluginAssetScope, input ResourceStateCommitInput, files map[string]*multipart.FileHeader) (*ResourceStateCommitResult, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	localAssetIds := map[string]string{}
	uploaded := make([]ResourceStateCommittedLocalFile, 0, len(input.LocalFiles))
	for _, item := range input.LocalFiles {
		localId := strings.TrimSpace(item.LocalId)
		fileKey := strings.TrimSpace(item.FileKey)
		if localId == "" || fileKey == "" {
			return nil, errors.New("本地资源参数不能为空")
		}
		fileHeader := files[fileKey]
		if fileHeader == nil {
			return nil, fmt.Errorf("本地资源文件缺失: %s", fileKey)
		}
		result, err := UploadPluginAssetInScope(user, scope, item.CollectionKey, fileHeader)
		if err != nil {
			return nil, err
		}
		localAssetIds[localId] = result.Asset.AssetId
		uploaded = append(uploaded, ResourceStateCommittedLocalFile{
			LocalId: localId,
			Asset:   result.Asset,
		})
	}

	finalBindings, referencedAssetIds, err := buildCommittedPluginAssetBindings(input.Bindings, input.LocalFiles, localAssetIds)
	if err != nil {
		return nil, err
	}
	for _, assetID := range normalizeDeletedPluginAssetIDs(input.DeletedAssetIds, referencedAssetIds) {
		if _, err := DeletePluginAssetInScope(user, scope, assetID); err != nil {
			return nil, err
		}
	}

	resourceState := replaceLocalPluginAssetIDs(input.ResourceState, localAssetIds)
	snapshot, err := SaveResourceStateInScope(user, scope, ResourceStateSaveInput{
		ExpectedRevision: input.ExpectedRevision,
		ResourceState:    resourceState,
		Bindings:         finalBindings,
	})
	if err != nil {
		return nil, err
	}
	return &ResourceStateCommitResult{
		Snapshot:      *snapshot,
		ResourceState: resourceState,
		Uploaded:      uploaded,
	}, nil
}

// GetActivePluginInstanceDraft 读取当前用户在发布下的活动草稿。
func GetActivePluginInstanceDraft(user security.JwtUser, releaseId int64) (*PluginInstanceDraftSnapshot, error) {
	ownerKey := factory.OwnerIndexKey(user.Addr)
	if ownerKey == "" {
		return nil, errors.New("请先登录钱包")
	}
	var draft ds.PluginInstanceDraft
	err := domain.Db.
		Where("owner_key = ? AND release_id = ? AND status = ?", ownerKey, releaseId, ds.PluginInstanceDraftStatusActive).
		Order("updated_at DESC").
		First(&draft).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	scope, err := ResolveDraftPluginAssetScope(user, releaseId, draft.DraftId)
	if err != nil {
		return nil, err
	}
	snapshot, err := RebuildPluginAssetSnapshot(scope)
	if err != nil {
		return nil, err
	}
	var state ds.PluginInstanceState
	err = applyScopeFilter(domain.Db.Model(&ds.PluginInstanceState{}), scope).First(&state).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &PluginInstanceDraftSnapshot{
		DraftId:       draft.DraftId,
		Scope:         scope,
		Snapshot:      *snapshot,
		ResourceState: decodeJSONObject(state.ResourceStateJson),
	}, nil
}

// ArchiveDraftPluginAssetsToFactAssets 将铸造草稿资源归档到新资产实例。
func ArchiveDraftPluginAssetsToFactAssets(user security.JwtUser, releaseId int64, draftId string, factAssetIds []int64, stateOverride map[string]any) error {
	if len(factAssetIds) == 0 {
		return nil
	}
	draftScope, err := ResolveDraftPluginAssetScope(user, releaseId, draftId)
	if err != nil {
		return err
	}
	factScopes := make([]ds.PluginAssetScope, 0, len(factAssetIds))
	for _, factAssetId := range factAssetIds {
		scope, err := ResolveFactPluginAssetScope(user, factAssetId)
		if err != nil {
			return err
		}
		factScopes = append(factScopes, scope)
	}
	var draftAssets []ds.PluginAsset
	if err := applyScopeFilter(domain.Db.Model(&ds.PluginAsset{}), draftScope).
		Where("status = ?", ds.PluginAssetStatusActive).
		Order("id ASC").
		Find(&draftAssets).Error; err != nil {
		return err
	}
	var draftBindings []ds.PluginAssetBinding
	if err := applyScopeFilter(domain.Db.Model(&ds.PluginAssetBinding{}), draftScope).
		Order("collection_key ASC, sort_order ASC, id ASC").
		Find(&draftBindings).Error; err != nil {
		return err
	}
	var draftState ds.PluginInstanceState
	err = applyScopeFilter(domain.Db.Model(&ds.PluginInstanceState{}), draftScope).First(&draftState).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	plans, err := buildDraftArchivePlans(draftScope, factScopes, draftAssets)
	if err != nil {
		return err
	}
	createdDirs := make([]string, 0)
	for _, plan := range plans {
		for _, filePlan := range plan.Files {
			if err := copyPluginAssetDir(filePlan.SourceDir, filePlan.TargetDir); err != nil {
				removeArchivedPluginAssetDirs(createdDirs)
				return err
			}
			createdDirs = append(createdDirs, filePlan.TargetDir)
		}
	}

	err = domain.Db.Transaction(func(tx *gorm.DB) error {
		for _, plan := range plans {
			for _, asset := range plan.Assets {
				asset.CreatedBy = user.Id
				asset.UpdatedBy = user.Id
				if err := tx.Create(&asset).Error; err != nil {
					return err
				}
			}
			bindings := buildArchivedPluginAssetBindings(draftBindings, plan.AssetIdMap)
			if err := replacePluginAssetBindings(tx, user.Id, plan.Scope, bindings); err != nil {
				return err
			}
			stateSource := decodeJSONObject(draftState.ResourceStateJson)
			if stateOverride != nil {
				stateSource = stateOverride
			}
			statePayload := replaceLocalPluginAssetIDs(stateSource, plan.AssetIdMap)
			state, err := lockPluginInstanceState(tx, plan.Scope)
			if err != nil {
				return err
			}
			applyScopeToPluginInstanceState(&state, plan.Scope)
			state.ResourceStateJson = encodeJSONOrEmpty(statePayload)
			state.StateJson = encodeJSONOrEmpty(decodeJSONObject(draftState.StateJson))
			state.Revision += 1
			state.UpdatedBy = user.Id
			if state.Id == 0 {
				state.CreatedBy = user.Id
				if err := tx.Create(&state).Error; err != nil {
					return err
				}
			} else if err := tx.Save(&state).Error; err != nil {
				return err
			}
		}
		if err := applyScopeFilter(tx, draftScope).Delete(&ds.PluginAssetBinding{}).Error; err != nil {
			return err
		}
		if err := applyScopeFilter(tx, draftScope).Delete(&ds.PluginAsset{}).Error; err != nil {
			return err
		}
		if err := applyScopeFilter(tx, draftScope).Delete(&ds.PluginInstanceState{}).Error; err != nil {
			return err
		}
		return tx.Model(&ds.PluginInstanceDraft{}).
			Where("draft_key = ?", buildPluginInstanceDraftKey(draftScope)).
			Updates(map[string]interface{}{
				"status":        ds.PluginInstanceDraftStatusMinted,
				"fact_asset_id": nullableInt64(factAssetIds[0]),
				"updated_by":    user.Id,
			}).Error
	})
	if err != nil {
		removeArchivedPluginAssetDirs(createdDirs)
		return err
	}
	for _, scope := range factScopes {
		if _, err := RebuildPluginAssetSnapshot(scope); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(ds.PluginAssetInstanceDir(draftScope)); err != nil {
		return err
	}
	return nil
}

// 删除插件关联的 draft 资源记录和静态目录。
func DeletePluginDraftArtifactsByPluginID(pluginId string) error {
	normalizedPluginID := strings.TrimSpace(pluginId)
	if normalizedPluginID == "" {
		return nil
	}
	if err := ds.ValidatePluginAssetPathSegment("pluginId", normalizedPluginID); err != nil {
		return err
	}
	var drafts []ds.PluginInstanceDraft
	if err := domain.Db.Where("plugin_id = ?", normalizedPluginID).Find(&drafts).Error; err != nil {
		return err
	}
	return deleteDraftArtifacts(drafts)
}

// 删除发布关联的 draft 资源记录和静态目录。
func DeleteReleaseDraftArtifacts(releaseId int64) error {
	if releaseId <= 0 {
		return nil
	}
	var drafts []ds.PluginInstanceDraft
	if err := domain.Db.Where("release_id = ?", releaseId).Find(&drafts).Error; err != nil {
		return err
	}
	return deleteDraftArtifacts(drafts)
}

// 按草稿记录批量清理数据库和磁盘目录。
func deleteDraftArtifacts(drafts []ds.PluginInstanceDraft) error {
	scopes, draftIDs, err := buildDraftArtifactCleanupScopes(drafts)
	if err != nil {
		return err
	}
	if len(scopes) == 0 {
		return nil
	}
	if err := domain.Db.Transaction(func(tx *gorm.DB) error {
		for _, scope := range scopes {
			if err := applyScopeFilter(tx, scope).Delete(&ds.PluginAssetBinding{}).Error; err != nil {
				return err
			}
			if err := applyScopeFilter(tx, scope).Delete(&ds.PluginAsset{}).Error; err != nil {
				return err
			}
			if err := applyScopeFilter(tx, scope).Delete(&ds.PluginInstanceState{}).Error; err != nil {
				return err
			}
		}
		return tx.Where("id IN ?", draftIDs).Delete(&ds.PluginInstanceDraft{}).Error
	}); err != nil {
		return err
	}
	return removeDraftArtifactDirs(scopes)
}

// 将草稿记录转换为可删除的资源空间定义。
func buildDraftArtifactCleanupScopes(drafts []ds.PluginInstanceDraft) ([]ds.PluginAssetScope, []uint64, error) {
	scopes := make([]ds.PluginAssetScope, 0, len(drafts))
	draftIDs := make([]uint64, 0, len(drafts))
	for _, draft := range drafts {
		scope := ds.PluginAssetScope{
			Kind:          ds.PluginAssetScopeDraft,
			OwnerKey:      strings.TrimSpace(draft.OwnerKey),
			OwnerAddress:  strings.TrimSpace(draft.OwnerAddress),
			ReleaseId:     draft.ReleaseId,
			DraftId:       strings.TrimSpace(draft.DraftId),
			PluginId:      strings.TrimSpace(draft.PluginId),
			PluginVersion: strings.TrimSpace(draft.PluginVersion),
		}
		if err := scope.Validate(); err != nil {
			return nil, nil, err
		}
		scopes = append(scopes, scope)
		draftIDs = append(draftIDs, draft.Id)
	}
	return scopes, draftIDs, nil
}

// 删除草稿资源目录。
func removeDraftArtifactDirs(scopes []ds.PluginAssetScope) error {
	for _, scope := range scopes {
		if err := os.RemoveAll(ds.PluginAssetInstanceDir(scope)); err != nil {
			return err
		}
	}
	return nil
}

// 删除指定 fact 资源空间的数据库记录和静态目录。
func DeleteFactPluginAssetArtifacts(factAssetId int64) error {
	if factAssetId <= 0 {
		return nil
	}
	scope := ds.PluginAssetScope{
		Kind:        ds.PluginAssetScopeFact,
		OwnerKey:    "system",
		FactAssetId: factAssetId,
	}
	if err := domain.Db.Transaction(func(tx *gorm.DB) error {
		if err := applyScopeFilter(tx, scope).Delete(&ds.PluginAssetBinding{}).Error; err != nil {
			return err
		}
		if err := applyScopeFilter(tx, scope).Delete(&ds.PluginAsset{}).Error; err != nil {
			return err
		}
		return applyScopeFilter(tx, scope).Delete(&ds.PluginInstanceState{}).Error
	}); err != nil {
		return err
	}
	return os.RemoveAll(ds.PluginAssetInstanceDir(scope))
}

// 删除插件资源并刷新静态快照。
func DeletePluginAsset(user security.JwtUser, factAssetId int64, assetID uint64) (*PluginAssetSnapshot, error) {
	if factAssetId <= 0 || assetID == 0 {
		return nil, errors.New("插件资产实例或资源不能为空")
	}
	scope, err := ResolveFactPluginAssetScope(user, factAssetId)
	if err != nil {
		return nil, err
	}
	return DeletePluginAssetInScope(user, scope, assetID)
}

// 物理删除指定空间下的插件资源并刷新静态快照。
func DeletePluginAssetInScope(user security.JwtUser, scope ds.PluginAssetScope, assetID uint64) (*PluginAssetSnapshot, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if assetID == 0 {
		return nil, errors.New("资源不能为空")
	}
	var asset ds.PluginAsset
	err := domain.Db.Transaction(func(tx *gorm.DB) error {
		err := applyScopeFilter(tx.Clauses(clause.Locking{Strength: "UPDATE"}).Model(&ds.PluginAsset{}), scope).
			Where("id = ?", assetID).
			First(&asset).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("资源不存在或无权删除")
		}
		if err != nil {
			return err
		}
		if err := applyScopeFilter(tx, scope).
			Where("asset_id = ?", assetID).
			Delete(&ds.PluginAssetBinding{}).Error; err != nil {
			return err
		}
		result := applyScopeFilter(tx, scope).
			Where("id = ?", assetID).
			Delete(&ds.PluginAsset{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("资源不存在或无权删除")
		}
		return bumpPluginInstanceState(tx, user.Id, scope)
	})
	if err != nil {
		return nil, err
	}
	assetDir := ds.PluginAssetDir(scope, assetID)
	if strings.TrimSpace(asset.StoragePath) != "" {
		// 以数据库路径反推目录，避免后续目录布局调整时误删实例根目录。
		assetDir = filepath.Dir(asset.StoragePath)
	}
	if err := removePluginAssetDir(scope, assetDir); err != nil {
		return nil, err
	}
	return RebuildPluginAssetSnapshot(scope)
}

func removePluginAssetDir(scope ds.PluginAssetScope, assetDir string) error {
	root, err := filepath.Abs(ds.PluginAssetInstanceDir(scope))
	if err != nil {
		return err
	}
	target, err := filepath.Abs(assetDir)
	if err != nil {
		return err
	}
	if target == root || !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return errors.New("资源目录越界")
	}
	return os.RemoveAll(target)
}

func pluginAssetStorageDir(scope ds.PluginAssetScope, asset ds.PluginAsset) string {
	if strings.TrimSpace(asset.StoragePath) != "" {
		return filepath.Dir(asset.StoragePath)
	}
	return ds.PluginAssetDir(scope, asset.Id)
}

func copyPluginAssetDir(sourceDir string, targetDir string) error {
	sourceInfo, err := os.Stat(sourceDir)
	if err != nil {
		return err
	}
	if !sourceInfo.IsDir() {
		return errors.New("资源源目录无效")
	}
	if err := os.MkdirAll(targetDir, os.ModePerm); err != nil {
		return err
	}
	return filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if relativePath == "." {
			return nil
		}
		targetPath := filepath.Join(targetDir, relativePath)
		if entry.IsDir() {
			return os.MkdirAll(targetPath, os.ModePerm)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return writeFileAtomic(targetPath, data)
	})
}

func removeArchivedPluginAssetDirs(dirs []string) {
	for _, dir := range dirs {
		_ = os.RemoveAll(dir)
	}
}

func resolveUserImageStoragePath(imageURL string) (string, error) {
	trimmed := strings.TrimSpace(imageURL)
	if trimmed == "" {
		return "", errors.New("图片路径为空")
	}
	cleanURL := filepath.Clean(trimmed)
	if cleanURL == "." || filepath.IsAbs(cleanURL) || strings.HasPrefix(cleanURL, "..") {
		return "", errors.New("图片路径无效")
	}
	root, err := filepath.Abs(setting.Config.App.FilePath.Image)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, cleanURL))
	if err != nil {
		return "", err
	}
	if target == root || !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", errors.New("图片路径越界")
	}
	return target, nil
}

func processPluginImageBytes(data []byte) (*processedPluginAsset, error) {
	processedImage, err := util.ProcessImageBytes(data, util.ImageProcessOptions{
		MaxBytes:          setting.Config.App.ImageMaxSize,
		MaxDimension:      1600,
		ThumbMaxDimension: 360,
		JPEGQuality:       82,
		ThumbJPEGQuality:  78,
	})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(processedImage.Original)
	return &processedPluginAsset{
		Kind:     defaultImageKind,
		Original: processedImage.Original,
		Thumb:    processedImage.Thumb,
		Mime:     processedImage.Mime,
		Ext:      processedImage.Ext,
		Hash:     "sha256:" + hex.EncodeToString(sum[:]),
		Width:    processedImage.Width,
		Height:   processedImage.Height,
	}, nil
}

func processPluginFileBytes(data []byte, filename string) (*processedPluginAsset, error) {
	if len(data) == 0 {
		return nil, errors.New("上传文件为空")
	}
	mime := strings.TrimSpace(http.DetectContentType(data[:minInt(len(data), 512)]))
	if mime == "" {
		mime = defaultFileMime
	}
	normalizedData, normalizedMime, normalized := util.NormalizeTextFileBytes(
		data,
		filename,
		mime,
	)
	if normalized {
		data = normalizedData
		mime = normalizedMime
	}
	sum := sha256.Sum256(data)
	return &processedPluginAsset{
		Kind:     defaultFileKind,
		Original: data,
		Mime:     mime,
		Ext:      normalizePluginAssetFileExt(filename),
		Hash:     "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

// RebuildPluginAssetSnapshot 重建插件实例资源静态快照。
func RebuildPluginAssetSnapshot(scope ds.PluginAssetScope) (*PluginAssetSnapshot, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	manifest, state, revision, err := buildPluginAssetSnapshotPayload(domain.Db, scope)
	if err != nil {
		return nil, err
	}
	instanceDir := ds.PluginAssetInstanceDir(scope)
	if err := os.MkdirAll(instanceDir, os.ModePerm); err != nil {
		return nil, err
	}
	if err := writeJSONFileAtomic(ds.PluginAssetManifestPath(scope), manifest); err != nil {
		return nil, err
	}
	if err := writeJSONFileAtomic(ds.PluginAssetStatePath(scope), state); err != nil {
		return nil, err
	}
	return &PluginAssetSnapshot{
		Scope:       scope,
		ManifestUrl: ds.PluginAssetManifestURL(scope),
		StateUrl:    ds.PluginAssetStateURL(scope),
		Revision:    revision,
	}, nil
}

// ResolvePluginAssetOwner 校验并返回当前用户拥有的插件资产实例。
func ResolvePluginAssetOwner(user security.JwtUser, factAssetId int64) (*factory.Asset, error) {
	return resolveOwnedFactoryAsset(domain.Db, user, factAssetId)
}

func buildFactPluginAssetScope(asset factory.Asset) ds.PluginAssetScope {
	return ds.PluginAssetScope{
		Kind:          ds.PluginAssetScopeFact,
		OwnerKey:      asset.OwnerKey,
		OwnerAddress:  asset.OwnerAddress,
		FactAssetId:   asset.Id,
		PluginId:      asset.PluginId,
		PluginVersion: asset.Version,
		ReleaseId:     asset.ReleaseId,
	}
}

func resolveOwnedFactoryAsset(db *gorm.DB, user security.JwtUser, factAssetId int64) (*factory.Asset, error) {
	ownerKey := factory.OwnerIndexKey(user.Addr)
	var asset factory.Asset
	err := db.Where("id = ? AND owner_key = ? AND status = ?", factAssetId, ownerKey, factory.AssetStatusActive).First(&asset).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, bizerr.NotFound("插件资产实例不存在或无权访问")
	}
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

func processUploadedPluginAsset(fileHeader *multipart.FileHeader) (*processedPluginAsset, error) {
	if fileHeader.Size <= 0 {
		return nil, errors.New("上传文件为空")
	}
	file, err := fileHeader.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	ctype := strings.TrimSpace(http.DetectContentType(data[:minInt(len(data), 512)]))
	if strings.HasPrefix(ctype, "image/") {
		return processPluginImageBytes(data)
	}
	return processPluginFileBytes(data, fileHeader.Filename)
}

func writeUploadedPluginAssetFiles(assetDir string, originalName string, thumbName string, processed *processedPluginAsset) error {
	if err := os.MkdirAll(assetDir, os.ModePerm); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(assetDir, originalName), processed.Original); err != nil {
		return err
	}
	if thumbName == "" || len(processed.Thumb) == 0 {
		return nil
	}
	return writeFileAtomic(filepath.Join(assetDir, thumbName), processed.Thumb)
}

func normalizePluginAssetFileExt(filename string) string {
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(filename)))
	if ext == "" || ext == "." {
		return ".bin"
	}
	if len(ext) > 16 {
		return ".bin"
	}
	for _, ch := range ext[1:] {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') {
			return ".bin"
		}
	}
	return ext
}

func buildCommittedPluginAssetBindings(bindings []ResourceStateCommitBindingInput, localFiles []ResourceStateCommitLocalFileInput, localAssetIds map[string]string) ([]ResourceStateBindingInput, map[uint64]struct{}, error) {
	result := make([]ResourceStateBindingInput, 0, len(bindings)+len(localFiles))
	referenced := map[uint64]struct{}{}
	for _, input := range bindings {
		assetID, ok, err := resolveCommittedAssetID(input.AssetId, localAssetIds)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		referenced[assetID] = struct{}{}
		result = append(result, ResourceStateBindingInput{
			AssetId:       assetID,
			CollectionKey: input.CollectionKey,
			SortOrder:     input.SortOrder,
			Config:        input.Config,
		})
	}
	for _, input := range localFiles {
		assetIDText := localAssetIds[strings.TrimSpace(input.LocalId)]
		assetID, err := strconv.ParseUint(assetIDText, 10, 64)
		if err != nil || assetID == 0 {
			return nil, nil, fmt.Errorf("本地资源未完成上传: %s", input.LocalId)
		}
		referenced[assetID] = struct{}{}
		result = append(result, ResourceStateBindingInput{
			AssetId:       assetID,
			CollectionKey: input.CollectionKey,
			SortOrder:     input.SortOrder,
			Config:        input.Config,
		})
	}
	return result, referenced, nil
}

func resolveCommittedAssetID(raw string, localAssetIds map[string]string) (uint64, bool, error) {
	assetIDText := strings.TrimSpace(raw)
	if assetIDText == "" {
		return 0, false, nil
	}
	if strings.HasPrefix(assetIDText, "local-") {
		assetIDText = localAssetIds[assetIDText]
		if assetIDText == "" {
			return 0, false, nil
		}
	}
	assetID, err := strconv.ParseUint(assetIDText, 10, 64)
	if err != nil || assetID == 0 {
		return 0, false, fmt.Errorf("资源ID无效: %s", raw)
	}
	return assetID, true, nil
}

func normalizeDeletedPluginAssetIDs(assetIDs []uint64, referenced map[uint64]struct{}) []uint64 {
	seen := map[uint64]struct{}{}
	result := make([]uint64, 0, len(assetIDs))
	for _, assetID := range assetIDs {
		if assetID == 0 {
			continue
		}
		if _, ok := referenced[assetID]; ok {
			continue
		}
		if _, ok := seen[assetID]; ok {
			continue
		}
		seen[assetID] = struct{}{}
		result = append(result, assetID)
	}
	return result
}

func replaceLocalPluginAssetIDs(value interface{}, localAssetIds map[string]string) map[string]interface{} {
	replaced, ok := replaceLocalPluginAssetIDValue(value, localAssetIds).(map[string]interface{})
	if !ok || replaced == nil {
		return map[string]interface{}{}
	}
	return replaced
}

func replaceLocalPluginAssetIDValue(value interface{}, localAssetIds map[string]string) interface{} {
	switch typed := value.(type) {
	case string:
		if replacement := localAssetIds[typed]; replacement != "" {
			return replacement
		}
		return typed
	case []interface{}:
		result := make([]interface{}, len(typed))
		for index, item := range typed {
			result[index] = replaceLocalPluginAssetIDValue(item, localAssetIds)
		}
		return result
	case map[string]interface{}:
		result := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			result[key] = replaceLocalPluginAssetIDValue(item, localAssetIds)
		}
		return result
	default:
		return value
	}
}

func replacePluginAssetBindings(tx *gorm.DB, userID uint64, scope ds.PluginAssetScope, bindings []ResourceStateBindingInput) error {
	if err := applyScopeFilter(tx.Model(&ds.PluginAssetBinding{}), scope).
		Delete(&ds.PluginAssetBinding{}).Error; err != nil {
		return err
	}
	for _, input := range bindings {
		collectionKey, err := normalizeCollectionKey(input.CollectionKey)
		if err != nil {
			return err
		}
		var count int64
		if err := applyScopeFilter(tx.Model(&ds.PluginAsset{}), scope).
			Where("id = ? AND status = ?", input.AssetId, ds.PluginAssetStatusActive).
			Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("资源 %d 不存在或已删除", input.AssetId)
		}
		binding := ds.PluginAssetBinding{
			ScopeKind:     scope.Kind,
			OwnerKey:      scope.OwnerKey,
			FactAssetId:   nullableInt64(scope.FactAssetId),
			PluginId:      scope.PluginId,
			PluginVersion: scope.PluginVersion,
			ReleaseId:     nullableInt64(scope.ReleaseId),
			DraftId:       scope.DraftId,
			AssetId:       input.AssetId,
			CollectionKey: collectionKey,
			SortOrder:     input.SortOrder,
			ConfigJson:    normalizeRawJSON(input.Config),
		}
		binding.CreatedBy = userID
		binding.UpdatedBy = userID
		if err := tx.Create(&binding).Error; err != nil {
			return err
		}
	}
	return nil
}

func bumpPluginInstanceState(tx *gorm.DB, userID uint64, scope ds.PluginAssetScope) error {
	state, err := lockPluginInstanceState(tx, scope)
	if err != nil {
		return err
	}
	applyScopeToPluginInstanceState(&state, scope)
	state.Revision += 1
	state.UpdatedBy = userID
	if state.Id == 0 {
		state.CreatedBy = userID
		state.ResourceStateJson = "{}"
		state.StateJson = "{}"
		if err := tx.Create(&state).Error; err != nil {
			return err
		}
		return upsertActivePluginInstanceDraft(tx, security.JwtUser{Id: userID}, scope)
	}
	if err := tx.Save(&state).Error; err != nil {
		return err
	}
	return upsertActivePluginInstanceDraft(tx, security.JwtUser{Id: userID}, scope)
}

func upsertActivePluginInstanceDraft(tx *gorm.DB, user security.JwtUser, scope ds.PluginAssetScope) error {
	if scope.Kind != ds.PluginAssetScopeDraft {
		return nil
	}
	draft := ds.PluginInstanceDraft{
		Id:            generatePluginAssetID(),
		DraftKey:      buildPluginInstanceDraftKey(scope),
		DraftId:       scope.DraftId,
		OwnerKey:      scope.OwnerKey,
		OwnerAddress:  scope.OwnerAddress,
		UserId:        user.Id,
		ReleaseId:     scope.ReleaseId,
		PluginId:      scope.PluginId,
		PluginVersion: scope.PluginVersion,
		Status:        ds.PluginInstanceDraftStatusActive,
	}
	draft.CreatedBy = user.Id
	draft.UpdatedBy = user.Id
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "draft_key"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"draft_id":       draft.DraftId,
			"owner_key":      draft.OwnerKey,
			"owner_address":  draft.OwnerAddress,
			"user_id":        draft.UserId,
			"release_id":     draft.ReleaseId,
			"plugin_id":      draft.PluginId,
			"plugin_version": draft.PluginVersion,
			"status":         draft.Status,
			"updated_by":     draft.UpdatedBy,
		}),
	}).Create(&draft).Error
}

func buildPluginInstanceDraftKey(scope ds.PluginAssetScope) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", scope.OwnerKey, scope.ReleaseId, scope.DraftId)))
	return "draft:" + hex.EncodeToString(sum[:])
}

func lockPluginInstanceState(tx *gorm.DB, scope ds.PluginAssetScope) (ds.PluginInstanceState, error) {
	var state ds.PluginInstanceState
	err := applyScopeFilter(tx.Clauses(clause.Locking{Strength: "UPDATE"}).Model(&ds.PluginInstanceState{}), scope).
		First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		state = ds.PluginInstanceState{
			ResourceStateJson: "{}",
			StateJson:         "{}",
			Revision:          0,
		}
		applyScopeToPluginInstanceState(&state, scope)
		return state, nil
	}
	return state, err
}

func nextPluginAssetSortOrder(tx *gorm.DB, scope ds.PluginAssetScope, collectionKey string) (int, error) {
	var maxOrder int
	err := applyScopeFilter(tx.Model(&ds.PluginAssetBinding{}), scope).
		Where("collection_key = ?", collectionKey).
		Select("COALESCE(MAX(sort_order), 0)").
		Scan(&maxOrder).Error
	return maxOrder + 10, err
}

func buildPluginAssetSnapshotPayload(db *gorm.DB, scope ds.PluginAssetScope) (*pluginAssetManifestJSON, *pluginInstanceStateJSON, int64, error) {
	var assets []ds.PluginAsset
	if err := applyScopeFilter(db.Model(&ds.PluginAsset{}), scope).
		Where("status = ?", ds.PluginAssetStatusActive).
		Order("id ASC").
		Find(&assets).Error; err != nil {
		return nil, nil, 0, err
	}
	var bindings []ds.PluginAssetBinding
	if err := applyScopeFilter(db.Model(&ds.PluginAssetBinding{}), scope).
		Order("collection_key ASC, sort_order ASC, id ASC").
		Find(&bindings).Error; err != nil {
		return nil, nil, 0, err
	}
	var state ds.PluginInstanceState
	err := applyScopeFilter(db.Model(&ds.PluginInstanceState{}), scope).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		state = ds.PluginInstanceState{ResourceStateJson: "{}", StateJson: "{}", Revision: 0}
		applyScopeToPluginInstanceState(&state, scope)
	} else if err != nil {
		return nil, nil, 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	manifestAssets := make([]pluginAssetJSON, 0, len(assets))
	for _, asset := range assets {
		manifestAssets = append(manifestAssets, buildPluginAssetJSON(asset))
	}
	collections := map[string][]pluginAssetBindingJSON{}
	for _, binding := range bindings {
		config := decodeJSONValue(binding.ConfigJson)
		item := pluginAssetBindingJSON{
			AssetId: strconv.FormatUint(binding.AssetId, 10),
			Order:   binding.SortOrder,
			Config:  config,
		}
		collections[binding.CollectionKey] = append(collections[binding.CollectionKey], item)
	}
	for key := range collections {
		sort.SliceStable(collections[key], func(i, j int) bool {
			return collections[key][i].Order < collections[key][j].Order
		})
	}
	manifest := &pluginAssetManifestJSON{
		Schema:    pluginAssetSchema,
		Scope:     scope,
		UpdatedAt: now,
		Assets:    manifestAssets,
	}
	stateJSON := &pluginInstanceStateJSON{
		Schema:    pluginStateSchema,
		Scope:     scope,
		Revision:  state.Revision,
		UpdatedAt: now,
		State:     decodeJSONValue(state.StateJson),
		ResourceState: pluginAssetResourceStateJSON{
			State:       decodeJSONValue(state.ResourceStateJson),
			Collections: collections,
		},
	}
	return manifest, stateJSON, state.Revision, nil
}

func buildPluginAssetJSON(asset ds.PluginAsset) pluginAssetJSON {
	return pluginAssetJSON{
		AssetId:   strconv.FormatUint(asset.Id, 10),
		Kind:      asset.Kind,
		Mime:      asset.Mime,
		Url:       asset.PublicUrl,
		ThumbUrl:  asset.ThumbUrl,
		Hash:      asset.Hash,
		Width:     asset.Width,
		Height:    asset.Height,
		SizeBytes: asset.SizeBytes,
	}
}

// 草稿资源归档计划。
type draftArchivePlan struct {
	Scope      ds.PluginAssetScope    // 目标 fact 资源空间。
	Assets     []ds.PluginAsset       // 待创建的 fact 资源记录。
	Files      []draftArchiveFilePlan // 待复制的资源目录。
	AssetIdMap map[string]string      // draft 资源ID到 fact 资源ID的映射。
}

// 草稿资源目录复制计划。
type draftArchiveFilePlan struct {
	SourceDir string // draft 资源目录。
	TargetDir string // fact 资源目录。
}

func buildDraftArchivePlans(draftScope ds.PluginAssetScope, factScopes []ds.PluginAssetScope, draftAssets []ds.PluginAsset) ([]draftArchivePlan, error) {
	plans := make([]draftArchivePlan, 0, len(factScopes))
	for _, factScope := range factScopes {
		plan := draftArchivePlan{
			Scope:      factScope,
			Assets:     make([]ds.PluginAsset, 0, len(draftAssets)),
			Files:      make([]draftArchiveFilePlan, 0, len(draftAssets)),
			AssetIdMap: map[string]string{},
		}
		for _, draftAsset := range draftAssets {
			nextAssetID := generatePluginAssetID()
			sourceDir := pluginAssetStorageDir(draftScope, draftAsset)
			targetDir := ds.PluginAssetDir(factScope, nextAssetID)
			originalName := filepath.Base(draftAsset.StoragePath)
			if originalName == "." || originalName == string(os.PathSeparator) {
				return nil, errors.New("草稿资源路径无效")
			}
			thumbName := filepath.Base(strings.TrimSpace(draftAsset.ThumbUrl))
			nextAsset := draftAsset
			nextAsset.Id = nextAssetID
			nextAsset.ScopeKind = factScope.Kind
			nextAsset.OwnerKey = factScope.OwnerKey
			nextAsset.OwnerAddress = factScope.OwnerAddress
			nextAsset.FactAssetId = nullableInt64(factScope.FactAssetId)
			nextAsset.PluginId = factScope.PluginId
			nextAsset.PluginVersion = factScope.PluginVersion
			nextAsset.ReleaseId = nullableInt64(factScope.ReleaseId)
			nextAsset.DraftId = ""
			nextAsset.StoragePath = filepath.Join(targetDir, originalName)
			nextAsset.PublicUrl = ds.PluginAssetStaticURL(append(factScope.StaticPathParts(), "assets", strconv.FormatUint(nextAssetID, 10), originalName)...)
			if thumbName != "" && thumbName != "." {
				nextAsset.ThumbUrl = ds.PluginAssetStaticURL(append(factScope.StaticPathParts(), "assets", strconv.FormatUint(nextAssetID, 10), thumbName)...)
			}
			plan.Assets = append(plan.Assets, nextAsset)
			plan.Files = append(plan.Files, draftArchiveFilePlan{
				SourceDir: sourceDir,
				TargetDir: targetDir,
			})
			plan.AssetIdMap[strconv.FormatUint(draftAsset.Id, 10)] = strconv.FormatUint(nextAssetID, 10)
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func buildArchivedPluginAssetBindings(bindings []ds.PluginAssetBinding, assetIdMap map[string]string) []ResourceStateBindingInput {
	result := make([]ResourceStateBindingInput, 0, len(bindings))
	for _, binding := range bindings {
		nextAssetIDText := assetIdMap[strconv.FormatUint(binding.AssetId, 10)]
		nextAssetID, err := strconv.ParseUint(nextAssetIDText, 10, 64)
		if err != nil || nextAssetID == 0 {
			continue
		}
		result = append(result, ResourceStateBindingInput{
			AssetId:       nextAssetID,
			CollectionKey: binding.CollectionKey,
			SortOrder:     binding.SortOrder,
			Config:        json.RawMessage(binding.ConfigJson),
		})
	}
	return result
}

func applyScopeFilter(tx *gorm.DB, scope ds.PluginAssetScope) *gorm.DB {
	switch scope.Kind {
	case ds.PluginAssetScopeFact:
		return tx.Where("scope_kind = ? AND fact_asset_id = ?", scope.Kind, scope.FactAssetId)
	case ds.PluginAssetScopeDev:
		return tx.Where("scope_kind = ? AND owner_key = ? AND plugin_id = ? AND plugin_version = ?", scope.Kind, scope.OwnerKey, scope.PluginId, scope.PluginVersion)
	case ds.PluginAssetScopeDraft:
		return tx.Where("scope_kind = ? AND owner_key = ? AND release_id = ? AND draft_id = ?", scope.Kind, scope.OwnerKey, scope.ReleaseId, scope.DraftId)
	default:
		return tx.Where("1 = 0")
	}
}

func applyScopeToPluginInstanceState(state *ds.PluginInstanceState, scope ds.PluginAssetScope) {
	state.ScopeKind = scope.Kind
	state.OwnerKey = scope.OwnerKey
	state.FactAssetId = nullableInt64(scope.FactAssetId)
	state.PluginId = scope.PluginId
	state.PluginVersion = scope.PluginVersion
	state.ReleaseId = nullableInt64(scope.ReleaseId)
	state.DraftId = scope.DraftId
}

func nullableInt64(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	v := value
	return &v
}

func writeJSONFileAtomic(path string, payload interface{}) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data)
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

func verifyDevPluginWorkspaceAccess(user security.JwtUser, pluginId string) error {
	if user.Id == 0 {
		return errors.New("请先登录")
	}
	pluginNumericID, err := strconv.ParseInt(strings.TrimSpace(pluginId), 10, 64)
	if err != nil || pluginNumericID <= 0 {
		return errors.New("开发工作区插件不存在")
	}
	plugin := dev.Plugin{Id: pluginNumericID}.GetById()
	if plugin.Id == 0 {
		return errors.New("开发工作区插件不存在")
	}
	if plugin.CreatedBy != user.Id {
		return errors.New("无权编辑该插件")
	}
	return nil
}

func resolveDevPluginWorkspaceVersion(pluginId string, version string) (string, string, error) {
	versionRoot, manifestVersion, err := resolveDevPluginVersionRoot(pluginId, version)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(versionRoot); err != nil {
		if os.IsNotExist(err) {
			return "", "", errors.New("开发工作区插件不存在")
		}
		return "", "", err
	}
	resolvedVersion := filepath.Base(versionRoot)
	return resolvedVersion, manifestVersion, nil
}

func resolveDevPluginVersionRoot(pluginId string, version string) (string, string, error) {
	if err := ds.ValidatePluginAssetPathSegment("pluginId", pluginId); err != nil {
		return "", "", err
	}
	pluginRoot := filepath.Join(setting.Config.App.FilePath.Plugin, strings.TrimSpace(pluginId))
	normalizedVersion := strings.TrimSpace(version)
	if normalizedVersion != "" {
		if err := ds.ValidatePluginAssetPathSegment("pluginVersion", normalizedVersion); err != nil {
			return "", "", err
		}
	}
	if normalizedVersion == "" {
		entries, err := os.ReadDir(pluginRoot)
		if err != nil {
			if os.IsNotExist(err) {
				return "", "", errors.New("开发工作区插件不存在")
			}
			return "", "", err
		}
		latestVersion := ""
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			candidate := strings.TrimSpace(entry.Name())
			if candidate == "" {
				continue
			}
			if ds.ValidatePluginAssetPathSegment("pluginVersion", candidate) != nil {
				continue
			}
			if latestVersion == "" || comparePluginVersions(candidate, latestVersion) > 0 {
				latestVersion = candidate
			}
		}
		if latestVersion == "" {
			return "", "", errors.New("开发工作区插件版本不存在")
		}
		normalizedVersion = latestVersion
	}
	versionRoot := filepath.Join(pluginRoot, normalizedVersion)
	manifestVersion, err := loadDevPluginManifestVersion(versionRoot)
	if err != nil {
		return "", "", err
	}
	return versionRoot, manifestVersion, nil
}

func comparePluginVersions(v1 string, v2 string) int {
	p1 := strings.Split(v1, ".")
	p2 := strings.Split(v2, ".")
	limit := len(p1)
	if len(p2) > limit {
		limit = len(p2)
	}
	for i := 0; i < limit; i++ {
		var n1, n2 int
		if i < len(p1) {
			n1, _ = strconv.Atoi(p1[i])
		}
		if i < len(p2) {
			n2, _ = strconv.Atoi(p2[i])
		}
		if n1 != n2 {
			return n1 - n2
		}
	}
	return 0
}

func loadDevPluginManifestVersion(versionRoot string) (string, error) {
	manifestPath := filepath.Join(versionRoot, "manifest.json")
	bytes, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("开发工作区插件缺少manifest.json")
		}
		return "", err
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(bytes, &manifest); err != nil {
		return "", errors.New("开发工作区manifest.json格式错误")
	}
	return strings.TrimSpace(manifest.Version), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeCollectionKey(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "default", nil
	}
	if err := ds.ValidatePluginAssetPathSegment("collectionKey", trimmed); err != nil {
		return "", err
	}
	return trimmed, nil
}

func encodeJSONOrEmpty(value map[string]interface{}) string {
	if value == nil {
		return "{}"
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func normalizeRawJSON(value json.RawMessage) string {
	if len(value) == 0 {
		return "{}"
	}
	var decoded interface{}
	if err := json.Unmarshal(value, &decoded); err != nil {
		return "{}"
	}
	data, err := json.Marshal(decoded)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func decodeJSONValue(raw string) interface{} {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]interface{}{}
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return map[string]interface{}{}
	}
	if decoded == nil {
		return map[string]interface{}{}
	}
	return decoded
}

func decodeJSONObject(raw string) map[string]interface{} {
	decoded, ok := decodeJSONValue(raw).(map[string]interface{})
	if !ok || decoded == nil {
		return map[string]interface{}{}
	}
	return decoded
}

func generatePluginAssetID() uint64 {
	if util.IdWorker == nil {
		util.Setup()
	}
	return uint64(util.IdWorker.Generate().Int64())
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
