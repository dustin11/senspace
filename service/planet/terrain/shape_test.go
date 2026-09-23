package terrain

import (
	"encoding/json"
	"strings"
	"testing"
)

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
