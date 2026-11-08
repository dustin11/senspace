package terrain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// 严格解码失败时列出未知字段的位置，避免嵌套同名字段导致逐个排查。
func unknownTerrainStateFields(state json.RawMessage) ([]string, error) {
	var value interface{}
	decoder := json.NewDecoder(bytes.NewReader(state))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var paths []string
	var visit func(interface{}, reflect.Type, string)
	visit = func(value interface{}, schema reflect.Type, path string) {
		// 错误信息仅包含有限数量的字段路径，不记录文档值。
		if len(paths) >= 16 {
			return
		}
		for schema.Kind() == reflect.Pointer {
			schema = schema.Elem()
		}
		switch schema.Kind() {
		case reflect.Struct:
			record, ok := value.(map[string]interface{})
			if !ok {
				return
			}
			for name, child := range record {
				var fieldType reflect.Type
				for i := 0; i < schema.NumField(); i++ {
					field := schema.Field(i)
					if strings.EqualFold(strings.Split(field.Tag.Get("json"), ",")[0], name) {
						fieldType = field.Type
						break
					}
				}
				childPath := path + "." + name
				if fieldType == nil {
					paths = append(paths, childPath)
				} else {
					visit(child, fieldType, childPath)
				}
				if len(paths) >= 16 {
					return
				}
			}
		case reflect.Slice, reflect.Array:
			if items, ok := value.([]interface{}); ok {
				for i, child := range items {
					visit(child, schema.Elem(), fmt.Sprintf("%s[%d]", path, i))
				}
			}
		}
	}
	visit(value, reflect.TypeOf(terrainState{}), "state")
	sort.Strings(paths)
	return paths, nil
}
