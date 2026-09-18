package factory_service

import "path/filepath"

// 单次快照构建内的模板索引，不跨任务缓存冻结文件。
type snapshotTemplateItems map[string]map[string]map[string]any

// 同一 metadataRef 只读取和解析一次，展示属性与库存证明共用原始参数。
func (cache snapshotTemplateItems) find(snapshotDir, metadataRef, itemID string) (map[string]any, error) {
	file, field, hasField, err := parseMetadataRef(metadataRef)
	if err != nil {
		return nil, err
	}
	key := filepath.Join(snapshotDir, file)
	ref := file
	if hasField {
		key += "#" + field
		ref += "#" + field
	}
	index, ok := cache[key]
	if !ok {
		items, err := loadMetadataRefItems(snapshotDir, ref)
		if err != nil {
			return nil, err
		}
		index = make(map[string]map[string]any, len(items))
		for _, item := range items {
			id := stringFromJSONValue(item["id"])
			if _, exists := index[id]; !exists {
				index[id] = item
			}
		}
		cache[key] = index
	}
	item := index[itemID]
	if item == nil {
		return nil, newConflictError("冻结库存缺少原始参数")
	}
	return item, nil
}
