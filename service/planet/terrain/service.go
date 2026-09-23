package terrain

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"senspace/domain"
	planet_surface "senspace/domain/planet/surface"
	terrain_domain "senspace/domain/planet/terrain"
	"senspace/domain/sys"
	"senspace/pkg/app/security"
	"senspace/pkg/bizerr"
	road_service "senspace/service/planet/road"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// 当前支持的地形状态结构版本。
	currentSchemaVersion = 9
	// 单个地形状态允许的最大字节数。
	maxStateBytes = 2 * 1024 * 1024
	// 单个星球允许的平台记录上限。
	maxPlatforms = 128
	// 单个星球允许的物件记录上限。
	maxObjects = 5000
	// 单个星球允许的组合体记录上限。
	maxAssemblies = 512
	// 单个星球允许的预制体记录上限。
	maxPrefabs = 256
	// 单个组合体允许引用的物件上限。
	maxAssemblyMembers = 512
	// 单个预制体允许保存的部件上限。
	maxPrefabParts = 512
	// 单星球紧凑植被批次和实例预算。
	maxVegetationPatches           = 2048
	maxVegetationInstancesPerPatch = 4096
	maxVegetationInstances         = 50000
	// 稀疏覆盖层和有效遮罩块预算。
	maxVegetationCoverageLayers = 512
	maxVegetationCoverageTiles  = 384
)

// 空地形状态的规范 JSON 表示。
var emptyState = json.RawMessage(`{"platforms":[],"objects":[],"assemblies":[],"prefabs":[],"vegetationPatches":[],"vegetationCoverageLayers":[]}`)

const (
	// 地形记录 ID 的最大长度。
	maxRecordIdLength = 128
	// 位置和旋转分量的绝对值上限。
	maxTransformComponent = 1_000_000
	// 缩放分量的最大值。
	maxScaleComponent = 1_000
	// 物件变体种子的最大值。
	maxTerrainVariantSeed = 2_147_483_647
)

// 允许发布的地形物件预设。
var terrainObjectPresetIds = map[string]struct{}{
	"shape-box": {}, "shape-wedge": {}, "shape-corner": {}, "shape-frustum": {}, "shape-prism": {}, "shape-pipe": {}, "shape-arc": {}, "shape-dome": {}, "shape-capsule": {}, "shape-extrude": {}, "shape-sweep": {}, "shape-lathe": {}, "shape-roof": {}, "shape-mesh": {},
	"cypress":           {},
	"shrub":             {},
	"grass-clump":       {},
	"daisy-patch":       {},
	"tulip-patch":       {},
	"trumpet-flower":    {},
	"fern":              {},
	"moss-patch":        {},
	"lichen-patch":      {},
	"fallen-leaf-clump": {},
	"twig-clump":        {},
	"lotus-cluster":     {},
	"rock":              {},
	"rock-pillar":       {},
	"rock-slab":         {},
	"rock-ridge":        {},
	"pebble-rock":       {},
	"box":               {},
	"sphere":            {},
	"cylinder":          {},
	"cone":              {},
	"wedge":             {},
	"frustum":           {},
	"octagonal-prism":   {},
	"torus":             {},
	"pyramid":           {},
	"arch":              {},
	"wall-door":         {},
	"wall-window":       {},
	"house-door":        {},
	"house-window":      {},
	"house-gable":       {},
	"house-roof":        {},
	"house-lamp":        {},
	"pavilion-roof":     {},
	"pavilion-column":   {},
	"pavilion-railing":  {},
	"stone-table":       {},
	"stone-stool":       {},
}

// 植物不能成为精确贴附宿主。
var terrainPlantPresetIds = map[string]struct{}{
	"cypress": {}, "shrub": {}, "grass-clump": {}, "daisy-patch": {},
	"tulip-patch": {}, "trumpet-flower": {}, "fern": {}, "moss-patch": {},
	"lichen-patch": {}, "fallen-leaf-clump": {}, "twig-clump": {}, "lotus-cluster": {},
}

// 分类到几何资源的唯一归属；覆盖分类不包含模型。
var terrainVegetationCategoryPresetIds = map[string]map[string]struct{}{
	"moss-coverage": {}, "lichen-coverage": {}, "fallen-leaves-coverage": {},
	"grass":                {"grass-clump": {}},
	"fern":                 {"fern": {}},
	"flowers":              {"daisy-patch": {}, "tulip-patch": {}, "trumpet-flower": {}},
	"moss-accent":          {"moss-patch": {}},
	"lichen-accent":        {"lichen-patch": {}},
	"fallen-leaves-accent": {"fallen-leaf-clump": {}},
	"twigs":                {"twig-clump": {}},
}

// 允许发布的围栏模型。
var fenceModelIds = map[string]struct{}{
	"brick-curb":        {},
	"road-curb":         {},
	"highway-guardrail": {},
	"arrow-barrier":     {},
	"park-railing":      {},
	"brick-wall":        {},
}

// 允许发布的围栏纹理。
var fenceMaterialIds = map[string]struct{}{
	"brick": {},
	"wood":  {},
	"steel": {},
}

// 服务端校验后的独立位置、旋转与缩放。
type terrainTransform struct {
	Position []float64 `json:"position"`
	Rotation []float64 `json:"rotation"`
	Scale    []float64 `json:"scale"`
}

// terrainFence 保存平台外边界围栏样式。
type terrainFence struct {
	ModelId    string `json:"modelId"`
	MaterialId string `json:"materialId"`
}

// 允许发布的平台记录。
type terrainPlatform struct {
	Id          string             `json:"id"`
	Kind        string             `json:"kind"`
	MaterialId  string             `json:"materialId"`
	Transform   terrainTransform   `json:"transform"`
	HeightField terrainHeightField `json:"heightField"`
	Fence       *terrainFence      `json:"fence,omitempty"`
}

// terrainObjectInteraction 保存静态构件的有限动态自由度。
type terrainObjectInteraction struct {
	HingeAngle *float64 `json:"hingeAngle,omitempty"`
}

// terrainSurfaceAnchor 表示简单局部挂点或稳定 LOD0 三角形锚点。
type terrainSurfaceAnchor struct {
	Kind               string            `json:"kind"`
	HostObjectId       string            `json:"hostObjectId"`
	LocalTransform     *terrainTransform `json:"localTransform,omitempty"`
	TriangleIndex      *int              `json:"triangleIndex,omitempty"`
	Barycentric        []float64         `json:"barycentric,omitempty"`
	FallbackLocalPoint []float64         `json:"fallbackLocalPoint,omitempty"`
	TangentRotation    *float64          `json:"tangentRotation,omitempty"`
	NormalOffset       *float64          `json:"normalOffset,omitempty"`
}

// 允许发布的实例物件记录。
type terrainObject struct {
	Id                string                    `json:"id"`
	Kind              string                    `json:"kind"`
	PlatformId        string                    `json:"platformId,omitempty"`
	PresetId          string                    `json:"presetId"`
	MaterialId        string                    `json:"materialId,omitempty"`
	Transform         terrainTransform          `json:"transform"`
	VariantSeed       int64                     `json:"variantSeed"`
	Interaction       *terrainObjectInteraction `json:"interaction,omitempty"`
	SurfaceAttachment *terrainSurfaceAnchor     `json:"surfaceAttachment,omitempty"`
	// 参数化形状源数据，随预制体一起保存。
	Shape *terrainShape `json:"shape,omitempty"`
}

// terrainAssembly 保存一组物件的稳定编辑关系。
type terrainAssembly struct {
	Id        string           `json:"id"`
	Kind      string           `json:"kind"`
	Name      string           `json:"name"`
	Transform terrainTransform `json:"transform"`
	MemberIds []string         `json:"memberIds"`
}

// terrainPrefabPart 保存相对预制体枢轴的一个部件。
type terrainPrefabPart struct {
	PresetId              string                    `json:"presetId"`
	MaterialId            string                    `json:"materialId,omitempty"`
	Transform             terrainTransform          `json:"transform"`
	VariantSeed           int64                     `json:"variantSeed"`
	Interaction           *terrainObjectInteraction `json:"interaction,omitempty"`
	SurfaceHostPartIndex  *int                      `json:"surfaceHostPartIndex,omitempty"`
	SurfaceLocalTransform *terrainTransform         `json:"surfaceLocalTransform,omitempty"`
	// 参数化形状源数据，随预制体一起保存。
	Shape *terrainShape `json:"shape,omitempty"`
}

// terrainPrefabVegetationPatch 保存相对具体预制体宿主部件的网格锚点。
type terrainPrefabVegetationPatch struct {
	SurfaceHostPartIndex int                              `json:"surfaceHostPartIndex"`
	CategoryId           string                           `json:"categoryId"`
	PresetId             string                           `json:"presetId"`
	Seed                 int64                            `json:"seed"`
	Instances            []terrainVegetationPatchInstance `json:"instances"`
}

// terrainPrefabVegetationCoverage 保存预制体宿主上的三平面稀疏遮罩。
type terrainPrefabVegetationCoverage struct {
	SurfaceHostPartIndex int                             `json:"surfaceHostPartIndex"`
	CategoryId           string                          `json:"categoryId"`
	Seed                 int64                           `json:"seed"`
	Tiles                []terrainVegetationCoverageTile `json:"tiles"`
}

// terrainPrefab 保存当前星球内可重复放置的用户预制体。
type terrainPrefab struct {
	Id                       string                            `json:"id"`
	Kind                     string                            `json:"kind"`
	Name                     string                            `json:"name"`
	Parts                    []terrainPrefabPart               `json:"parts"`
	VegetationPatches        []terrainPrefabVegetationPatch    `json:"vegetationPatches"`
	VegetationCoverageLayers []terrainPrefabVegetationCoverage `json:"vegetationCoverageLayers"`
}

// 高度场中的稀疏压缩块。
type terrainHeightChunk struct {
	X        int    `json:"x"`
	Z        int    `json:"z"`
	Encoding string `json:"encoding"`
	Code     *int   `json:"code,omitempty"`
	Data     string `json:"data,omitempty"`
}

// 第一版固定参数的高度场。
type terrainHeightField struct {
	Version         int                  `json:"version"`
	Enabled         bool                 `json:"enabled"`
	BaseHeight      float64              `json:"baseHeight"`
	CellSize        float64              `json:"cellSize"`
	SamplesPerChunk int                  `json:"samplesPerChunk"`
	HeightUnit      float64              `json:"heightUnit"`
	ZeroCode        int                  `json:"zeroCode"`
	Chunks          []terrainHeightChunk `json:"chunks"`
}

// terrainVegetationPatchSurface 绑定具体平台或实际命中的组合成员。
type terrainVegetationPatchSurface struct {
	Kind         string `json:"kind"`
	PlatformId   string `json:"platformId,omitempty"`
	HostObjectId string `json:"hostObjectId,omitempty"`
}

// terrainVegetationPatchInstance 是无需独立物件记录的紧凑实例。
type terrainVegetationPatchInstance struct {
	Kind               string    `json:"kind"`
	LocalXZ            []float64 `json:"localXZ,omitempty"`
	TriangleIndex      *int      `json:"triangleIndex,omitempty"`
	Barycentric        []float64 `json:"barycentric,omitempty"`
	FallbackLocalPoint []float64 `json:"fallbackLocalPoint,omitempty"`
	TangentRotation    float64   `json:"tangentRotation"`
	NormalOffset       float64   `json:"normalOffset"`
	Scale              []float64 `json:"scale"`
	VariantSeed        int64     `json:"variantSeed"`
}

// terrainVegetationPatch 按宿主、分类和品种保存紧凑实例。
type terrainVegetationPatch struct {
	Id         string                           `json:"id"`
	Kind       string                           `json:"kind"`
	PresetId   string                           `json:"presetId"`
	CategoryId string                           `json:"categoryId"`
	Seed       int64                            `json:"seed"`
	Surface    terrainVegetationPatchSurface    `json:"surface"`
	Instances  []terrainVegetationPatchInstance `json:"instances"`
}

// terrainVegetationCoverageTile 是固定容量 RGBA8 稀疏遮罩块。
type terrainVegetationCoverageTile struct {
	ProjectionAxis string `json:"projectionAxis"`
	TileX          int    `json:"tileX"`
	TileY          int    `json:"tileY"`
	Resolution     int    `json:"resolution"`
	Encoding       string `json:"encoding"`
	Data           string `json:"data"`
}

// terrainVegetationCoverageLayer 保存一个宿主上的一种覆盖材质。
type terrainVegetationCoverageLayer struct {
	Id         string                          `json:"id"`
	Kind       string                          `json:"kind"`
	CategoryId string                          `json:"categoryId"`
	Surface    terrainVegetationPatchSurface   `json:"surface"`
	Seed       int64                           `json:"seed"`
	Projection string                          `json:"projection"`
	Tiles      []terrainVegetationCoverageTile `json:"tiles"`
}

// 当前地形完整发布载荷。
type terrainState struct {
	Platforms                []terrainPlatform                `json:"platforms"`
	Objects                  []terrainObject                  `json:"objects"`
	Assemblies               []terrainAssembly                `json:"assemblies"`
	Prefabs                  []terrainPrefab                  `json:"prefabs"`
	VegetationPatches        []terrainVegetationPatch         `json:"vegetationPatches"`
	VegetationCoverageLayers []terrainVegetationCoverageLayer `json:"vegetationCoverageLayers"`
}

// 公开读取一个星球当前发布的地形。
func GetPublished(planetId int) (*DocumentResponse, error) {
	if planetId <= 0 {
		return nil, bizerr.Parameter("planetId无效")
	}
	if domain.Db == nil {
		return nil, fmt.Errorf("planet terrain db not initialized")
	}
	var document terrain_domain.Document
	err := domain.Db.First(&document, "planet_id = ?", planetId).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		hash := sha256.Sum256(emptyState)
		return &DocumentResponse{
			SchemaVersion: currentSchemaVersion,
			Revision:      0,
			UpdatedAt:     time.Unix(0, 0).UTC().Format(time.RFC3339Nano),
			ContentHash:   hex.EncodeToString(hash[:]),
			State:         append(json.RawMessage(nil), emptyState...),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	response := mapDocument(document)
	if response.SchemaVersion != document.SchemaVersion ||
		string(response.State) != document.StateJson {
		if err := domain.Db.Model(&terrain_domain.Document{}).
			Where("planet_id = ?", planetId).
			UpdateColumns(map[string]interface{}{
				"schema_version": response.SchemaVersion,
				"state_json":     string(response.State),
				"content_hash":   response.ContentHash,
			}).Error; err != nil {
			return nil, fmt.Errorf("clean automatic terrain data: %w", err)
		}
	}
	return response, nil
}

// 校验星球真实归属并以乐观锁保存地形。
func SavePublished(
	planetId int,
	request SaveRequest,
	user *security.JwtUser,
) (*DocumentResponse, error) {
	if planetId <= 0 {
		return nil, bizerr.Parameter("planetId无效")
	}
	if user == nil || user.Id == 0 {
		return nil, bizerr.Unauthorized()
	}
	if request.ExpectedRevision < 0 {
		return nil, bizerr.Parameter("expectedRevision无效")
	}
	if request.SchemaVersion != currentSchemaVersion {
		return nil, bizerr.Parameter("不支持的地形结构版本")
	}
	normalizedState, err := validateState(request.State)
	if err != nil {
		return nil, err
	}
	if domain.Db == nil {
		return nil, fmt.Errorf("planet terrain db not initialized")
	}

	var saved terrain_domain.Document
	err = domain.Db.Transaction(func(tx *gorm.DB) error {
		var databaseUser sys.User
		if err := tx.Select("id", "planet_id").First(&databaseUser, user.Id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return bizerr.Unauthorized()
			}
			return err
		}
		// 权限以数据库中的 sys_user.planet_id 为准，不能只信任 JWT 快照。
		if databaseUser.PlanetId != planetId {
			return bizerr.Forbidden("只有星球主人可以保存地形")
		}

		var current terrain_domain.Document
		findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&current, "planet_id = ?", planetId).Error
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			if request.ExpectedRevision != 0 {
				return bizerr.Conflict("地形版本已更新，请重新加载")
			}
			now := time.Now().UTC()
			hash := sha256.Sum256(normalizedState)
			saved = terrain_domain.Document{
				PlanetId:      planetId,
				SchemaVersion: request.SchemaVersion,
				Revision:      1,
				StateJson:     string(normalizedState),
				ContentHash:   hex.EncodeToString(hash[:]),
				CreatInfo: domain.CreatInfo{
					CreatedAt: now,
					CreatedBy: user.Id,
				},
				UpdateInfo: domain.UpdateInfo{
					UpdatedAt: now,
					UpdatedBy: user.Id,
				},
			}
			return tx.Create(&saved).Error
		}
		if findErr != nil {
			return findErr
		}
		if current.Revision != request.ExpectedRevision {
			return bizerr.Conflict("地形版本已更新，请重新加载")
		}
		removedPlatformIds, err := findRemovedPlatformIds(
			json.RawMessage(current.StateJson),
			normalizedState,
		)
		if err != nil {
			return fmt.Errorf("resolve removed terrain platforms: %w", err)
		}

		hash := sha256.Sum256(normalizedState)
		current.SchemaVersion = request.SchemaVersion
		current.Revision++
		current.StateJson = string(normalizedState)
		current.ContentHash = hex.EncodeToString(hash[:])
		current.UpdatedAt = time.Now().UTC()
		current.UpdatedBy = user.Id
		if err := tx.Save(&current).Error; err != nil {
			return err
		}
		if err := road_service.RemovePlatformAnchorsTx(
			tx,
			planetId,
			removedPlatformIds,
			user.Id,
		); err != nil {
			return err
		}
		saved = current
		return nil
	})
	if err != nil {
		return nil, err
	}
	return mapDocument(saved), nil
}

// findRemovedPlatformIds 比较前后状态，只返回本次真正删除的平台。
func findRemovedPlatformIds(
	currentState json.RawMessage,
	nextState json.RawMessage,
) (map[string]struct{}, error) {
	var current terrainState
	if err := json.Unmarshal(currentState, &current); err != nil {
		return nil, err
	}
	var next terrainState
	if err := json.Unmarshal(nextState, &next); err != nil {
		return nil, err
	}
	nextIds := make(map[string]struct{}, len(next.Platforms))
	for _, platform := range next.Platforms {
		nextIds[platform.Id] = struct{}{}
	}
	removedIds := make(map[string]struct{})
	for _, platform := range current.Platforms {
		if _, exists := nextIds[platform.Id]; !exists {
			removedIds[platform.Id] = struct{}{}
		}
	}
	return removedIds, nil
}

// 校验完整结构、有限姿态、预设白名单与记录上限。
func validateState(state json.RawMessage) (json.RawMessage, error) {
	if len(state) == 0 || len(state) > maxStateBytes || !json.Valid(state) {
		return nil, bizerr.Parameter("地形状态无效或过大")
	}
	var envelope terrainState
	decoder := json.NewDecoder(bytes.NewReader(state))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, bizerr.Parameter("地形状态结构无效")
	}
	if err := ensureJsonEnd(decoder); err != nil {
		return nil, bizerr.Parameter("地形状态必须是对象")
	}
	if envelope.Platforms == nil || envelope.Objects == nil {
		return nil, bizerr.Parameter("地形状态必须包含platforms和objects")
	}
	// v5 新集合在首个兼容发布中允许缺省，并规范化为空数组。
	if envelope.Assemblies == nil {
		envelope.Assemblies = []terrainAssembly{}
	}
	if envelope.Prefabs == nil {
		envelope.Prefabs = []terrainPrefab{}
	}
	if envelope.VegetationPatches == nil {
		envelope.VegetationPatches = []terrainVegetationPatch{}
	}
	if envelope.VegetationCoverageLayers == nil {
		envelope.VegetationCoverageLayers = []terrainVegetationCoverageLayer{}
	}
	if len(envelope.Platforms) > maxPlatforms {
		return nil, bizerr.Parameter("地形平面数量超过上限")
	}
	if len(envelope.Objects) > maxObjects {
		return nil, bizerr.Parameter("地形物件数量超过上限")
	}
	if len(envelope.Assemblies) > maxAssemblies {
		return nil, bizerr.Parameter("地形组合体数量超过上限")
	}
	if len(envelope.Prefabs) > maxPrefabs {
		return nil, bizerr.Parameter("地形预制体数量超过上限")
	}
	if len(envelope.VegetationPatches) > maxVegetationPatches {
		return nil, bizerr.Parameter("植被批次数量超过上限")
	}
	if len(envelope.VegetationCoverageLayers) > maxVegetationCoverageLayers {
		return nil, bizerr.Parameter("植被覆盖层数量超过上限")
	}

	usedIds := make(map[string]struct{}, len(envelope.Platforms)+len(envelope.Objects)+len(envelope.Assemblies)+len(envelope.Prefabs)+len(envelope.VegetationPatches)+len(envelope.VegetationCoverageLayers))
	platformIds := make(map[string]struct{}, len(envelope.Platforms))
	objectIds := make(map[string]struct{}, len(envelope.Objects))
	for _, platform := range envelope.Platforms {
		if platform.Kind != "platform" {
			return nil, bizerr.Parameter("地形平台kind无效")
		}
		if !planet_surface.IsMaterialID(platform.MaterialId) {
			return nil, bizerr.Parameter("地形平台材质无效")
		}
		if err := validateTerrainRecordId(platform.Id, usedIds); err != nil {
			return nil, err
		}
		if !validTerrainTransform(platform.Transform) {
			return nil, bizerr.Parameter("地形平台变换无效")
		}
		if err := validateTerrainHeightField(platform.HeightField); err != nil {
			return nil, err
		}
		if err := validateFence(platform.Fence); err != nil {
			return nil, err
		}
		platformIds[platform.Id] = struct{}{}
	}
	for _, object := range envelope.Objects {
		if object.Kind != "object" {
			return nil, bizerr.Parameter("地形物件kind无效")
		}
		if _, exists := terrainObjectPresetIds[object.PresetId]; !exists {
			return nil, bizerr.Parameter("地形物件预设无效")
		}
		if object.MaterialId != "" {
			if !planet_surface.IsMaterialID(object.MaterialId) {
				return nil, bizerr.Parameter("地形物件纹理无效")
			}
		}
		if object.VariantSeed < 0 || object.VariantSeed > maxTerrainVariantSeed {
			return nil, bizerr.Parameter("地形物件variantSeed无效")
		}
		if object.PlatformId != "" {
			if _, exists := platformIds[object.PlatformId]; !exists {
				return nil, bizerr.Parameter("地形物件所属平台无效")
			}
		}
		if err := validateTerrainRecordId(object.Id, usedIds); err != nil {
			return nil, err
		}
		if !validTerrainTransform(object.Transform) {
			return nil, bizerr.Parameter("地形物件变换无效")
		}
		if err := validateTerrainShape(object.PresetId, object.Shape); err != nil {
			return nil, err
		}
		if err := validateTerrainObjectInteraction(object.PresetId, object.Interaction); err != nil {
			return nil, err
		}
		objectIds[object.Id] = struct{}{}
	}
	objectPresetById := make(map[string]string, len(envelope.Objects))
	for _, object := range envelope.Objects {
		objectPresetById[object.Id] = object.PresetId
	}
	for _, object := range envelope.Objects {
		if err := validateTerrainSurfaceAnchor(object.Id, object.SurfaceAttachment, objectIds, objectPresetById); err != nil {
			return nil, err
		}
	}
	if err := validateTerrainSurfaceAttachmentCycles(envelope.Objects); err != nil {
		return nil, err
	}
	groupedObjectIds := make(map[string]struct{})
	for _, assembly := range envelope.Assemblies {
		if assembly.Kind != "assembly" {
			return nil, bizerr.Parameter("地形组合体kind无效")
		}
		if err := validateTerrainRecordId(assembly.Id, usedIds); err != nil {
			return nil, err
		}
		if strings.TrimSpace(assembly.Name) == "" || utf8.RuneCountInString(assembly.Name) > 80 {
			return nil, bizerr.Parameter("地形组合体名称无效")
		}
		if !validTerrainTransform(assembly.Transform) {
			return nil, bizerr.Parameter("地形组合体变换无效")
		}
		if len(assembly.MemberIds) < 2 || len(assembly.MemberIds) > maxAssemblyMembers {
			return nil, bizerr.Parameter("地形组合体成员数量无效")
		}
		assemblyMemberIds := make(map[string]struct{}, len(assembly.MemberIds))
		for _, memberId := range assembly.MemberIds {
			if _, exists := objectIds[memberId]; !exists {
				return nil, bizerr.Parameter("地形组合体成员不存在")
			}
			if _, exists := assemblyMemberIds[memberId]; exists {
				return nil, bizerr.Parameter("地形组合体成员重复")
			}
			if _, exists := groupedObjectIds[memberId]; exists {
				return nil, bizerr.Parameter("地形物件不能属于多个组合体")
			}
			assemblyMemberIds[memberId] = struct{}{}
			groupedObjectIds[memberId] = struct{}{}
		}
	}
	for prefabIndex := range envelope.Prefabs {
		prefab := &envelope.Prefabs[prefabIndex]
		if prefab.Kind != "prefab" {
			return nil, bizerr.Parameter("地形预制体kind无效")
		}
		if err := validateTerrainRecordId(prefab.Id, usedIds); err != nil {
			return nil, err
		}
		if strings.TrimSpace(prefab.Name) == "" || utf8.RuneCountInString(prefab.Name) > 80 {
			return nil, bizerr.Parameter("地形预制体名称无效")
		}
		if len(prefab.Parts) == 0 || len(prefab.Parts) > maxPrefabParts {
			return nil, bizerr.Parameter("地形预制体部件数量无效")
		}
		for _, part := range prefab.Parts {
			if err := validateTerrainPrefabPart(part, len(prefab.Parts), prefab.Parts); err != nil {
				return nil, err
			}
		}
		if prefab.VegetationPatches == nil {
			prefab.VegetationPatches = []terrainPrefabVegetationPatch{}
		}
		if prefab.VegetationCoverageLayers == nil {
			prefab.VegetationCoverageLayers = []terrainPrefabVegetationCoverage{}
		}
		if len(prefab.VegetationPatches) > maxVegetationPatches ||
			len(prefab.VegetationCoverageLayers) > maxVegetationCoverageLayers {
			return nil, bizerr.Parameter("地形预制体植被记录超过上限")
		}
		prefabInstanceCount := 0
		for _, patch := range prefab.VegetationPatches {
			if err := validateTerrainPrefabVegetationPatch(patch, prefab.Parts); err != nil {
				return nil, err
			}
			prefabInstanceCount += len(patch.Instances)
			if prefabInstanceCount > maxVegetationInstances {
				return nil, bizerr.Parameter("地形预制体植被实例超过上限")
			}
		}
		prefabTileCount := 0
		for _, layer := range prefab.VegetationCoverageLayers {
			if err := validateTerrainPrefabVegetationCoverage(layer, prefab.Parts); err != nil {
				return nil, err
			}
			prefabTileCount += len(layer.Tiles)
			if prefabTileCount > maxVegetationCoverageTiles {
				return nil, bizerr.Parameter("地形预制体覆盖块超过上限")
			}
		}
	}
	vegetationInstances := 0
	for _, patch := range envelope.VegetationPatches {
		if err := validateTerrainVegetationPatch(patch, usedIds, platformIds, objectIds, objectPresetById); err != nil {
			return nil, err
		}
		vegetationInstances += len(patch.Instances)
		if vegetationInstances > maxVegetationInstances {
			return nil, bizerr.Parameter("植被实例总数超过上限")
		}
	}
	coverageTiles := 0
	for _, layer := range envelope.VegetationCoverageLayers {
		if err := validateTerrainVegetationCoverageLayer(layer, usedIds, platformIds, objectIds, objectPresetById); err != nil {
			return nil, err
		}
		coverageTiles += len(layer.Tiles)
		if coverageTiles > maxVegetationCoverageTiles {
			return nil, bizerr.Parameter("植被覆盖块总数超过上限")
		}
	}
	normalized, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal terrain state: %w", err)
	}
	return normalized, nil
}

// validateTerrainPrefabPart 校验预制体内部的可实例化部件。
func validateTerrainPrefabPart(part terrainPrefabPart, partCount int, parts []terrainPrefabPart) error {
	if _, exists := terrainObjectPresetIds[part.PresetId]; !exists {
		return bizerr.Parameter("地形预制体部件预设无效")
	}
	if part.MaterialId != "" {
		if !planet_surface.IsMaterialID(part.MaterialId) {
			return bizerr.Parameter("地形预制体部件纹理无效")
		}
	}
	if part.VariantSeed < 0 || part.VariantSeed > maxTerrainVariantSeed {
		return bizerr.Parameter("地形预制体部件variantSeed无效")
	}
	if !validTerrainTransform(part.Transform) {
		return bizerr.Parameter("地形预制体部件变换无效")
	}
	if err := validateTerrainShape(part.PresetId, part.Shape); err != nil {
		return err
	}
	if err := validateTerrainObjectInteraction(part.PresetId, part.Interaction); err != nil {
		return err
	}
	if (part.SurfaceHostPartIndex == nil) != (part.SurfaceLocalTransform == nil) {
		return bizerr.Parameter("地形预制体贴附字段不完整")
	}
	if part.SurfaceHostPartIndex != nil {
		if *part.SurfaceHostPartIndex < 0 || *part.SurfaceHostPartIndex >= partCount ||
			*part.SurfaceHostPartIndex >= len(parts) {
			return bizerr.Parameter("地形预制体贴附宿主无效")
		}
		if _, isPlant := terrainPlantPresetIds[parts[*part.SurfaceHostPartIndex].PresetId]; isPlant {
			return bizerr.Parameter("植物不能承载预制体贴附")
		}
		if !validTerrainTransform(*part.SurfaceLocalTransform) {
			return bizerr.Parameter("地形预制体局部贴附变换无效")
		}
	}
	return nil
}

// validateTerrainPrefabVegetationPatch 校验预制体内部的精确网格锚点。
func validateTerrainPrefabVegetationPatch(
	patch terrainPrefabVegetationPatch,
	parts []terrainPrefabPart,
) error {
	if patch.SurfaceHostPartIndex < 0 || patch.SurfaceHostPartIndex >= len(parts) {
		return bizerr.Parameter("地形预制体植被宿主无效")
	}
	if _, isPlant := terrainPlantPresetIds[parts[patch.SurfaceHostPartIndex].PresetId]; isPlant {
		return bizerr.Parameter("植物不能承载预制体笔刷植被")
	}
	presets, exists := terrainVegetationCategoryPresetIds[patch.CategoryId]
	if !exists {
		return bizerr.Parameter("地形预制体植被分类无效")
	}
	if _, exists := presets[patch.PresetId]; !exists {
		return bizerr.Parameter("地形预制体植被品种不属于分类")
	}
	if patch.Seed < 0 || patch.Seed > maxTerrainVariantSeed ||
		len(patch.Instances) == 0 || len(patch.Instances) > maxVegetationInstancesPerPatch {
		return bizerr.Parameter("地形预制体植被种子或数量无效")
	}
	for _, instance := range patch.Instances {
		if instance.Kind != "mesh" || len(instance.LocalXZ) != 0 ||
			instance.TriangleIndex == nil || *instance.TriangleIndex < 0 ||
			*instance.TriangleIndex > 100_000_000 ||
			!validTerrainBarycentric(instance.Barycentric) ||
			!validTerrainVector(instance.FallbackLocalPoint, false, maxTransformComponent) ||
			!validFinite(instance.TangentRotation, maxTransformComponent) ||
			!validFinite(instance.NormalOffset, 1) ||
			!validTerrainVector(instance.Scale, true, maxScaleComponent) ||
			instance.VariantSeed < 0 || instance.VariantSeed > maxTerrainVariantSeed {
			return bizerr.Parameter("地形预制体网格植被实例无效")
		}
	}
	return nil
}

// validateTerrainPrefabVegetationCoverage 校验预制体内部三平面遮罩。
func validateTerrainPrefabVegetationCoverage(
	layer terrainPrefabVegetationCoverage,
	parts []terrainPrefabPart,
) error {
	if layer.SurfaceHostPartIndex < 0 || layer.SurfaceHostPartIndex >= len(parts) {
		return bizerr.Parameter("地形预制体覆盖宿主无效")
	}
	if _, isPlant := terrainPlantPresetIds[parts[layer.SurfaceHostPartIndex].PresetId]; isPlant {
		return bizerr.Parameter("植物不能承载预制体覆盖")
	}
	if layer.CategoryId != "moss-coverage" && layer.CategoryId != "lichen-coverage" &&
		layer.CategoryId != "fallen-leaves-coverage" {
		return bizerr.Parameter("地形预制体覆盖分类无效")
	}
	if layer.Seed < 0 || layer.Seed > maxTerrainVariantSeed || len(layer.Tiles) == 0 {
		return bizerr.Parameter("地形预制体覆盖种子或块无效")
	}
	usedTiles := make(map[string]struct{}, len(layer.Tiles))
	for _, tile := range layer.Tiles {
		if tile.Encoding != "predict-rle-base64" ||
			(tile.Resolution != 128 && tile.Resolution != 256) ||
			tile.TileX < -32768 || tile.TileX > 32768 || tile.TileY < -32768 || tile.TileY > 32768 ||
			(tile.ProjectionAxis != "xz" && tile.ProjectionAxis != "xy" && tile.ProjectionAxis != "yz") {
			return bizerr.Parameter("地形预制体覆盖块参数无效")
		}
		key := fmt.Sprintf("%s:%d:%d", tile.ProjectionAxis, tile.TileX, tile.TileY)
		if _, exists := usedTiles[key]; exists {
			return bizerr.Parameter("地形预制体覆盖块重复")
		}
		usedTiles[key] = struct{}{}
		hasCoverage, err := validateTerrainVegetationCoverageData(
			tile.Data,
			tile.Resolution*tile.Resolution*4,
		)
		if err != nil {
			return err
		}
		if !hasCoverage {
			return bizerr.Parameter("地形预制体覆盖块不能空白")
		}
	}
	return nil
}

// validateTerrainObjectInteraction 限制通用状态只能用于声明过的静态门。
func validateTerrainObjectInteraction(presetId string, interaction *terrainObjectInteraction) error {
	if interaction == nil {
		return nil
	}
	if presetId != "house-door" || interaction.HingeAngle == nil ||
		!validFinite(*interaction.HingeAngle, math.Pi*2) ||
		*interaction.HingeAngle < 0 || *interaction.HingeAngle > 1.78 {
		return bizerr.Parameter("地形物件交互状态无效")
	}
	return nil
}

// validateTerrainSurfaceAnchor 校验简单挂点或稳定三角形锚点。
func validateTerrainSurfaceAnchor(
	ownerId string,
	anchor *terrainSurfaceAnchor,
	objectIds map[string]struct{},
	objectPresetById map[string]string,
) error {
	if anchor == nil {
		return nil
	}
	if anchor.HostObjectId == ownerId {
		return bizerr.Parameter("地形贴附不能引用自身")
	}
	if _, exists := objectIds[anchor.HostObjectId]; !exists {
		return bizerr.Parameter("地形贴附宿主不存在")
	}
	if _, isPlant := terrainPlantPresetIds[objectPresetById[anchor.HostObjectId]]; isPlant {
		return bizerr.Parameter("植物不能承载植被")
	}
	switch anchor.Kind {
	case "simple":
		if anchor.LocalTransform == nil || !validTerrainTransform(*anchor.LocalTransform) ||
			anchor.TriangleIndex != nil || len(anchor.Barycentric) != 0 ||
			len(anchor.FallbackLocalPoint) != 0 || anchor.TangentRotation != nil ||
			anchor.NormalOffset != nil {
			return bizerr.Parameter("简单地形贴附无效")
		}
	case "mesh":
		if anchor.LocalTransform != nil || anchor.TriangleIndex == nil ||
			*anchor.TriangleIndex < 0 || *anchor.TriangleIndex > 100_000_000 ||
			!validTerrainBarycentric(anchor.Barycentric) ||
			!validTerrainVector(anchor.FallbackLocalPoint, false, maxTransformComponent) ||
			anchor.TangentRotation == nil || !validFinite(*anchor.TangentRotation, maxTransformComponent) ||
			anchor.NormalOffset == nil || !validFinite(*anchor.NormalOffset, 1) {
			return bizerr.Parameter("精确地形贴附无效")
		}
	default:
		return bizerr.Parameter("地形贴附类型无效")
	}
	return nil
}

// validateTerrainSurfaceAttachmentCycles 防止简单贴附和精确贴附形成环。
func validateTerrainSurfaceAttachmentCycles(objects []terrainObject) error {
	hostByChild := make(map[string]string)
	for _, object := range objects {
		if object.SurfaceAttachment != nil {
			hostByChild[object.Id] = object.SurfaceAttachment.HostObjectId
		}
	}
	for childId := range hostByChild {
		visited := map[string]struct{}{childId: {}}
		for hostId := hostByChild[childId]; hostId != ""; hostId = hostByChild[hostId] {
			if _, exists := visited[hostId]; exists {
				return bizerr.Parameter("地形贴附不能形成循环")
			}
			visited[hostId] = struct{}{}
		}
	}
	return nil
}

// validateTerrainVegetationSurface 校验 patch/coverage 的稳定宿主引用。
func validateTerrainVegetationSurface(
	surface terrainVegetationPatchSurface,
	platformIds map[string]struct{},
	objectIds map[string]struct{},
	objectPresetById map[string]string,
) error {
	switch surface.Kind {
	case "terrain":
		if surface.HostObjectId != "" {
			return bizerr.Parameter("植被地形宿主字段无效")
		}
		if _, exists := platformIds[surface.PlatformId]; !exists {
			return bizerr.Parameter("植被地形宿主不存在")
		}
	case "object":
		if surface.PlatformId != "" {
			return bizerr.Parameter("植被物件宿主字段无效")
		}
		if _, exists := objectIds[surface.HostObjectId]; !exists {
			return bizerr.Parameter("植被物件宿主不存在")
		}
		if _, isPlant := terrainPlantPresetIds[objectPresetById[surface.HostObjectId]]; isPlant {
			return bizerr.Parameter("植物不能承载笔刷植被")
		}
	default:
		return bizerr.Parameter("植被宿主类型无效")
	}
	return nil
}

// validateTerrainVegetationPatch 校验分类归属、实例类型和容量。
func validateTerrainVegetationPatch(
	patch terrainVegetationPatch,
	usedIds map[string]struct{},
	platformIds map[string]struct{},
	objectIds map[string]struct{},
	objectPresetById map[string]string,
) error {
	if patch.Kind != "vegetation-patch" {
		return bizerr.Parameter("植被批次kind无效")
	}
	if err := validateTerrainRecordId(patch.Id, usedIds); err != nil {
		return err
	}
	presets, categoryExists := terrainVegetationCategoryPresetIds[patch.CategoryId]
	if !categoryExists {
		return bizerr.Parameter("植被批次分类无效")
	}
	if _, exists := presets[patch.PresetId]; !exists {
		return bizerr.Parameter("植被批次品种不属于分类")
	}
	if patch.Seed < 0 || patch.Seed > maxTerrainVariantSeed ||
		len(patch.Instances) == 0 || len(patch.Instances) > maxVegetationInstancesPerPatch {
		return bizerr.Parameter("植被批次种子或数量无效")
	}
	if err := validateTerrainVegetationSurface(patch.Surface, platformIds, objectIds, objectPresetById); err != nil {
		return err
	}
	expectedKind := "mesh"
	if patch.Surface.Kind == "terrain" {
		expectedKind = "terrain"
	}
	for _, instance := range patch.Instances {
		if instance.Kind != expectedKind ||
			!validFinite(instance.TangentRotation, maxTransformComponent) ||
			!validFinite(instance.NormalOffset, 1) ||
			!validTerrainVector(instance.Scale, true, maxScaleComponent) ||
			instance.VariantSeed < 0 || instance.VariantSeed > maxTerrainVariantSeed {
			return bizerr.Parameter("植被实例公共字段无效")
		}
		if expectedKind == "terrain" {
			if len(instance.LocalXZ) != 2 || !validFinite(instance.LocalXZ[0], maxTransformComponent) ||
				!validFinite(instance.LocalXZ[1], maxTransformComponent) || instance.TriangleIndex != nil ||
				len(instance.Barycentric) != 0 || len(instance.FallbackLocalPoint) != 0 {
				return bizerr.Parameter("地形植被实例无效")
			}
		} else if len(instance.LocalXZ) != 0 || instance.TriangleIndex == nil ||
			*instance.TriangleIndex < 0 || *instance.TriangleIndex > 100_000_000 ||
			!validTerrainBarycentric(instance.Barycentric) ||
			!validTerrainVector(instance.FallbackLocalPoint, false, maxTransformComponent) {
			return bizerr.Parameter("网格植被实例无效")
		}
	}
	return nil
}

// validateTerrainVegetationCoverageLayer 校验稀疏遮罩块和投影语义。
func validateTerrainVegetationCoverageLayer(
	layer terrainVegetationCoverageLayer,
	usedIds map[string]struct{},
	platformIds map[string]struct{},
	objectIds map[string]struct{},
	objectPresetById map[string]string,
) error {
	if layer.Kind != "vegetation-coverage" {
		return bizerr.Parameter("植被覆盖层kind无效")
	}
	if err := validateTerrainRecordId(layer.Id, usedIds); err != nil {
		return err
	}
	if layer.CategoryId != "moss-coverage" && layer.CategoryId != "lichen-coverage" &&
		layer.CategoryId != "fallen-leaves-coverage" {
		return bizerr.Parameter("植被覆盖分类无效")
	}
	if layer.Seed < 0 || layer.Seed > maxTerrainVariantSeed || len(layer.Tiles) == 0 {
		return bizerr.Parameter("植被覆盖层种子或块数量无效")
	}
	if err := validateTerrainVegetationSurface(layer.Surface, platformIds, objectIds, objectPresetById); err != nil {
		return err
	}
	expectedProjection := "local-triplanar"
	if layer.Surface.Kind == "terrain" {
		expectedProjection = "terrain-xz"
	}
	if layer.Projection != expectedProjection {
		return bizerr.Parameter("植被覆盖投影无效")
	}
	usedTiles := make(map[string]struct{}, len(layer.Tiles))
	for _, tile := range layer.Tiles {
		if tile.Encoding != "predict-rle-base64" ||
			(tile.Resolution != 128 && tile.Resolution != 256) ||
			tile.TileX < -32768 || tile.TileX > 32768 || tile.TileY < -32768 || tile.TileY > 32768 ||
			(tile.ProjectionAxis != "xz" && tile.ProjectionAxis != "xy" && tile.ProjectionAxis != "yz") ||
			(layer.Surface.Kind == "terrain" && tile.ProjectionAxis != "xz") {
			return bizerr.Parameter("植被覆盖块参数无效")
		}
		key := fmt.Sprintf("%s:%d:%d", tile.ProjectionAxis, tile.TileX, tile.TileY)
		if _, exists := usedTiles[key]; exists {
			return bizerr.Parameter("植被覆盖块坐标重复")
		}
		usedTiles[key] = struct{}{}
		hasCoverage, err := validateTerrainVegetationCoverageData(tile.Data, tile.Resolution*tile.Resolution*4)
		if err != nil {
			return err
		}
		if !hasCoverage {
			return bizerr.Parameter("植被覆盖块不能是空白块")
		}
	}
	return nil
}

// validateTerrainVegetationCoverageData 严格解码固定长度字节 RLE，防止解码炸弹。
func validateTerrainVegetationCoverageData(data string, expectedBytes int) (bool, error) {
	if expectedBytes <= 0 || expectedBytes > 256*256*4 || len(data) == 0 {
		return false, bizerr.Parameter("植被覆盖遮罩大小无效")
	}
	encoded, err := base64.StdEncoding.Strict().DecodeString(data)
	if err != nil {
		return false, bizerr.Parameter("植被覆盖遮罩Base64无效")
	}
	cursor := 0
	decoded := 0
	hasCoverage := false
	for cursor < len(encoded) && decoded < expectedBytes {
		run, nextCursor, ok := readTerrainVarUint(encoded, cursor)
		if !ok || run == 0 || run > uint64(expectedBytes-decoded) {
			return false, bizerr.Parameter("植被覆盖遮罩游程无效")
		}
		cursor = nextCursor
		if cursor >= len(encoded) {
			return false, bizerr.Parameter("植被覆盖遮罩像素缺失")
		}
		hasCoverage = hasCoverage || encoded[cursor] != 0
		cursor++
		decoded += int(run)
	}
	if cursor != len(encoded) || decoded != expectedBytes {
		return false, bizerr.Parameter("植被覆盖遮罩采样数量无效")
	}
	return hasCoverage, nil
}

// validTerrainBarycentric 校验三个有限重心权重且和接近一。
func validTerrainBarycentric(values []float64) bool {
	if len(values) != 3 {
		return false
	}
	sum := 0.0
	for _, value := range values {
		if !validFinite(value, 1.0001) || value < -0.0001 {
			return false
		}
		sum += value
	}
	return math.Abs(sum-1) <= 0.001
}

// validFinite 校验一个受限有限数。
func validFinite(value float64, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Abs(value) <= maximum
}

// validateFence 校验可选围栏模型和纹理白名单。
func validateFence(fence *terrainFence) error {
	if fence == nil {
		return nil
	}
	if _, exists := fenceModelIds[fence.ModelId]; !exists {
		return bizerr.Parameter("地形围栏模型无效")
	}
	if _, exists := fenceMaterialIds[fence.MaterialId]; !exists {
		return bizerr.Parameter("地形围栏纹理无效")
	}
	return nil
}

// 校验固定高度场参数、块唯一性与压缩流完整性。
func validateTerrainHeightField(field terrainHeightField) error {
	if field.Version != 2 ||
		field.CellSize != 0.5 ||
		field.SamplesPerChunk != 64 ||
		field.HeightUnit != 0.005 ||
		field.ZeroCode != 32768 ||
		math.IsNaN(field.BaseHeight) ||
		math.IsInf(field.BaseHeight, 0) ||
		math.Abs(field.BaseHeight) > maxTransformComponent ||
		len(field.Chunks) > 4096 {
		return bizerr.Parameter("地形高度场参数无效")
	}
	if field.Enabled != (len(field.Chunks) > 0) {
		return bizerr.Parameter("地形高度场启用状态无效")
	}
	usedKeys := make(map[string]struct{}, len(field.Chunks))
	for _, chunk := range field.Chunks {
		if chunk.X < -32768 || chunk.X > 32768 ||
			chunk.Z < -32768 || chunk.Z > 32768 {
			return bizerr.Parameter("地形高度块坐标无效")
		}
		key := fmt.Sprintf("%d:%d", chunk.X, chunk.Z)
		if _, exists := usedKeys[key]; exists {
			return bizerr.Parameter("地形高度块坐标重复")
		}
		usedKeys[key] = struct{}{}
		switch chunk.Encoding {
		case "constant":
			if chunk.Code == nil || *chunk.Code < 0 || *chunk.Code > 65535 || chunk.Data != "" {
				return bizerr.Parameter("地形常量高度块无效")
			}
			if *chunk.Code == field.ZeroCode {
				return bizerr.Parameter("地形高度块不能是空白基准块")
			}
		case "delta-rle-v1":
			if chunk.Code != nil || len(chunk.Data) == 0 || len(chunk.Data) > 65536 {
				return bizerr.Parameter("地形压缩高度块无效")
			}
			hasHeightChange, err := validateTerrainHeightChunkData(
				chunk.Data,
				field.SamplesPerChunk*field.SamplesPerChunk,
				field.ZeroCode,
			)
			if err != nil {
				return err
			}
			if !hasHeightChange {
				return bizerr.Parameter("地形高度块不能是空白基准块")
			}
		default:
			return bizerr.Parameter("地形高度块编码无效")
		}
	}
	return nil
}

// 解码差分游程并确认高度、样本数和尾部字节均有效。
func validateTerrainHeightChunkData(
	data string,
	expectedSamples int,
	zeroCode int,
) (bool, error) {
	bytes, err := base64.StdEncoding.Strict().DecodeString(data)
	if err != nil {
		return false, bizerr.Parameter("地形高度块Base64无效")
	}
	cursor := 0
	samples := 0
	previous := 32768
	hasHeightChange := false
	for cursor < len(bytes) && samples < expectedSamples {
		runLength, nextCursor, ok := readTerrainVarUint(bytes, cursor)
		if !ok || runLength == 0 || runLength > uint64(expectedSamples-samples) {
			return false, bizerr.Parameter("地形高度块游程无效")
		}
		cursor = nextCursor
		encodedDelta, nextCursor, ok := readTerrainVarUint(bytes, cursor)
		if !ok {
			return false, bizerr.Parameter("地形高度块残差无效")
		}
		cursor = nextCursor
		delta := int64(encodedDelta >> 1)
		if encodedDelta&1 == 1 {
			delta = -delta - 1
		}
		for index := uint64(0); index < runLength; index++ {
			height := int64(previous) + delta
			if height < 0 || height > 65535 {
				return false, bizerr.Parameter("地形高度块采样越界")
			}
			previous = int(height)
			hasHeightChange = hasHeightChange || previous != zeroCode
			samples++
		}
	}
	if samples != expectedSamples || cursor != len(bytes) {
		return false, bizerr.Parameter("地形高度块采样数量无效")
	}
	return hasHeightChange, nil
}

// 从受限字节流读取一个最多五字节的无符号变长整数。
func readTerrainVarUint(data []byte, cursor int) (uint64, int, bool) {
	var result uint64
	for shift := 0; shift <= 28 && cursor < len(data); shift += 7 {
		value := data[cursor]
		cursor++
		result |= uint64(value&0x7f) << shift
		if value&0x80 == 0 {
			return result, cursor, true
		}
	}
	return 0, cursor, false
}

// 确认根对象后不存在第二段 JSON。
func ensureJsonEnd(decoder *json.Decoder) error {
	var extra interface{}
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("terrain state contains multiple JSON values")
	}
	return err
}

// 校验跨平台和物件唯一的稳定记录 ID。
func validateTerrainRecordId(id string, usedIds map[string]struct{}) error {
	if id == "" || strings.TrimSpace(id) != id || len(id) > maxRecordIdLength {
		return bizerr.Parameter("地形记录id无效")
	}
	if _, exists := usedIds[id]; exists {
		return bizerr.Parameter("地形记录id重复")
	}
	usedIds[id] = struct{}{}
	return nil
}

// 校验位姿数值有限，且缩放分量均为正数。
func validTerrainTransform(transform terrainTransform) bool {
	if !validTerrainVector(transform.Position, false, maxTransformComponent) ||
		!validTerrainVector(transform.Rotation, false, maxTransformComponent) ||
		!validTerrainVector(transform.Scale, true, maxScaleComponent) {
		return false
	}
	return true
}

// 校验精确三分量向量。
func validTerrainVector(values []float64, positive bool, maximum float64) bool {
	if len(values) != 3 {
		return false
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > maximum {
			return false
		}
		if positive && value <= 0 {
			return false
		}
	}
	return true
}

// 把领域模型映射为稳定 API 信封。
func mapDocument(document terrain_domain.Document) *DocumentResponse {
	updatedAt := document.UpdatedAt.UTC()
	if updatedAt.IsZero() {
		updatedAt = document.CreatedAt.UTC()
	}
	state := json.RawMessage(document.StateJson)
	schemaVersion := document.SchemaVersion
	if schemaVersion < currentSchemaVersion {
		var legacy struct {
			Platforms                json.RawMessage `json:"platforms"`
			Objects                  json.RawMessage `json:"objects"`
			Assemblies               json.RawMessage `json:"assemblies"`
			Prefabs                  json.RawMessage `json:"prefabs"`
			VegetationPatches        json.RawMessage `json:"vegetationPatches"`
			VegetationCoverageLayers json.RawMessage `json:"vegetationCoverageLayers"`
		}
		if json.Unmarshal(state, &legacy) == nil {
			var platforms []map[string]interface{}
			if schemaVersion < 3 && json.Unmarshal(legacy.Platforms, &platforms) == nil {
				for _, platform := range platforms {
					platform["heightField"] = map[string]interface{}{
						"version":         2,
						"enabled":         false,
						"baseHeight":      0.006,
						"cellSize":        0.5,
						"samplesPerChunk": 64,
						"heightUnit":      0.005,
						"zeroCode":        32768,
						"chunks":          []interface{}{},
					}
				}
				legacy.Platforms, _ = json.Marshal(platforms)
			}
			if len(legacy.Assemblies) == 0 {
				legacy.Assemblies = json.RawMessage(`[]`)
			}
			if len(legacy.Prefabs) == 0 {
				legacy.Prefabs = json.RawMessage(`[]`)
			}
			if schemaVersion < 7 {
				var objects []map[string]interface{}
				if json.Unmarshal(legacy.Objects, &objects) == nil {
					for _, object := range objects {
						attachment, ok := object["surfaceAttachment"].(map[string]interface{})
						if ok {
							if _, hasKind := attachment["kind"]; !hasKind {
								attachment["kind"] = "simple"
							}
						}
					}
					legacy.Objects, _ = json.Marshal(objects)
				}
			}
			if len(legacy.VegetationPatches) == 0 {
				legacy.VegetationPatches = json.RawMessage(`[]`)
			}
			if len(legacy.VegetationCoverageLayers) == 0 {
				legacy.VegetationCoverageLayers = json.RawMessage(`[]`)
			}
			migrated, err := json.Marshal(map[string]json.RawMessage{
				"platforms":                legacy.Platforms,
				"objects":                  legacy.Objects,
				"assemblies":               legacy.Assemblies,
				"prefabs":                  legacy.Prefabs,
				"vegetationPatches":        legacy.VegetationPatches,
				"vegetationCoverageLayers": legacy.VegetationCoverageLayers,
			})
			if err == nil {
				state = migrated
				schemaVersion = currentSchemaVersion
				hash := sha256.Sum256(state)
				document.ContentHash = hex.EncodeToString(hash[:])
			}
		}
	}
	return &DocumentResponse{
		SchemaVersion: schemaVersion,
		Revision:      document.Revision,
		UpdatedAt:     updatedAt.Format(time.RFC3339Nano),
		ContentHash:   document.ContentHash,
		State:         state,
	}
}
