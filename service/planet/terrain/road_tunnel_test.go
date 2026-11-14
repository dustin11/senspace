package terrain

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"senspace/pkg/bizerr"
)

// 平台文档经过发布校验和重新读取后，保留道路身份、宽度与坡度标架。
func TestRoadTunnelStateRoundTrip(t *testing.T) {
	const source = `{"platforms":[{"id":"p1","kind":"platform","materialId":"grass","transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[64,1,64]},"heightField":{"version":2,"enabled":false,"baseHeight":0.006,"cellSize":0.5,"samplesPerChunk":64,"heightUnit":0.005,"zeroCode":32768,"chunks":[]},"roadTunnels":[{"id":"road","sections":[{"point":[0,0.1,-10],"right":[-1,0,0],"up":[0,0.8,0.6],"halfWidth":1.2,"height":2.2},{"point":[0,0.1,10],"right":[-1,0,0],"up":[0,0.8,0.6],"halfWidth":2,"height":3}]}]}],"objects":[]}`
	normalized, err := validateState(json.RawMessage(source))
	if err != nil {
		t.Fatal(err)
	}
	var before, after terrainState
	if err := json.Unmarshal([]byte(source), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(normalized, &after); err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal(before.Platforms[0].RoadTunnels)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := json.Marshal(after.Platforms[0].RoadTunnels)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("道路洞保存后发生变化: %s", actual)
	}
	if _, err := validateState(normalized); err != nil {
		t.Fatalf("已发布道路洞无法重读: %v", err)
	}
	for _, key := range []string{"preview", "key", "unknown"} {
		invalid := strings.Replace(source, `"id":"road"`, `"id":"road","`+key+`":true`, 1)
		if _, err := validateState(json.RawMessage(invalid)); !bizerr.IsKind(err, bizerr.KindParameter) {
			t.Fatalf("应拒绝运行时或未知字段 %s，得到 %v", key, err)
		}
	}
	legacy := strings.Replace(source, `,"roadTunnels":`+string(expected), "", 1)
	if _, err := validateState(json.RawMessage(legacy)); err != nil {
		t.Fatalf("没有道路洞的旧地形不能受影响: %v", err)
	}
}

// 只允许有界且可重建的正式道路截面，不触及任何数据库或真实星球。
func TestRoadTunnelInvalidState(t *testing.T) {
	valid := func() terrainRoadTunnel {
		return terrainRoadTunnel{Id: "road", Sections: []terrainRoadTunnelSection{
			{Point: []float64{0, .1, -10}, Right: []float64{-1, 0, 0}, Up: []float64{0, 1, 0}, HalfWidth: 1.2, Height: 2.2},
			{Point: []float64{0, .1, 10}, Right: []float64{-1, 0, 0}, Up: []float64{0, 1, 0}, HalfWidth: 1.2, Height: 2.2},
		}}
	}
	for name, change := range map[string]func(*terrainRoadTunnel){
		"empty identity":     func(p *terrainRoadTunnel) { p.Id = "" },
		"space identity":     func(p *terrainRoadTunnel) { p.Id = " road" },
		"missing sections":   func(p *terrainRoadTunnel) { p.Sections = nil },
		"too many sections":  func(p *terrainRoadTunnel) { p.Sections = make([]terrainRoadTunnelSection, 4097) },
		"invalid vector":     func(p *terrainRoadTunnel) { p.Sections[0].Point = []float64{0, 0} },
		"nonfinite position": func(p *terrainRoadTunnel) { p.Sections[0].Point[0] = math.NaN() },
		"zero frame":         func(p *terrainRoadTunnel) { p.Sections[0].Up = []float64{0, 0, 0} },
		"scaled frame":       func(p *terrainRoadTunnel) { p.Sections[0].Right = []float64{2, 0, 0} },
		"negative width":     func(p *terrainRoadTunnel) { p.Sections[0].HalfWidth = -1 },
		"nonfinite height":   func(p *terrainRoadTunnel) { p.Sections[0].Height = math.Inf(1) },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := valid()
			change(&invalid)
			if err := validateTerrainRoadTunnels([]terrainRoadTunnel{invalid}); !bizerr.IsKind(err, bizerr.KindParameter) {
				t.Fatalf("应拒绝损坏道路洞，得到 %v", err)
			}
		})
	}
	if validateTerrainRoadTunnels([]terrainRoadTunnel{valid(), valid()}) == nil {
		t.Fatal("不能保存重复道路身份")
	}
	if validateTerrainRoadTunnels(make([]terrainRoadTunnel, 2001)) == nil {
		t.Fatal("不能保存超量道路洞")
	}
}
