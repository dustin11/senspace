package terrain

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// 联合工作区必须提交与唯一源一致的保存名单；独立后端由镜像构建强制生成。
func TestPlantCatalogMatchesSource(t *testing.T) {
	const frontendRoot = "../../../../senspace-web"
	data, err := os.ReadFile(frontendRoot + "/src/components/StarSky/terrain/domain/schema/TerrainPlantCatalog.data.json")
	if err != nil {
		if _, rootErr := os.Stat(frontendRoot); os.IsNotExist(rootErr) {
			t.Skip("独立后端检出；API 镜像从同一工作区的植物源目录强制生成名单")
		}
		t.Fatal(err)
	}
	var source, generated []terrainPlantCatalogEntry
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(terrainPlantCatalogJSON, &generated); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source, generated) {
		t.Fatal("植物保存名单与源目录不一致，请运行 yarn prepare:terrain-catalog 并提交生成文件")
	}
}

// 目录登记的每个品种均能保存为单株和笔刷，不再维护另一份测试名单。
func TestPlantCatalogSave(t *testing.T) {
	var entries []terrainPlantCatalogEntry
	if err := json.Unmarshal(terrainPlantCatalogJSON, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("植物目录为空")
	}
	for _, plant := range entries {
		t.Run(plant.ID, func(t *testing.T) {
			source := fmt.Sprintf(`{"platforms":[],"objects":[
				{"id":"host","kind":"object","presetId":"box","variantSeed":1,"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]}},
				{"id":"single","kind":"object","presetId":%q,"variantSeed":2,"transform":{"position":[0,0.5,0],"rotation":[0,0,0],"scale":[1,1,1]}}
			],"vegetationPatches":[{"id":"patch","kind":"vegetation-patch","presetId":%q,"categoryId":%q,"seed":7,
				"surface":{"kind":"object","hostObjectId":"host"},"instances":[{"kind":"mesh","triangleIndex":0,"barycentric":[0.2,0.3,0.5],
				"fallbackLocalPoint":[0,0.5,0],"tangentRotation":0.4,"normalOffset":0.01,"scale":[1,1,1],"variantSeed":9}]}]}`,
				plant.ID, plant.ID, plant.CategoryID)
			if _, err := validateState(json.RawMessage(source)); err != nil {
				t.Fatalf("目录植物无法保存: %v", err)
			}
			wrong := strings.Replace(source, `"categoryId":"`+plant.CategoryID+`"`, `"categoryId":"moss-coverage"`, 1)
			if _, err := validateState(json.RawMessage(wrong)); err == nil {
				t.Fatal("分类与品种不符的笔刷应拒绝")
			}
		})
	}
}

// 枝叶允许木本宿主，但草花、覆盖层和循环贴附仍受原校验约束。
func TestPlantCanopyHost(t *testing.T) {
	objectIDs := map[string]struct{}{"tree": {}, "grass": {}, "canopy": {}}
	presets := map[string]string{"tree": "cypress", "grass": "grass-clump", "canopy": "mountain-canopy"}
	surface := terrainVegetationPatchSurface{Kind: "object", HostObjectId: "tree"}
	if err := validateTerrainVegetationSurface(surface, nil, objectIDs, presets, "canopy"); err != nil {
		t.Fatal(err)
	}
	if err := validateTerrainVegetationSurface(surface, nil, objectIDs, presets, "flowers"); err == nil {
		t.Fatal("草花不能挂在树上")
	}
	surface.HostObjectId = "grass"
	if err := validateTerrainVegetationSurface(surface, nil, objectIDs, presets, "canopy"); err == nil {
		t.Fatal("草不能承载枝叶")
	}
	anchor := &terrainSurfaceAnchor{Kind: "simple", HostObjectId: "tree", LocalTransform: &terrainTransform{
		Position: []float64{0, 0, 0}, Rotation: []float64{0, 0, 0}, Scale: []float64{1, 1, 1},
	}}
	if err := validateTerrainSurfaceAnchor("canopy", anchor, objectIDs, presets); err != nil {
		t.Fatal(err)
	}
	if err := validateTerrainSurfaceAnchor("grass", anchor, objectIDs, presets); err == nil {
		t.Fatal("草的单株锚点不能挂在树上")
	}
}
