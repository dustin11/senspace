package terrain

import (
	"encoding/base64"
	"math"
	"regexp"
	"senspace/pkg/bizerr"
	"strings"
)

// 参数化形状的持久化描述，派生几何不作为运行时权威状态。
type terrainShapeModifier struct {
	Kind   string  `json:"kind"`
	Amount float64 `json:"amount"`
}

// 参数化形状记录。
type terrainShapeRig struct {
	ID     string                 `json:"id"`
	Parent string                 `json:"parent,omitempty"`
	Kind   string                 `json:"kind"`
	Pivot  []float64              `json:"pivot"`
	Axis   []float64              `json:"axis"`
	Min    float64                `json:"min"`
	Max    float64                `json:"max"`
	Value  float64                `json:"value"`
	Keys   []terrainShapeKeyframe `json:"keys"`
}
type terrainShapeKeyframe struct {
	Time  float64 `json:"time"`
	Value float64 `json:"value"`
}
type terrainShapeOperand struct {
	PresetID  string           `json:"presetId"`
	Shape     *terrainShape    `json:"shape"`
	Transform terrainTransform `json:"transform"`
}
type terrainShapeRecipe struct {
	Operation string                `json:"operation"`
	Operands  []terrainShapeOperand `json:"operands"`
}
type terrainShapeImport struct {
	Name   string `json:"name"`
	Base64 string `json:"base64"`
}

// 放样截面，宽度和深度为半尺寸，中心可沿 XZ 偏移。
type terrainShapeSection struct {
	Y       float64 `json:"y"`       // 截面高度。
	Width   float64 `json:"width"`   // 截面半宽。
	Depth   float64 `json:"depth"`   // 截面半深。
	OffsetX float64 `json:"offsetX"` // 中心横向偏移。
	OffsetZ float64 `json:"offsetZ"` // 中心纵深偏移。
}

// 形状源定义与有界高级造型描述。
type terrainShape struct {
	RoomID       string                 `json:"roomId,omitempty"`
	Rooms        *terrainRoomLayout     `json:"rooms,omitempty"`
	LOD          *terrainShapeLOD       `json:"lod,omitempty"`
	Rig          *terrainShapeRig       `json:"rig,omitempty"`
	Recipe       *terrainShapeRecipe    `json:"recipe,omitempty"`
	ImportSource *terrainShapeImport    `json:"importSource,omitempty"`
	Modifiers    []terrainShapeModifier `json:"modifiers,omitempty"`
	Version      int                    `json:"version"`
	Name         string                 `json:"name,omitempty"`
	Parameters   map[string]float64     `json:"parameters"`
	Profile      [][]float64            `json:"profile,omitempty"`
	Holes        [][][]float64          `json:"holes,omitempty"`
	Path         [][]float64            `json:"path,omitempty"`
	Sections     []terrainShapeSection  `json:"sections,omitempty"`
	Style        terrainShapeStyle      `json:"style"`
	Decorative   bool                   `json:"decorative,omitempty"`
	Mesh         *terrainShapeMesh      `json:"mesh,omitempty"`
}

// 用户确认的细节约束；重要部件优先于固定档位，开口始终保留。
type terrainShapeLOD struct {
	Important bool `json:"important,omitempty"`
	Fixed     *int `json:"fixed,omitempty"`
}

// 形状的基础 PBR 外观。
type terrainShapeStyle struct {
	Color     string  `json:"color"`
	Roughness float64 `json:"roughness"`
	Metalness float64 `json:"metalness"`
	Opacity   float64 `json:"opacity"`
	Emissive  string  `json:"emissive"`
}

// 有界静态网格，禁止不受控顶点和非法索引。
type terrainShapeMesh struct {
	Positions []float64 `json:"positions"`
	Normals   []float64 `json:"normals,omitempty"`
	UVs       []float64 `json:"uvs,omitempty"`
	Indices   []int     `json:"indices"`
}

var shapeColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// 参数范围与前端生成器一致；未列出的字段拒绝进入存档。
var shapeParameterRanges = map[string][2]float64{
	"ridge": {0, 10}, "roundness": {1, 4}, "mirror": {0, 1},
	"width": {.005, 100}, "height": {.005, 100}, "depth": {.005, 100}, "radius": {.005, 100},
	"topWidth": {.005, 100}, "topDepth": {.005, 100}, "topRadius": {0, 50}, "offset": {-20, 20},
	"bevel": {0, 10}, "smooth": {1, 5}, "top": {0, 1}, "sides": {3, 64}, "wall": {0, 10},
	"square": {0, 1}, "tube": {.005, 100}, "start": {-360, 360}, "arc": {1, 360},
	"left": {0, 100}, "right": {0, 100}, "cut": {5, 180}, "aspect": {.005, 100},
	"eave": {0, 4}, "corner": {0, 4}, "thickness": {.005, 100}, "curve": {.3, 4},
}
var shapeParameterKeys = map[string]string{
	"shape-box": "width height depth bevel smooth", "shape-wedge": "width height depth top", "shape-corner": "width height depth",
	"shape-frustum": "width height depth topWidth topDepth offset", "shape-prism": "radius height topRadius sides",
	"shape-pipe": "radius height wall sides square", "shape-arc": "radius tube wall start arc left right",
	"shape-dome": "radius cut wall sides", "shape-capsule": "radius height sides", "shape-extrude": "depth bevel ridge mirror",
	"shape-loft":  "sides roundness arc start smooth mirror",
	"shape-sweep": "radius wall square aspect", "shape-lathe": "arc start aspect sides", "shape-roof": "width depth height eave corner thickness curve", "shape-mesh": "",
}

// 检查固定维度控制点，不允许 NaN、越界和无限细分。
func validShapePoints(points [][]float64, dimensions, minimum int) bool {
	if len(points) < minimum || len(points) > 128 {
		return false
	}
	for _, point := range points {
		if len(point) != dimensions {
			return false
		}
		for _, v := range point {
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 100 {
				return false
			}
		}
	}
	return true
}

// 同时校验物件和预制体形状，避免服务端重编码丢失源参数。
func validateTerrainShape(id string, shape *terrainShape) error {
	return validateTerrainShapeDepth(id, shape, 0)
}
func validateTerrainShapeDepth(id string, shape *terrainShape, depth int) error {
	if depth > 3 {
		return bizerr.Parameter("布尔源嵌套超限")
	}

	keys, isShape := shapeParameterKeys[id]
	if !isShape {
		if shape != nil {
			return bizerr.Parameter("旧预设不接受参数化形状")
		}
		return nil
	}
	if shape == nil || shape.Version != 1 || len([]rune(shape.Name)) > 80 {
		return bizerr.Parameter("形状描述或版本无效")
	}
	if shape.RoomID != "" && !roomIDPattern.MatchString(shape.RoomID) {
		return bizerr.Parameter("所属房间名称无效")
	}
	if err := validateTerrainRooms(shape.Rooms); err != nil {
		return err
	}
	if shape.LOD != nil && shape.LOD.Fixed != nil && (*shape.LOD.Fixed < 0 || *shape.LOD.Fixed > 2) {
		return bizerr.Parameter("LOD 档位只能为 0、1、2")
	}
	if shape.Rig != nil {
		if err := validateShapeRig(shape.Rig); err != nil {
			return err
		}
	}
	if shape.Recipe != nil {
		recipe := shape.Recipe
		if id != "shape-mesh" || len(recipe.Operands) != 2 || (recipe.Operation != "union" && recipe.Operation != "subtract" && recipe.Operation != "intersect") {
			return bizerr.Parameter("布尔源无效")
		}
		for _, operand := range recipe.Operands {
			if !strings.HasPrefix(operand.PresetID, "shape-") || !validTerrainTransform(operand.Transform) {
				return bizerr.Parameter("布尔源变换无效")
			}
			if err := validateTerrainShapeDepth(operand.PresetID, operand.Shape, depth+1); err != nil {
				return err
			}
		}
	}
	if shape.ImportSource != nil {
		source := shape.ImportSource
		if id != "shape-mesh" || len(source.Name) > 512 || len(source.Base64) > 1398104 {
			return bizerr.Parameter("导入源超限")
		}
		bytes, err := base64.StdEncoding.DecodeString(source.Base64)
		if err != nil || len(bytes) < 12 || string(bytes[:4]) != "glTF" {
			return bizerr.Parameter("GLB源文件无效")
		}
	}
	// 新增可选参数与前端默认值一致，已有形状文档无需重写。
	if shape.Parameters != nil {
		if id == "shape-extrude" || id == "shape-loft" {
			if _, exists := shape.Parameters["mirror"]; !exists {
				shape.Parameters["mirror"] = 0
			}
		}
		if id == "shape-extrude" {
			if _, exists := shape.Parameters["ridge"]; !exists {
				shape.Parameters["ridge"] = 0
			}
		}
		if id == "shape-lathe" {
			if _, exists := shape.Parameters["start"]; !exists {
				shape.Parameters["start"] = 0
			}
			if _, exists := shape.Parameters["aspect"]; !exists {
				shape.Parameters["aspect"] = 1
			}
		}
		if id == "shape-dome" || id == "shape-capsule" || id == "shape-lathe" {
			if _, exists := shape.Parameters["sides"]; !exists {
				shape.Parameters["sides"] = 32
			}
			if shape.Parameters["sides"] < 6 {
				return bizerr.Parameter("环向细分至少为6")
			}
		}
	}
	allowed := strings.Fields(keys)
	if len(shape.Parameters) != len(allowed) {
		return bizerr.Parameter("形状参数不完整")
	}
	for _, key := range allowed {
		value, ok := shape.Parameters[key]
		bounds := shapeParameterRanges[key]
		if id == "shape-loft" {
			if key == "sides" {
				bounds = [2]float64{4, 32}
			}
			if key == "smooth" {
				bounds = [2]float64{0, 1}
			}
		}
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) || value < bounds[0] || value > bounds[1] || ((key == "sides" || key == "smooth" || key == "mirror") && math.Trunc(value) != value) {
			return bizerr.Parameter("形状参数超出范围: " + key)
		}
	}
	if len(shape.Modifiers) > 4 {
		return bizerr.Parameter("修改器数量超限")
	}
	for _, m := range shape.Modifiers {
		if math.IsNaN(m.Amount) || math.IsInf(m.Amount, 0) {
			return bizerr.Parameter("修改器数值无效")
		}
		switch m.Kind {
		case "bend", "twist":
			if math.Abs(m.Amount) > 180 {
				return bizerr.Parameter("修改器角度超限")
			}
		case "taper":
			if m.Amount < -0.9 || m.Amount > 3 {
				return bizerr.Parameter("锥化比例超限")
			}
		default:
			return bizerr.Parameter("未知修改器")
		}
	}
	p := shape.Parameters
	if (id == "shape-box" && p["bevel"] > math.Min(p["width"], math.Min(p["height"], p["depth"]))/2) ||
		(id == "shape-pipe" && (p["wall"] < .005 || p["wall"] >= p["radius"])) ||
		(id == "shape-arc" && (p["tube"] >= p["radius"] || p["wall"] >= p["tube"])) ||
		((id == "shape-dome" || id == "shape-sweep") && p["wall"] >= p["radius"]) ||
		(id == "shape-extrude" && p["bevel"] > 1) || (id == "shape-sweep" && p["wall"] > 5) {
		return bizerr.Parameter("形状壁厚、倒角或半径无效")
	}
	if (id == "shape-extrude" || id == "shape-lathe" || shape.Profile != nil) && !validShapePoints(shape.Profile, 2, 3) {
		return bizerr.Parameter("形状轮廓无效")
	}
	if (id == "shape-sweep" || shape.Path != nil) && !validShapePoints(shape.Path, 3, 2) {
		return bizerr.Parameter("形状路径无效")
	}
	if id == "shape-loft" && p["roundness"] == 1 &&
		(math.Floor(p["start"]/90)+1)*90 >= p["start"]+p["arc"] {
		return bizerr.Parameter("菱形截面弧段须跨过至少一个角点，不能退化为直线")
	}
	if id == "shape-loft" || shape.Sections != nil {
		if err := validateShapeSections(shape.Sections); err != nil {
			return err
		}
	}
	if len(shape.Holes) > 16 {
		return bizerr.Parameter("形状孔洞过多")
	}
	for _, hole := range shape.Holes {
		if !validShapePoints(hole, 2, 3) {
			return bizerr.Parameter("形状孔洞无效")
		}
	}
	if id == "shape-extrude" {
		if err := validateShapePolygon(shape.Profile, shape.Holes); err != nil {
			return err
		}
		if p["ridge"] > 0 {
			positive, negative := false, false
			for i, a := range shape.Profile {
				b, c := shape.Profile[(i+1)%len(shape.Profile)], shape.Profile[(i+2)%len(shape.Profile)]
				turn := (b[0]-a[0])*(c[1]-b[1]) - (b[1]-a[1])*(c[0]-b[0])
				positive = positive || turn > 1e-8
				negative = negative || turn < -1e-8
			}
			if p["bevel"] > 0 || len(shape.Holes) > 0 || (positive && negative) {
				return bizerr.Parameter("棱脊体需要无孔凸轮廓，且不能同时使用倒角")
			}
		}
	}
	if id == "shape-sweep" {
		if err := validateShapePath(shape.Path); err != nil {
			return err
		}
	}
	if id == "shape-lathe" {
		for _, point := range shape.Profile {
			if point[0] < 0 {
				return bizerr.Parameter("旋转体半径不能为负")
			}
		}
	}
	style := shape.Style
	if !shapeColorPattern.MatchString(style.Color) || !shapeColorPattern.MatchString(style.Emissive) {
		return bizerr.Parameter("形状颜色无效")
	}
	for _, v := range []float64{style.Roughness, style.Metalness, style.Opacity} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return bizerr.Parameter("形状材质无效")
		}
	}
	if id == "shape-mesh" {
		return validateTerrainShapeMesh(shape.Mesh)
	}
	if shape.Mesh != nil {
		return bizerr.Parameter("基础形状不能携带网格")
	}
	return nil
}

// 检查网格所有数组，防止发布后加载越界。
func validateTerrainShapeMesh(mesh *terrainShapeMesh) error {
	invalid := func() error { return bizerr.Parameter("静态网格数据无效或超出预算") }
	if mesh == nil || len(mesh.Positions) < 9 || len(mesh.Positions) > 60000 || len(mesh.Positions)%3 != 0 || len(mesh.Indices) < 3 || len(mesh.Indices) > 90000 || len(mesh.Indices)%3 != 0 {
		return invalid()
	}
	for _, v := range mesh.Positions {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1000 {
			return invalid()
		}
	}
	for _, v := range mesh.Indices {
		if v < 0 || v >= len(mesh.Positions)/3 {
			return invalid()
		}
	}
	if (mesh.Normals != nil && len(mesh.Normals) != len(mesh.Positions)) || (mesh.UVs != nil && len(mesh.UVs) != len(mesh.Positions)/3*2) {
		return invalid()
	}
	for _, values := range [][]float64{mesh.Normals, mesh.UVs} {
		for _, v := range values {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return invalid()
			}
		}
	}
	return nil
}

// 校验关节角度、轴、关键帧与行程，拒绝无界动画。
func validateShapeRig(rig *terrainShapeRig) error {
	invalid := func() error { return bizerr.Parameter("刚性机构描述无效") }
	if rig.ID == "" || len(rig.ID) > 128 || len(rig.Parent) > 128 || rig.Parent == rig.ID || len(rig.Axis) != 3 || len(rig.Pivot) != 3 {
		return invalid()
	}
	if rig.Kind != "fixed" && rig.Kind != "hinge" && rig.Kind != "slider" {
		return invalid()
	}
	length := 0.0
	for _, v := range rig.Axis {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 100 {
			return invalid()
		}
		length += v * v
	}
	for _, v := range rig.Pivot {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 100 {
			return invalid()
		}
	}
	if length < 1e-12 || rig.Min > rig.Max || rig.Value < rig.Min || rig.Value > rig.Max || len(rig.Keys) > 64 {
		return invalid()
	}
	limit := 180.0
	if rig.Kind == "slider" {
		limit = 100
	}
	if math.Abs(rig.Min) > limit || math.Abs(rig.Max) > limit {
		return invalid()
	}
	for i, key := range rig.Keys {
		if key.Time < 0 || key.Time > 120 || key.Value < rig.Min || key.Value > rig.Max || (i > 0 && key.Time <= rig.Keys[i-1].Time) {
			return invalid()
		}
	}
	return nil
}

// 截面有界且高度递增，仅首尾允许同时归零为尖端，防止翻面和无体积几何。
func validateShapeSections(sections []terrainShapeSection) error {
	if len(sections) < 2 || len(sections) > 32 {
		return bizerr.Parameter("放样截面需要2–32层")
	}
	hasVolume := false
	for i, section := range sections {
		for _, value := range []float64{section.Y, section.Width, section.Depth, section.OffsetX, section.OffsetZ} {
			if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 100 {
				return bizerr.Parameter("放样截面数值无效")
			}
		}
		if section.Width < 0 || section.Depth < 0 || (i > 0 && section.Y <= sections[i-1].Y) {
			return bizerr.Parameter("截面须按高度递增，半宽半深不能为负")
		}
		if (section.Width == 0 || section.Depth == 0) &&
			(section.Width != 0 || section.Depth != 0 || (i > 0 && i < len(sections)-1)) {
			return bizerr.Parameter("仅首尾截面可同时归零为尖端")
		}
		hasVolume = hasVolume || section.Width > 0
	}
	if !hasVolume {
		return bizerr.Parameter("放样体至少需要一个非零截面")
	}
	return nil
}
