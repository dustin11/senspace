package terrain

import (
	"encoding/json"
	"strings"
	"testing"
)

// 棱脊扩展兼容平面挤出，并拒绝会产生翻面或交叠的参数组合。
func TestRidgedExtrusion(t *testing.T) {
	shape := &terrainShape{Version: 1, Parameters: map[string]float64{"depth": .1, "bevel": 0},
		Profile: [][]float64{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}},
		Style:   terrainShapeStyle{Color: "#ffffff", Emissive: "#000000", Opacity: 1, Roughness: .4}}
	if err := validateTerrainShape("shape-extrude", shape); err != nil {
		t.Fatal(err)
	}
	shape.Parameters["ridge"] = .2
	shape.Parameters["mirror"] = 1
	if err := validateTerrainShape("shape-extrude", shape); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(shape)
	if err != nil {
		t.Fatal(err)
	}
	var restored terrainShape
	if err = json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Parameters["ridge"] != .2 {
		t.Fatal("棱脊参数丢失")
	}
	if restored.Parameters["mirror"] != 1 {
		t.Fatal("挤出镜像参数丢失")
	}
	shape.Parameters["bevel"] = .02
	if validateTerrainShape("shape-extrude", shape) == nil {
		t.Fatal("倒角与棱脊不能同时使用")
	}
	shape.Parameters["bevel"] = 0
	shape.Holes = [][][]float64{{{-.1, -.1}, {.1, -.1}, {0, .1}}}
	if validateTerrainShape("shape-extrude", shape) == nil {
		t.Fatal("棱脊不能带孔")
	}
	shape.Holes = nil
	shape.Profile = [][]float64{{0, 0}, {1, 0}, {.3, .3}, {0, 1}}
	if validateTerrainShape("shape-extrude", shape) == nil {
		t.Fatal("棱脊不能使用凹轮廓")
	}
}

// 球体细分参数可选，默认形状及显式低细分都可保存。
func TestRoundShapeSides(t *testing.T) {
	for _, id := range []string{"shape-dome", "shape-capsule", "shape-lathe"} {
		p := map[string]float64{"radius": .2, "height": .6}
		if id == "shape-dome" {
			p = map[string]float64{"radius": .2, "cut": 180, "wall": 0}
		}
		if id == "shape-lathe" {
			p = map[string]float64{"arc": 360}
		}
		shape := &terrainShape{Version: 1, Parameters: p,
			Style: terrainShapeStyle{Color: "#ffffff", Emissive: "#000000", Opacity: 1}}
		if id == "shape-lathe" {
			shape.Profile = [][]float64{{0, -.5}, {.4, 0}, {0, .5}}
		}
		if err := validateTerrainShape(id, shape); err != nil {
			t.Fatal(err)
		}
		if p["sides"] != 32 {
			t.Fatal("细分默认值不一致")
		}
		p["sides"] = 11
		if err := validateTerrainShape(id, shape); err != nil {
			t.Fatal(err)
		}
		for _, invalid := range []float64{5, 11.5, 65} {
			p["sides"] = invalid
			if validateTerrainShape(id, shape) == nil {
				t.Fatal("非法细分通过")
			}
		}
	}
}

// 新形状和旧形状混合发布后，服务端重编码不得丢弃源参数与机构。
func TestShapeStateRoundTrip(t *testing.T) {
	source := `{"platforms":[],"objects":[{"id":"shape-1","kind":"object","presetId":"shape-arc","variantSeed":0,"transform":{"position":[0,6,0],"rotation":[0,0,0],"scale":[1,1,1]},"shape":{"version":1,"name":"U形管","lod":{"important":true,"fixed":0},"parameters":{"radius":0.6,"tube":0.1,"wall":0.025,"start":0,"arc":180,"left":0.6,"right":0.6},"style":{"color":"#223344","roughness":0.5,"metalness":0.2,"opacity":1,"emissive":"#000000"},"rig":{"id":"joint-a","kind":"hinge","pivot":[0,0,0],"axis":[0,1,0],"min":-90,"max":90,"value":0,"keys":[{"time":0,"value":0},{"time":2,"value":60}]},"modifiers":[{"kind":"taper","amount":0.1}]}},{"id":"old-box","kind":"object","presetId":"box","variantSeed":0,"transform":{"position":[2,6,0],"rotation":[0,0,0],"scale":[1,1,1]}}]}`
	result, err := validateState(json.RawMessage(source))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"lod":{"important":true,"fixed":0}`, `"shape"`, `"wall":0.025`, `"rig"`, `"keys"`, `"modifiers"`, `"presetId":"box"`} {
		if !strings.Contains(string(result), field) {
			t.Fatalf("源字段丢失: %s", field)
		}
	}
	for _, mutation := range []string{
		strings.Replace(source, `"fixed":0`, `"fixed":3`, 1),
		strings.Replace(source, `"fixed":0`, `"fixed":1.5`, 1),
		strings.Replace(source, `"wall":0.025`, `"wall":0.2`, 1),
		strings.Replace(source, `"arc":180`, `"arc":0`, 1),
		strings.Replace(source, `"amount":0.1`, `"amount":-1`, 1),
		strings.Replace(source, `"axis":[0,1,0]`, `"axis":[0,0,0]`, 1),
		strings.Replace(source, `"time":2`, `"time":0`, 1),
	} {
		if _, err := validateState(json.RawMessage(mutation)); err == nil {
			t.Fatal("非法形状通过了发布校验")
		}
	}
}

// 网格校验阻止越界索引；轮廓点数量和数值受预算限制。
func TestShapeMeshAndBudget(t *testing.T) {
	valid := &terrainShapeMesh{Positions: []float64{0, 0, 0, 1, 0, 0, 0, 1, 0}, Indices: []int{0, 1, 2}}
	if err := validateTerrainShapeMesh(valid); err != nil {
		t.Fatal(err)
	}
	valid.Indices[2] = 9
	if err := validateTerrainShapeMesh(valid); err == nil {
		t.Fatal("越界索引未被拒绝")
	}
	if validShapePoints([][]float64{{0, 0}, {1, 0}, {0, 1}}, 2, 3) != true {
		t.Fatal("有效轮廓被拒绝")
	}
	if validShapePoints(make([][]float64, 129), 2, 3) {
		t.Fatal("过多控制点未被拒绝")
	}
}

// 与编辑器一致地拒绝无法生成有效孔洞或路径的存档。
func TestShapeTopologyValidation(t *testing.T) {
	outer := [][]float64{{-2, -2}, {2, -2}, {2, 2}, {-2, 2}}
	hole := [][]float64{{-.5, -.5}, {.5, -.5}, {.5, .5}, {-.5, .5}}
	if err := validateShapePolygon(outer, [][][]float64{hole}); err != nil {
		t.Fatal(err)
	}
	invalid := [][][]float64{
		{{-1, -1}, {1, 1}, {-1, 1}, {1, -1}},
		{{0, 0}, {1, 0}, {2, 0}},
		{{0, 0}, {0, 0}, {1, 1}},
	}
	for _, ring := range invalid {
		if validateShapePolygon(ring, nil) == nil {
			t.Fatal("非法轮廓通过")
		}
	}
	if validateShapePolygon(hole, [][][]float64{outer}) == nil {
		t.Fatal("外置孔洞通过")
	}
	if validateShapePolygon(outer, [][][]float64{hole, hole}) == nil {
		t.Fatal("重叠孔洞通过")
	}
	for _, path := range [][][]float64{{{0, 0, 0}, {0, 0, 0}}, {{0, 0, 0}, {1, 0, 0}, {0, 0, 0}}} {
		if validateShapePath(path) == nil {
			t.Fatal("退化路径通过")
		}
	}
	if err := validateShapePath([][]float64{{0, 0, 0}, {1, 0, 0}, {1, 1, 0}}); err != nil {
		t.Fatal(err)
	}
}

// 放样体保存后保留每层变宽、变深和偏移，并执行与前端一致的预算校验。
func TestLoftSections(t *testing.T) {
	shape := &terrainShape{Version: 1,
		Parameters: map[string]float64{"sides": 16, "roundness": 2, "arc": 180, "start": -90, "smooth": 0, "mirror": 1},
		Sections: []terrainShapeSection{
			{Y: -1, Width: 0, Depth: 0},
			{Y: 0, Width: .5, Depth: .3, OffsetX: .1, OffsetZ: .2},
			{Y: 1, Width: .2, Depth: .1},
		},
		Style: terrainShapeStyle{Color: "#ffffff", Emissive: "#000000", Opacity: 1},
	}
	if err := validateTerrainShape("shape-loft", shape); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(shape)
	if err != nil {
		t.Fatal(err)
	}
	source := `{"platforms":[],"objects":[{"id":"loft","kind":"object","presetId":"shape-loft","variantSeed":0,"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]},"shape":` + string(encoded) + `}]}`
	result, err := validateState(json.RawMessage(source))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"offsetX":0.1,"offsetZ":0.2`) {
		t.Fatal("放样截面在发布时丢失")
	}
	if !strings.Contains(string(result), `"mirror":1`) {
		t.Fatal("放样镜像参数在保存时丢失")
	}
	for _, mutation := range []string{
		strings.Replace(string(encoded), `"y":0`, `"y":-1`, 1),
		strings.Replace(string(encoded), `"width":0.5`, `"width":0`, 1),
		strings.Replace(string(encoded), `"sides":16`, `"sides":33`, 1),
		strings.Replace(string(encoded), `"smooth":0`, `"smooth":2`, 1),
		strings.Replace(string(encoded), `"mirror":1`, `"mirror":0.5`, 1),
		strings.Replace(string(encoded), `"mirror":1`, `"mirror":2`, 1),
	} {
		var invalid terrainShape
		if err := json.Unmarshal([]byte(mutation), &invalid); err != nil {
			t.Fatal(err)
		}
		if validateTerrainShape("shape-loft", &invalid) == nil {
			t.Fatal("非法放样截面通过校验")
		}
	}
	if validateShapeSections(make([]terrainShapeSection, 33)) == nil {
		t.Fatal("超过32层通过校验")
	}
	if validateShapeSections([]terrainShapeSection{{Y: -1}, {Y: 1}}) == nil {
		t.Fatal("无体积放样体通过校验")
	}
}

// 菱形局部弧必须跨角点，单条直边不能组成有效体积。
func TestLoftDiamondArc(t *testing.T) {
	shape := &terrainShape{Version: 1,
		Parameters: map[string]float64{"sides": 8, "roundness": 1, "arc": 70, "start": 10, "smooth": 1},
		Sections:   []terrainShapeSection{{Y: -1, Width: .5, Depth: .3}, {Y: 1, Width: .5, Depth: .3}},
		Style:      terrainShapeStyle{Color: "#ffffff", Emissive: "#000000", Opacity: 1},
	}
	if validateTerrainShape("shape-loft", shape) == nil {
		t.Fatal("菱形单边弧段不能通过校验")
	}
	shape.Parameters["start"] = -10
	if err := validateTerrainShape("shape-loft", shape); err != nil {
		t.Fatal(err)
	}
}
