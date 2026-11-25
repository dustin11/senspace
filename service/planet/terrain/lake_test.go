package terrain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// 发布与读取保留两类地质和参数；普通笔刷的启用状态独立于湖盆。
func TestLakeStateRoundTrip(t *testing.T) {
	const source = `{"platforms":[{"id":"ground","kind":"platform","materialId":"grass","transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[64,1,64]},"heightField":{"version":2,"enabled":false,"baseHeight":0.006,"cellSize":0.5,"samplesPerChunk":64,"heightUnit":0.005,"zeroCode":32768,"chunks":[],"lakes":[{"id":"lake","version":1,"center":[2,-1],"radius":4,"width":2,"depth":1.5,"rotation":-0.24,"geology":"sediment","seed":325}]}}],"objects":[]}`
	const defaultPoint = `,{"radiusScale":1,"steepness":1,"shelfRatio":0}`
	const customPoint = `{"radiusScale":1.24,"steepness":0.7,"shelfRatio":0.35}`
	shoreline := `,"shoreline":[` + customPoint + strings.Repeat(defaultPoint, 7) + `]`
	for _, params := range []string{`"geology":"sediment"`, `"geology":"bedrock","innerRatio":0.72` + shoreline} {
		data := []byte(strings.Replace(source, `"geology":"sediment"`, params, 1))
		normalized, err := validateState(data)
		if err != nil {
			t.Fatal(err)
		}
		var before, after terrainState
		if err := json.Unmarshal(data, &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(normalized, &after); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before.Platforms[0].HeightField, after.Platforms[0].HeightField) {
			t.Fatal("发布时丢失湖盆参数")
		}
		if strings.Contains(params, `"innerRatio"`) != strings.Contains(string(normalized), `"innerRatio":0.72`) {
			t.Fatal("发布时应保留内圈及其缺省状态")
		}
		if strings.Contains(params, `"shoreline"`) != strings.Contains(string(normalized), shoreline) {
			t.Fatal("发布时应保留岸线控制点及其缺省状态")
		}
		if _, err := validateState(normalized); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]string{
		{"sediment", "lava"}, {`"depth":1.5`, `"depth":-2`}, {`"center":[2,-1]`, `"center":[2]`},
		{`"depth":1.5`, `"depth":1.5,"innerRatio":0.14`}, {`"depth":1.5`, `"depth":1.5,"innerRatio":0.86`},
		{`"depth":1.5`, `"depth":1.5,"shoreline":[` + customPoint + `]`},
		{`"depth":1.5`, `"depth":1.5` + strings.Replace(shoreline, `"radiusScale":1.24`, `"radiusScale":1.56`, 1)},
		{`"depth":1.5`, `"depth":1.5` + strings.Replace(shoreline, `"steepness":0.7`, `"steepness":0.64`, 1)},
		{`"depth":1.5`, `"depth":1.5` + strings.Replace(shoreline, `"shelfRatio":0.35`, `"shelfRatio":0.46`, 1)},
	} {
		if _, err := validateState([]byte(strings.Replace(source, pair[0], pair[1], 1))); err == nil {
			t.Fatal("应拒绝无效湖盆", pair)
		}
	}
	one := terrainLake{Version: 1, Id: "same", Center: []float64{1, 1}, Radius: 2, Width: 1, Depth: 1, Geology: "bedrock"}
	if validateTerrainLakes([]terrainLake{one, one}) == nil || validateTerrainLakes(make([]terrainLake, 49)) == nil {
		t.Fatal("应拒绝重复或过量湖盆")
	}
}
