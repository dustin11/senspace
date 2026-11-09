package terrain

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// 保存名单由前端植物目录生成，新增植物不再分别维护笔刷映射。
//
//go:embed plant_catalog.json
var terrainPlantCatalogJSON []byte

// 生成目录只传递持久化校验所需的身份和分类。
type terrainPlantCatalogEntry struct {
	// 稳定植物资源名。
	ID string `json:"id"`
	// 所属种植规则。
	CategoryID string `json:"categoryId"`
}

func init() {
	var entries []terrainPlantCatalogEntry
	if err := json.Unmarshal(terrainPlantCatalogJSON, &entries); err != nil {
		panic(fmt.Errorf("植物目录无法解码: %w", err))
	}
	for _, entry := range entries {
		if entry.ID == "" || entry.CategoryID == "" {
			panic("植物目录缺少资源身份或分类")
		}
		terrainPlantPresetIds[entry.ID] = struct{}{}
		terrainObjectPresetIds[entry.ID] = struct{}{}
		if terrainVegetationCategoryPresetIds[entry.CategoryID] == nil {
			terrainVegetationCategoryPresetIds[entry.CategoryID] = map[string]struct{}{}
		}
		terrainVegetationCategoryPresetIds[entry.CategoryID][entry.ID] = struct{}{}
	}
}

// 装饰枝叶可以挂在木本植物上，草花及其它覆盖仍不能使用植物宿主。
func validTerrainCanopyHost(categoryID, presetID string) bool {
	if categoryID != "canopy" {
		return false
	}
	_, tree := terrainVegetationCategoryPresetIds["trees"][presetID]
	_, shrub := terrainVegetationCategoryPresetIds["shrubs"][presetID]
	return tree || shrub
}

// 单株和预制体沿用同一枝叶宿主规则。
func validTerrainPlantAttachment(ownerPresetID, hostPresetID string) bool {
	_, canopy := terrainVegetationCategoryPresetIds["canopy"][ownerPresetID]
	return canopy && validTerrainCanopyHost("canopy", hostPresetID)
}
