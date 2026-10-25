package road

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"senspace/domain"
	road_domain "senspace/domain/planet/road"
	planet_surface "senspace/domain/planet/surface"
	"senspace/domain/sys"
	"senspace/pkg/app/security"
	"senspace/pkg/bizerr"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// 当前支持的道路状态结构版本。
	currentSchemaVersion = 3
	// 与前端一致的旧道路统一宽度，门接口保留保存值。
	defaultRoadWidth = 2.6
	// 单个道路状态允许的最大字节数。
	maxStateBytes = 2 * 1024 * 1024
	// 单个星球允许的道路节点上限。
	maxRoadNodes = 2000
	// 单个星球允许的道路段上限。
	maxRoadEdges = 2000
	// 道路标识和外部引用的最大长度。
	maxRoadIdLength = 128
	// 坐标和切线分量的绝对值上限。
	maxRoadComponent = 1_000_000
	// 与前端编辑器一致的道路最大纵向坡度比例。
	maxRoadGrade = 100
)

var emptyState = json.RawMessage(`{"nodes":[],"edges":[]}`)

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

// roadAnchor 保存节点相对活动面、连接口或稳定标架的位置。
type roadAnchor struct {
	Kind         string    `json:"kind"`
	SurfaceId    string    `json:"surfaceId,omitempty"`
	RegionId     string    `json:"regionId,omitempty"`
	SurfacePoint []float64 `json:"surfacePoint,omitempty"`
	NormalOffset float64   `json:"normalOffset,omitempty"`
	ConnectorId  string    `json:"connectorId,omitempty"`
	Side         string    `json:"side,omitempty"`
	FrameId      string    `json:"frameId,omitempty"`
	LocalPoint   []float64 `json:"localPoint,omitempty"`
}

// roadNode 保存道路拓扑节点和曲线切线。
type roadNode struct {
	Id          string        `json:"id"`
	Anchor      roadAnchor    `json:"anchor"`
	TangentMode string        `json:"tangentMode"`
	TangentIn   []float64     `json:"tangentIn,omitempty"`
	TangentOut  []float64     `json:"tangentOut,omitempty"`
	Width       float64       `json:"width"`
	Junction    *roadJunction `json:"junction,omitempty"`
}

// roadJunction 保存分叉节点的主路关系。
type roadJunction struct {
	Type           string   `json:"type"`
	PrimaryEdgeIds []string `json:"primaryEdgeIds"`
	CornerRadius   float64  `json:"cornerRadius"`
	BranchSide     string   `json:"branchSide,omitempty"`
}

// roadFence 保存道路两侧共用的围栏样式。
type roadFence struct {
	ModelId    string `json:"modelId"`
	MaterialId string `json:"materialId"`
}

// roadEdge 保存两个节点之间的道路与通行参数。
type roadEdge struct {
	Id              string     `json:"id"`
	FromNodeId      string     `json:"fromNodeId"`
	ToNodeId        string     `json:"toNodeId"`
	CorridorId      string     `json:"corridorId,omitempty"`
	TangentFrom     []float64  `json:"tangentFrom,omitempty"`
	TangentTo       []float64  `json:"tangentTo,omitempty"`
	StyleId         string     `json:"styleId"`
	SurfaceMode     string     `json:"surfaceMode"`
	ShoulderWidth   float64    `json:"shoulderWidth"`
	MaxGrade        float64    `json:"maxGrade"`
	ElevationOffset float64    `json:"elevationOffset"`
	Direction       string     `json:"direction"`
	SpeedLimit      float64    `json:"speedLimit"`
	RouteModes      []string   `json:"routeModes"`
	Fence           *roadFence `json:"fence,omitempty"`
}

// roadState 是道路文档的完整业务状态。
type roadState struct {
	Nodes []roadNode `json:"nodes"`
	Edges []roadEdge `json:"edges"`
}

// GetPublished 公开读取一个星球当前发布的道路网络。
func GetPublished(planetId int) (*DocumentResponse, error) {
	if planetId <= 0 {
		return nil, bizerr.Parameter("planetId无效")
	}
	if domain.Db == nil {
		return nil, fmt.Errorf("planet road db not initialized")
	}
	var document road_domain.Document
	err := domain.Db.First(&document, "planet_id = ?", planetId).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return emptyDocumentResponse(), nil
	}
	if err != nil {
		return nil, err
	}
	return mapDocument(document), nil
}

// SavePublished 校验星球真实归属并以乐观锁保存道路网络。
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
		return nil, bizerr.Parameter("不支持的道路结构版本")
	}
	normalizedState, err := validateState(request.State)
	if err != nil {
		return nil, err
	}
	if domain.Db == nil {
		return nil, fmt.Errorf("planet road db not initialized")
	}

	var saved road_domain.Document
	err = domain.Db.Transaction(func(tx *gorm.DB) error {
		if err := validateOwner(tx, planetId, user.Id); err != nil {
			return err
		}
		var current road_domain.Document
		findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&current, "planet_id = ?", planetId).Error
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			if request.ExpectedRevision != 0 {
				return bizerr.Conflict("道路版本已更新，请重新加载")
			}
			saved = createDocument(planetId, request.SchemaVersion, normalizedState, user.Id)
			return tx.Create(&saved).Error
		}
		if findErr != nil {
			return findErr
		}
		if current.Revision != request.ExpectedRevision {
			return bizerr.Conflict("道路版本已更新，请重新加载")
		}
		applyState(&current, request.SchemaVersion, normalizedState, user.Id)
		if err := tx.Save(&current).Error; err != nil {
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

// RemovePlatformAnchorsTx 在地形删除事务中级联移除平台道路锚点。
func RemovePlatformAnchorsTx(
	tx *gorm.DB,
	planetId int,
	platformIds map[string]struct{},
	userId uint64,
) error {
	if len(platformIds) == 0 {
		return nil
	}
	var document road_domain.Document
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&document, "planet_id = ?", planetId).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	state, err := decodeState(json.RawMessage(document.StateJson))
	if err != nil {
		return fmt.Errorf("decode road state for terrain cascade: %w", err)
	}
	removedNodes := make(map[string]struct{})
	keptNodes := make([]roadNode, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		_, removed := platformIds[node.Anchor.RegionId]
		if node.Anchor.Kind == "surface" && removed {
			removedNodes[node.Id] = struct{}{}
			continue
		}
		keptNodes = append(keptNodes, node)
	}
	if len(removedNodes) == 0 {
		return nil
	}
	keptEdges := make([]roadEdge, 0, len(state.Edges))
	for _, edge := range state.Edges {
		_, fromRemoved := removedNodes[edge.FromNodeId]
		_, toRemoved := removedNodes[edge.ToNodeId]
		if !fromRemoved && !toRemoved {
			keptEdges = append(keptEdges, edge)
		}
	}
	state.Nodes = keptNodes
	state.Edges = keptEdges
	migrateRoadWidths(state, document.SchemaVersion)
	normalized, err := json.Marshal(state)
	if err != nil {
		return err
	}
	applyState(&document, currentSchemaVersion, normalized, userId)
	return tx.Save(&document).Error
}

// validateOwner 使用数据库中的真实星球归属验证发布权限。
func validateOwner(tx *gorm.DB, planetId int, userId uint64) error {
	var databaseUser sys.User
	if err := tx.Select("id", "planet_id").First(&databaseUser, userId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return bizerr.Unauthorized()
		}
		return err
	}
	if databaseUser.PlanetId != planetId {
		return bizerr.Forbidden("只有星球主人可以保存道路")
	}
	return nil
}

// validateState 严格校验并压缩道路状态。
func validateState(raw json.RawMessage) (json.RawMessage, error) {
	state, err := decodeState(raw)
	if err != nil {
		return nil, bizerr.Parameter("道路状态格式无效")
	}
	if len(state.Nodes) > maxRoadNodes || len(state.Edges) > maxRoadEdges {
		return nil, bizerr.Parameter("道路记录超过数量上限")
	}
	if err := validateRoadState(state); err != nil {
		return nil, err
	}
	state.Nodes = append([]roadNode{}, state.Nodes...)
	state.Edges = append([]roadEdge{}, state.Edges...)
	normalized, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	if len(normalized) > maxStateBytes {
		return nil, bizerr.Parameter("道路状态超过2MiB")
	}
	return normalized, nil
}

// decodeState 拒绝未知字段、空载荷和尾随 JSON。
func decodeState(raw json.RawMessage) (*roadState, error) {
	if len(raw) == 0 || len(raw) > maxStateBytes {
		return nil, fmt.Errorf("invalid road state size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var state roadState
	if err := decoder.Decode(&state); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("road state contains trailing json")
	}
	if state.Nodes == nil || state.Edges == nil {
		return nil, fmt.Errorf("road state must contain nodes and edges")
	}
	return &state, nil
}

// validateRoadState 校验节点、边和引用完整性。
func validateRoadState(state *roadState) error {
	nodeIds := make(map[string]struct{}, len(state.Nodes))
	for index := range state.Nodes {
		node := &state.Nodes[index]
		if err := validateUniqueId(node.Id, nodeIds, "道路节点"); err != nil {
			return err
		}
		if err := validateAnchor(node.Anchor); err != nil {
			return err
		}
		if node.TangentMode != "auto" && node.TangentMode != "linear" && node.TangentMode != "manual" {
			return bizerr.Parameter("道路节点切线模式无效")
		}
		if !validOptionalVector(node.TangentIn) || !validOptionalVector(node.TangentOut) {
			return bizerr.Parameter("道路节点切线无效")
		}
		if !finiteInRange(node.Width, 0.001, 100) {
			return bizerr.Parameter("道路宽度无效")
		}
	}

	edgeIds := make(map[string]struct{}, len(state.Edges))
	edgePairs := make(map[string]struct{}, len(state.Edges))
	for index := range state.Edges {
		edge := &state.Edges[index]
		if err := validateUniqueId(edge.Id, edgeIds, "道路段"); err != nil {
			return err
		}
		if edge.CorridorId == "" {
			edge.CorridorId = edge.Id
		}
		if !validReference(edge.CorridorId) {
			return bizerr.Parameter("道路走廊标识无效")
		}
		if !validOptionalVector(edge.TangentFrom) || !validOptionalVector(edge.TangentTo) {
			return bizerr.Parameter("道路段局部切线无效")
		}
		if edge.FromNodeId == edge.ToNodeId {
			return bizerr.Parameter("道路段不能连接同一节点")
		}
		if _, ok := nodeIds[edge.FromNodeId]; !ok {
			return bizerr.Parameter("道路段起点不存在")
		}
		if _, ok := nodeIds[edge.ToNodeId]; !ok {
			return bizerr.Parameter("道路段终点不存在")
		}
		pair := edge.FromNodeId + "\x00" + edge.ToNodeId
		reversePair := edge.ToNodeId + "\x00" + edge.FromNodeId
		if _, exists := edgePairs[pair]; exists {
			return bizerr.Parameter("存在重复道路段")
		}
		if _, exists := edgePairs[reversePair]; exists {
			return bizerr.Parameter("存在重复道路段")
		}
		edgePairs[pair] = struct{}{}
		if !planet_surface.IsMaterialID(edge.StyleId) {
			return bizerr.Parameter("道路样式无效")
		}
		if edge.SurfaceMode != "auto" && edge.SurfaceMode != "conform" &&
			edge.SurfaceMode != "terrain-blend" && edge.SurfaceMode != "bridge" {
			return bizerr.Parameter("道路贴合方式无效")
		}
		if !finiteInRange(edge.ShoulderWidth, 0, 100) || !finiteInRange(edge.MaxGrade, 0, maxRoadGrade) {
			return bizerr.Parameter("道路坡度或路肩无效")
		}
		if !finiteInRange(edge.ElevationOffset, -1000, 1000) {
			return bizerr.Parameter("道路起伏偏移无效")
		}
		if edge.Direction != "both" && edge.Direction != "forward" && edge.Direction != "reverse" {
			return bizerr.Parameter("道路方向无效")
		}
		if !finiteInRange(edge.SpeedLimit, 0, 1000) {
			return bizerr.Parameter("道路限速无效")
		}
		if err := validateRouteModes(edge.RouteModes); err != nil {
			return err
		}
		if err := validateFence(edge.Fence); err != nil {
			return err
		}
	}
	for index := range state.Nodes {
		node := &state.Nodes[index]
		if node.Junction == nil {
			continue
		}
		junction := node.Junction
		if junction.Type != "t-junction" && junction.Type != "crossing" && junction.Type != "roundabout" {
			return bizerr.Parameter("道路路口类型无效")
		}
		if junction.BranchSide != "" && junction.BranchSide != "left" && junction.BranchSide != "right" {
			return bizerr.Parameter("道路支路方向无效")
		}
		if len(junction.PrimaryEdgeIds) != 2 || junction.PrimaryEdgeIds[0] == junction.PrimaryEdgeIds[1] ||
			!finiteInRange(junction.CornerRadius, 0.1, 50) {
			return bizerr.Parameter("道路路口主路关系无效")
		}
		for _, edgeId := range junction.PrimaryEdgeIds {
			if _, exists := edgeIds[edgeId]; !exists {
				return bizerr.Parameter("道路路口引用不存在")
			}
			connected := false
			for edgeIndex := range state.Edges {
				edge := &state.Edges[edgeIndex]
				if edge.Id == edgeId && (edge.FromNodeId == node.Id || edge.ToNodeId == node.Id) {
					connected = true
					break
				}
			}
			if !connected {
				return bizerr.Parameter("道路路口引用未连接当前节点")
			}
		}
	}
	return nil
}

// validateFence 校验可选围栏模型和纹理白名单。
func validateFence(fence *roadFence) error {
	if fence == nil {
		return nil
	}
	if _, exists := fenceModelIds[fence.ModelId]; !exists {
		return bizerr.Parameter("道路围栏模型无效")
	}
	if _, exists := fenceMaterialIds[fence.MaterialId]; !exists {
		return bizerr.Parameter("道路围栏纹理无效")
	}
	return nil
}

// validateAnchor 按锚点类别检查必需字段并拒绝混合语义。
func validateAnchor(anchor roadAnchor) error {
	switch anchor.Kind {
	case "surface":
		if !validReference(anchor.SurfaceId) || !validVector(anchor.SurfacePoint) ||
			!finiteInRange(anchor.NormalOffset, -1000, 1000) ||
			anchor.ConnectorId != "" || anchor.Side != "" ||
			anchor.FrameId != "" || len(anchor.LocalPoint) != 0 {
			return bizerr.Parameter("道路表面锚点无效")
		}
		if anchor.RegionId != "" && !validReference(anchor.RegionId) {
			return bizerr.Parameter("道路表面分区无效")
		}
	case "connector":
		if !validReference(anchor.ConnectorId) ||
			(anchor.Side != "" && anchor.Side != "inside" && anchor.Side != "outside") ||
			anchor.SurfaceId != "" || anchor.RegionId != "" || len(anchor.SurfacePoint) != 0 ||
			anchor.FrameId != "" || len(anchor.LocalPoint) != 0 {
			return bizerr.Parameter("道路连接口锚点无效")
		}
	case "frame":
		if !validReference(anchor.FrameId) || !validVector(anchor.LocalPoint) ||
			anchor.SurfaceId != "" || anchor.RegionId != "" || len(anchor.SurfacePoint) != 0 ||
			anchor.ConnectorId != "" || anchor.Side != "" {
			return bizerr.Parameter("道路标架锚点无效")
		}
	default:
		return bizerr.Parameter("道路锚点类别无效")
	}
	return nil
}

// validateRouteModes 确保通行模式非空、有效且不重复。
func validateRouteModes(modes []string) error {
	if len(modes) == 0 || len(modes) > 2 {
		return bizerr.Parameter("道路通行模式无效")
	}
	used := make(map[string]struct{}, len(modes))
	for _, mode := range modes {
		if mode != "ground" && mode != "air" {
			return bizerr.Parameter("道路通行模式无效")
		}
		if _, exists := used[mode]; exists {
			return bizerr.Parameter("道路通行模式重复")
		}
		used[mode] = struct{}{}
	}
	return nil
}

// validateUniqueId 检查非空、安全长度和集合内唯一性。
func validateUniqueId(id string, used map[string]struct{}, label string) error {
	if !validReference(id) {
		return bizerr.Parameter(label + "ID无效")
	}
	if _, exists := used[id]; exists {
		return bizerr.Parameter(label + "ID重复")
	}
	used[id] = struct{}{}
	return nil
}

// validReference 检查外部引用长度和空白字符。
func validReference(value string) bool {
	if value == "" || len(value) > maxRoadIdLength || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

// validVector 检查固定三维有限向量。
func validVector(values []float64) bool {
	if len(values) != 3 {
		return false
	}
	for _, value := range values {
		if !finiteInRange(value, -maxRoadComponent, maxRoadComponent) {
			return false
		}
	}
	return true
}

// validOptionalVector 允许切线省略，但提供时必须是合法三维向量。
func validOptionalVector(values []float64) bool {
	return len(values) == 0 || validVector(values)
}

// finiteInRange 检查有限数值及闭区间范围。
func finiteInRange(value float64, minimum float64, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minimum && value <= maximum
}

// createDocument 创建第一版道路文档。
func createDocument(planetId int, schemaVersion int, state json.RawMessage, userId uint64) road_domain.Document {
	now := time.Now().UTC()
	hash := sha256.Sum256(state)
	return road_domain.Document{
		PlanetId:      planetId,
		SchemaVersion: schemaVersion,
		Revision:      1,
		StateJson:     string(state),
		ContentHash:   hex.EncodeToString(hash[:]),
		CreatInfo: domain.CreatInfo{
			CreatedAt: now,
			CreatedBy: userId,
		},
		UpdateInfo: domain.UpdateInfo{
			UpdatedAt: now,
			UpdatedBy: userId,
		},
	}
}

// applyState 更新道路文档版本、摘要和审计信息。
func applyState(document *road_domain.Document, schemaVersion int, state json.RawMessage, userId uint64) {
	hash := sha256.Sum256(state)
	document.SchemaVersion = schemaVersion
	document.Revision++
	document.StateJson = string(state)
	document.ContentHash = hex.EncodeToString(hash[:])
	document.UpdatedAt = time.Now().UTC()
	document.UpdatedBy = userId
}

// emptyDocumentResponse 返回未发布星球的稳定空状态。
func emptyDocumentResponse() *DocumentResponse {
	hash := sha256.Sum256(emptyState)
	return &DocumentResponse{
		SchemaVersion: currentSchemaVersion,
		Revision:      0,
		UpdatedAt:     time.Unix(0, 0).UTC().Format(time.RFC3339Nano),
		ContentHash:   hex.EncodeToString(hash[:]),
		State:         append(json.RawMessage(nil), emptyState...),
	}
}

// migrateRoadWidths 只在旧版本升级时统一非接口节点，之后保留用户编辑。
func migrateRoadWidths(state *roadState, schemaVersion int) {
	if schemaVersion >= 3 {
		return
	}
	for index := range state.Nodes {
		if state.Nodes[index].Anchor.Kind != "connector" {
			state.Nodes[index].Width = defaultRoadWidth
		}
	}
}

// mapDocument 把数据库记录映射为公开响应。
func mapDocument(document road_domain.Document) *DocumentResponse {
	state := json.RawMessage(document.StateJson)
	schemaVersion := document.SchemaVersion
	contentHash := document.ContentHash
	if schemaVersion < currentSchemaVersion {
		var legacy roadState
		if json.Unmarshal(state, &legacy) == nil {
			migrateRoadWidths(&legacy, schemaVersion)
			if migrated, err := json.Marshal(legacy); err == nil {
				state = migrated
				schemaVersion = currentSchemaVersion
				hash := sha256.Sum256(state)
				contentHash = hex.EncodeToString(hash[:])
			}
		}
	}
	return &DocumentResponse{
		SchemaVersion: schemaVersion,
		Revision:      document.Revision,
		UpdatedAt:     document.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ContentHash:   contentHash,
		State:         state,
	}
}
