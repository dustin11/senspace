package terrain

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"senspace/pkg/bizerr"
)

// 山体参数必须完整往返；非法高度、节点和重复身份不能发布。
func TestEditableMountainState(t *testing.T) {
	mountain := terrainMountain{Id: "mountain", Version: 1, Center: []float64{0, 0}, Height: 8, Radius: 8, Seed: 1867, Sediment: 1, Grass: 1, Gravel: .4, Erosion: 1,
		Branches: []terrainMountainBranch{{Id: "ridge", InnerSteepness: .8, Points: []terrainMountainNode{{Id: "root", X: .2, Z: 0, Height: .6, Width: .35}, {Id: "tip", X: 1, Z: .5, Height: .7, Width: .3}}}}}
	data, err := json.Marshal(mountain)
	if err != nil {
		t.Fatal(err)
	}
	source := `{"platforms":[{"id":"p1","kind":"platform","materialId":"grass","transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[64,1,64]},"heightField":{"version":2,"enabled":false,"baseHeight":0.006,"cellSize":0.5,"samplesPerChunk":64,"heightUnit":0.005,"zeroCode":32768,"chunks":[]},"mountains":[MOUNTAIN]}],"objects":[]}`
	normalized, err := validateState(json.RawMessage(strings.ReplaceAll(source, "MOUNTAIN", string(data))))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(normalized), string(data)) {
		t.Fatalf("山地参数丢失: %s", normalized)
	}
	for _, height := range []float64{math.NaN(), math.Inf(1), 21, -1} {
		invalid := mountain
		invalid.Height = height
		if validateTerrainMountains([]terrainMountain{invalid}) == nil {
			t.Fatalf("接受非法山高 %v", height)
		}
	}
	if validateTerrainMountains([]terrainMountain{mountain, mountain}) == nil {
		t.Fatal("接受重复山地身份")
	}
	mountain.Branches[0].Points[1].X = 4
	if validateTerrainMountains([]terrainMountain{mountain}) == nil {
		t.Fatal("接受越界山脊")
	}
}

// 岩台参数经严格解码与规范化后完整保留，并与山脊共用身份和容量校验。
func TestMountainLedgeState(t *testing.T) {
	const ledge = `{"id":"ledge","x":0.4,"z":-0.2,"height":0.5,"radius":0.2,"aspect":0.8,"rotation":0.7,"grass":0}`
	const source = `{"platforms":[{"id":"p1","kind":"platform","materialId":"grass","transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[64,1,64]},"heightField":{"version":2,"enabled":false,"baseHeight":0.006,"cellSize":0.5,"samplesPerChunk":64,"heightUnit":0.005,"zeroCode":32768,"chunks":[]},"mountains":[{"id":"mountain","version":1,"center":[0,0],"height":8,"radius":8,"rotation":0,"seed":1867,"branches":[{"id":"ridge","innerSteepness":0.8,"points":[{"id":"root","x":0.2,"z":0,"height":0.6,"width":0.35},{"id":"tip","x":1,"z":0.5,"height":0.7,"width":0.3}]}],"ledges":LEDGES,"sediment":1,"grass":1,"gravel":0.4,"erosion":1}]}],"objects":[]}`
	stateWithLedges := func(ledges string) json.RawMessage {
		return json.RawMessage(strings.Replace(source, "LEDGES", ledges, 1))
	}
	normalized, err := validateState(stateWithLedges("[" + ledge + "]"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(normalized), `"ledges":[`+ledge+`]`) {
		t.Fatalf("岩台参数丢失: %s", normalized)
	}
	if _, err := validateState(normalized); err != nil {
		t.Fatalf("无法重新读取已保存的岩台: %v", err)
	}

	ledges := make([]string, 13)
	for i := range ledges {
		ledges[i] = strings.Replace(ledge, `"id":"ledge"`, fmt.Sprintf(`"id":"ledge-%d"`, i), 1)
	}
	for _, value := range []string{
		`[]`,
		"[" + strings.Replace(ledge, `"grass":0`, `"grass":2`, 1) + "]",
		"[" + strings.Join(ledges[:12], ",") + "]",
		`[{"id":"low","x":-4,"z":-4,"height":0.01,"radius":0.04,"aspect":0.4,"rotation":` + fmt.Sprint(-math.Pi*8) + `},` +
			`{"id":"high","x":4,"z":4,"height":1.25,"radius":0.65,"aspect":1,"rotation":` + fmt.Sprint(math.Pi*8) + `}]`,
	} {
		if _, err := validateState(stateWithLedges(value)); err != nil {
			t.Fatalf("拒绝合法岩台: %v", err)
		}
	}

	for _, test := range []struct {
		name   string
		ledges string
	}{
		{"over capacity", "[" + strings.Join(ledges, ",") + "]"},
		{"duplicate identity", "[" + ledge + "," + ledge + "]"},
		{"branch identity conflict", "[" + strings.Replace(ledge, `"ledge"`, `"ridge"`, 1) + "]"},
		{"node identity conflict", "[" + strings.Replace(ledge, `"ledge"`, `"root"`, 1) + "]"},
		{"empty identity", "[" + strings.Replace(ledge, `"ledge"`, `""`, 1) + "]"},
		{"invalid collection", `{}`},
		{"invalid x", "[" + strings.Replace(ledge, `"x":0.4`, `"x":-4.01`, 1) + "]"},
		{"invalid z", "[" + strings.Replace(ledge, `"z":-0.2`, `"z":4.01`, 1) + "]"},
		{"height too low", "[" + strings.Replace(ledge, `"height":0.5`, `"height":0`, 1) + "]"},
		{"height too high", "[" + strings.Replace(ledge, `"height":0.5`, `"height":1.26`, 1) + "]"},
		{"radius too small", "[" + strings.Replace(ledge, `"radius":0.2`, `"radius":0.03`, 1) + "]"},
		{"radius too large", "[" + strings.Replace(ledge, `"radius":0.2`, `"radius":0.66`, 1) + "]"},
		{"aspect too small", "[" + strings.Replace(ledge, `"aspect":0.8`, `"aspect":0.39`, 1) + "]"},
		{"aspect too large", "[" + strings.Replace(ledge, `"aspect":0.8`, `"aspect":1.01`, 1) + "]"},
		{"invalid rotation", "[" + strings.Replace(ledge, `"rotation":0.7`, `"rotation":26`, 1) + "]"},
		{"grass too low", "[" + strings.Replace(ledge, `"grass":0`, `"grass":-0.1`, 1) + "]"},
		{"grass too high", "[" + strings.Replace(ledge, `"grass":0`, `"grass":2.1`, 1) + "]"},
		{"nonfinite height", "[" + strings.Replace(ledge, `"height":0.5`, `"height":1e999`, 1) + "]"},
		{"unknown field", "[" + strings.Replace(ledge, `"aspect":0.8`, `"aspect":0.8,"unknownField":1`, 1) + "]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validateState(stateWithLedges(test.ledges)); !bizerr.IsKind(err, bizerr.KindParameter) {
				t.Fatalf("期望参数错误，得到 %v", err)
			}
		})
	}
}

// 洞室与既有岩台一起保存，严格校验容量、有限参数和跨部位身份。
func TestMountainCaveState(t *testing.T) {
	cave := terrainMountainCave{Id: "cave", X: .4, Z: -.3, Height: .45, Radius: .18, Aspect: .8, Depth: .5, Rotation: .7, Pitch: .25, Texture: "mountain", FloorTexture: "loess"}
	mountain := terrainMountain{Id: "mountain", Version: 1, Center: []float64{0, 0}, Height: 8, Radius: 8, Seed: 1867,
		Branches: []terrainMountainBranch{}, Caves: []terrainMountainCave{cave},
		Ledges:   []terrainMountainLedge{{Id: "ledge", X: .4, Z: -.3, Height: .45, Radius: .25, Aspect: .8}},
		Sediment: 1, Grass: 1, Gravel: .4, Erosion: 1}
	data, err := json.Marshal(mountain)
	if err != nil {
		t.Fatal(err)
	}
	source := `{"platforms":[{"id":"p1","kind":"platform","materialId":"grass","transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[64,1,64]},"heightField":{"version":2,"enabled":false,"baseHeight":0.006,"cellSize":0.5,"samplesPerChunk":64,"heightUnit":0.005,"zeroCode":32768,"chunks":[]},"mountains":[MOUNTAIN]}],"objects":[]}`
	normalized, err := validateState(json.RawMessage(strings.Replace(source, "MOUNTAIN", string(data), 1)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(normalized), `"caves":[`) || !strings.Contains(string(normalized), `"ledges":[`) {
		t.Fatalf("洞室或原岩台参数丢失: %s", normalized)
	}
	if _, err := validateState(normalized); err != nil {
		t.Fatalf("山洞无法重新读取: %v", err)
	}
	if !strings.Contains(string(normalized), `"texture":"mountain"`) || !strings.Contains(string(normalized), `"floorTexture":"loess"`) {
		t.Fatalf("山洞纹理设置丢失: %s", normalized)
	}
	for _, texture := range []string{"plain", "limestone", "mountain", "loess", "grass", "wood", "steel", "marble"} {
		state := strings.Replace(string(normalized), `"texture":"mountain"`, `"texture":"`+texture+`"`, 1)
		state = strings.Replace(state, `"floorTexture":"loess"`, `"floorTexture":"`+texture+`"`, 1)
		result, err := validateState(json.RawMessage(state))
		if err != nil || !strings.Contains(string(result), `"texture":"`+texture+`"`) || !strings.Contains(string(result), `"floorTexture":"`+texture+`"`) {
			t.Fatalf("公共纹理 %q 无法在洞壁和洞底往返: %v", texture, err)
		}
	}
	for name, change := range map[string]func(*terrainMountainCave){
		"empty id":      func(c *terrainMountainCave) { c.Id = "" },
		"ledge id":      func(c *terrainMountainCave) { c.Id = "ledge" },
		"nonfinite":     func(c *terrainMountainCave) { c.Radius = math.NaN() },
		"small radius":  func(c *terrainMountainCave) { c.Radius = .03 },
		"large aspect":  func(c *terrainMountainCave) { c.Aspect = 1.6 },
		"depth":         func(c *terrainMountainCave) { c.Depth = 0 },
		"pitch":         func(c *terrainMountainCave) { c.Pitch = math.Pi },
		"position":      func(c *terrainMountainCave) { c.X = 4.1 },
		"wall texture":  func(c *terrainMountainCave) { c.Texture = "missing" },
		"floor texture": func(c *terrainMountainCave) { c.FloorTexture = "missing" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := cave
			change(&invalid)
			copy := mountain
			copy.Caves = []terrainMountainCave{invalid}
			if err := validateTerrainMountains([]terrainMountain{copy}); !bizerr.IsKind(err, bizerr.KindParameter) {
				t.Fatalf("应拒绝损坏洞室，得到 %v", err)
			}
		})
	}
	mountain.Caves = make([]terrainMountainCave, 9)
	for i := range mountain.Caves {
		mountain.Caves[i] = cave
		mountain.Caves[i].Id = fmt.Sprintf("cave-%d", i)
	}
	if validateTerrainMountains([]terrainMountain{mountain}) == nil {
		t.Fatal("接受超出容量的洞室")
	}
}

// 三维截面可折返、可独立改宽，严格往返后保持路径与总长，拒绝超量或损坏节点。
func TestMountainCavePathState(t *testing.T) {
	path := []terrainMountainCaveNode{{Id: "a", Z: .2, Width: 1}, {Id: "b", X: .2, Y: .1, Z: .4, Width: .6}, {Id: "c", X: .5, Y: .2, Z: .3, Width: 2}}
	height := .9
	path[1].Height = &height
	length, previous := 0.0, terrainMountainCaveNode{}
	for _, p := range path {
		length += math.Sqrt(math.Pow(p.X-previous.X, 2) + math.Pow(p.Y-previous.Y, 2) + math.Pow(p.Z-previous.Z, 2))
		previous = p
	}
	cave := terrainMountainCave{Id: "cave", Height: .5, Radius: .1, Aspect: .8, Depth: length, Path: path}
	mountain := terrainMountain{Id: "mountain", Version: 1, Center: []float64{0, 0}, Height: 8, Radius: 8, Seed: 1867,
		Branches: []terrainMountainBranch{}, Caves: []terrainMountainCave{cave}, Sediment: 1, Grass: 1, Gravel: .4, Erosion: 1}
	data, err := json.Marshal(mountain)
	if err != nil {
		t.Fatal(err)
	}
	source := `{"platforms":[{"id":"p1","kind":"platform","materialId":"grass","transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[64,1,64]},"heightField":{"version":2,"enabled":false,"baseHeight":0.006,"cellSize":0.5,"samplesPerChunk":64,"heightUnit":0.005,"zeroCode":32768,"chunks":[]},"mountains":[MOUNTAIN]}],"objects":[]}`
	normalized, err := validateState(json.RawMessage(strings.Replace(source, "MOUNTAIN", string(data), 1)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(normalized), `"path":[`) || !strings.Contains(string(normalized), `"width":2`) {
		t.Fatalf("通道截面丢失: %s", normalized)
	}
	if _, err := validateState(normalized); err != nil {
		t.Fatalf("无法重新读取通道: %v", err)
	}
	for name, change := range map[string]func(*terrainMountainCave){
		"empty":      func(c *terrainMountainCave) { c.Path = []terrainMountainCaveNode{} },
		"too many":   func(c *terrainMountainCave) { c.Path = append(c.Path, append(c.Path, c.Path...)...) },
		"width":      func(c *terrainMountainCave) { c.Path[1].Width = 0 },
		"height":     func(c *terrainMountainCave) { invalid := 0.0; c.Path[1].Height = &invalid },
		"position":   func(c *terrainMountainCave) { c.Path[1].Y = 4.1 },
		"nonfinite":  func(c *terrainMountainCave) { c.Path[1].X = math.NaN() },
		"duplicate":  func(c *terrainMountainCave) { c.Path[1].Id = c.Path[0].Id },
		"coincident": func(c *terrainMountainCave) { c.Path[1] = c.Path[0]; c.Path[1].Id = "other" },
		"depth":      func(c *terrainMountainCave) { c.Depth = .1 },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := cave
			invalid.Path = append([]terrainMountainCaveNode{}, cave.Path...)
			change(&invalid)
			copy := mountain
			copy.Caves = []terrainMountainCave{invalid}
			if err := validateTerrainMountains([]terrainMountain{copy}); !bizerr.IsKind(err, bizerr.KindParameter) {
				t.Fatalf("应拒绝损坏路径，得到 %v", err)
			}
		})
	}
}
