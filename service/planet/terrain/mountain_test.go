package terrain

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
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
