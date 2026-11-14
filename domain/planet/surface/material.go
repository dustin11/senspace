// Package surface 定义星球表面可持久化的公共语义。
package surface

var materialIDs = map[string]struct{}{
	"limestone":      {},
	"mountain":       {},
	"loess":          {},
	"plain":          {},
	"grass":          {},
	"alpine-grass":   {},
	"muddy-grass":    {},
	"mud":            {},
	"sand":           {},
	"moss":           {},
	"pebble":         {},
	"yellow-pebble":  {},
	"jade":           {},
	"rockscape":      {},
	"warm-rockscape": {},
	"warm-marble":    {},
	"marble":         {},
	"asphalt":        {},
	"brick":          {},
	"wood":           {},
	"steel":          {},
}

// IsMaterialID 判断纹理是否为地形和道路都允许持久化的表面纹理。
func IsMaterialID(id string) bool {
	_, exists := materialIDs[id]
	return exists
}

// 判断纹理是否允许用于地形和道路围栏。
func IsFenceMaterialID(id string) bool {
	return IsMaterialID(id)
}
