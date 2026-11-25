package terrain

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
)

// 退役资源只在读取历史发布态时识别。
var retiredBuildingPresetIds = map[string]bool{
	"wall-door": true, "wall-window": true, "house-door": true, "house-window": true,
	"house-gable": true, "house-roof": true, "house-lamp": true, "pavilion-roof": true,
	"pavilion-column": true, "pavilion-railing": true, "stone-table": true, "stone-stool": true,
}

var renamedTerrainBuildingPresets = map[string]string{
	"白墙赤瓦小屋 · 新": "小屋",
	"碧瓦月窗凉亭 · 新": "凉亭",
}

func hasRetiredBuildingData(state json.RawMessage) bool {
	for id := range retiredBuildingPresetIds {
		if bytes.Contains(state, []byte(`"`+id+`"`)) {
			return true
		}
	}
	for name := range renamedTerrainBuildingPresets {
		if bytes.Contains(state, []byte(name)) {
			return true
		}
	}
	return false
}

func isRetiredBuilding(name string, presets []string) bool {
	switch name {
	case "模块房屋":
		if slices.Contains(presets, "house-roof") {
			return true
		}
		if len(presets) != 6 {
			return false
		}
		counts := map[string]int{}
		for _, preset := range presets {
			counts[preset]++
		}
		return counts["wall-door"] == 1 && counts["wall-window"] == 3 && counts["box"] == 2
	case "中式凉亭":
		return slices.Contains(presets, "pavilion-roof")
	default:
		return false
	}
}

// 历史发布态只改迁移涉及的字段，其余字段和数值精度原样保留。
type terrainMigrationRecord = map[string]any

func migrationRecord(value any) terrainMigrationRecord {
	record, _ := value.(terrainMigrationRecord)
	return record
}

func migrationString(record terrainMigrationRecord, key string) string {
	value, _ := record[key].(string)
	return value
}

func migrationList(record terrainMigrationRecord, key string) []any {
	value, _ := record[key].([]any)
	return value
}

func migrationHostPartIndex(record terrainMigrationRecord) int {
	number, ok := record["surfaceHostPartIndex"].(json.Number)
	if !ok {
		return -1
	}
	index, err := number.Int64()
	if err != nil || index < 0 || index >= maxPrefabParts {
		return -1
	}
	return int(index)
}

func migrateRetiredBuildingPrefab(prefab terrainMigrationRecord) (removed, changed bool) {
	parts := migrationList(prefab, "parts")
	presets := make([]string, len(parts))
	for index, part := range parts {
		presets[index] = migrationString(migrationRecord(part), "presetId")
	}
	if isRetiredBuilding(migrationString(prefab, "name"), presets) {
		return true, true
	}
	indices := make(map[int]int, len(parts))
	kept := parts[:0]
	for index, part := range parts {
		if retiredBuildingPresetIds[presets[index]] {
			continue
		}
		indices[index] = len(kept)
		kept = append(kept, part)
	}
	if len(kept) == len(parts) {
		return false, false
	}
	if len(kept) == 0 {
		return true, true
	}
	for _, value := range kept {
		part := migrationRecord(value)
		if part["surfaceHostPartIndex"] == nil {
			continue
		}
		if index, exists := indices[migrationHostPartIndex(part)]; exists {
			part["surfaceHostPartIndex"] = index
		} else {
			delete(part, "surfaceHostPartIndex")
			delete(part, "surfaceLocalTransform")
		}
	}
	prefab["parts"] = kept
	for _, field := range []string{"vegetationPatches", "vegetationCoverageLayers"} {
		if prefab[field] == nil {
			continue
		}
		records := migrationList(prefab, field)
		records = slices.DeleteFunc(records, func(value any) bool {
			record := migrationRecord(value)
			index, exists := indices[migrationHostPartIndex(record)]
			if exists {
				record["surfaceHostPartIndex"] = index
			}
			return !exists
		})
		prefab[field] = records
	}
	return false, true
}

func migrateRetiredTerrainBuildings(state json.RawMessage) (json.RawMessage, bool) {
	if !hasRetiredBuildingData(state) {
		return state, false
	}
	var document terrainMigrationRecord
	decoder := json.NewDecoder(bytes.NewReader(state))
	decoder.UseNumber()
	if decoder.Decode(&document) != nil {
		return state, false
	}
	objects := migrationList(document, "objects")
	assemblies := migrationList(document, "assemblies")
	presets := make(map[string]string, len(objects))
	shapeObjects := make(map[string]bool)
	removedIds := make(map[string]bool)
	for _, value := range objects {
		object := migrationRecord(value)
		id := migrationString(object, "id")
		preset := migrationString(object, "presetId")
		presets[id] = preset
		shapeObjects[id] = object["shape"] != nil
		if retiredBuildingPresetIds[preset] {
			removedIds[id] = true
		}
	}
	assembliesChanged := false
	assemblies = slices.DeleteFunc(assemblies, func(value any) bool {
		assembly := migrationRecord(value)
		name := migrationString(assembly, "name")
		if newName := renamedTerrainBuildingPresets[name]; newName != "" {
			ids := migrationList(assembly, "memberIds")
			if len(ids) > 0 && !slices.ContainsFunc(ids, func(value any) bool {
				id, _ := value.(string)
				return !shapeObjects[id]
			}) {
				assembly["name"] = newName
				assembliesChanged = true
			}
		}
		if name != "模块房屋" && name != "中式凉亭" {
			return false
		}
		memberIds := migrationList(assembly, "memberIds")
		memberPresets := make([]string, len(memberIds))
		for index, value := range memberIds {
			id, _ := value.(string)
			memberPresets[index] = presets[id]
		}
		if !isRetiredBuilding(name, memberPresets) {
			return false
		}
		for _, value := range memberIds {
			id, _ := value.(string)
			removedIds[id] = true
		}
		assembliesChanged = true
		return true
	})

	prefabs := migrationList(document, "prefabs")
	prefabsChanged := false
	prefabs = slices.DeleteFunc(prefabs, func(value any) bool {
		removed, changed := migrateRetiredBuildingPrefab(migrationRecord(value))
		prefabsChanged = prefabsChanged || changed
		return removed
	})
	if !prefabsChanged && !assembliesChanged && len(removedIds) == 0 {
		return state, false
	}
	if prefabsChanged {
		document["prefabs"] = prefabs
	}
	if len(removedIds) > 0 {
		removeRetiredBuildingVegetation(document, removedIds)
		objects = slices.DeleteFunc(objects, func(value any) bool {
			object := migrationRecord(value)
			if removedIds[migrationString(object, "id")] {
				return true
			}
			attachment := migrationRecord(object["surfaceAttachment"])
			if removedIds[migrationString(attachment, "hostObjectId")] {
				delete(object, "surfaceAttachment")
			}
			return false
		})
		assemblies = slices.DeleteFunc(assemblies, func(value any) bool {
			assembly := migrationRecord(value)
			ids := migrationList(assembly, "memberIds")
			kept := slices.DeleteFunc(ids, func(value any) bool {
				id, _ := value.(string)
				return removedIds[id]
			})
			if len(ids) != len(kept) {
				assembly["memberIds"] = kept
			}
			return len(kept) < 2
		})
		document["objects"] = objects
		assembliesChanged = true
	}
	if assembliesChanged {
		if assemblies == nil {
			assemblies = []any{}
		}
		document["assemblies"] = assemblies
	}
	migrated, err := json.Marshal(document)
	if err != nil {
		return state, false
	}
	return migrated, true
}

func removeRetiredBuildingVegetation(document terrainMigrationRecord, removedIds map[string]bool) {
	for _, field := range []string{"vegetationPatches", "vegetationCoverageLayers"} {
		if document[field] == nil {
			continue
		}
		records := migrationList(document, field)
		records = slices.DeleteFunc(records, func(value any) bool {
			record := migrationRecord(value)
			surface := migrationRecord(record["surface"])
			removedHost := migrationString(surface, "kind") == "object" &&
				removedIds[migrationString(surface, "hostObjectId")]
			if field == "vegetationCoverageLayers" {
				return removedHost
			}
			instances := migrationList(record, "instances")
			kept := instances[:0]
			for index, value := range instances {
				instance := migrationRecord(value)
				attachment := migrationRecord(instance["surfaceAttachment"])
				if (removedHost && instance["transform"] == nil) ||
					removedIds[migrationString(attachment, "hostObjectId")] {
					instanceKey, ok := instance["key"].(json.Number)
					if !ok {
						instanceKey = json.Number(strconv.Itoa(index))
					}
					removedIds[migrationString(record, "id")+"#"+instanceKey.String()] = true
					continue
				}
				if len(kept) != index && instance["key"] == nil {
					instance["key"] = index
				}
				kept = append(kept, instance)
			}
			if len(kept) != len(instances) {
				record["instances"] = kept
			}
			return len(kept) == 0
		})
		document[field] = records
	}
}
