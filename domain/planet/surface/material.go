// Package surface 定义星球表面可持久化的公共语义。
package surface

var materialIDs = map[string]struct{}{
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

// 地形和道路围栏共用的可持久化纹理。
var fenceMaterialIDs = map[string]struct{}{
	"grass":         {},
	"pebble":        {},
	"yellow-pebble": {},
	"jade":          {},
	"rockscape":     {},
	"marble":        {},
	"asphalt":       {},
	"brick":         {},
	"wood":          {},
	"steel":         {},
}

// IsMaterialID 判断纹理是否为地形和道路都允许持久化的表面纹理。
func IsMaterialID(id string) bool {
	_, exists := materialIDs[id]
	return exists
}

// 判断纹理是否允许用于地形和道路围栏。
func IsFenceMaterialID(id string) bool {
	_, exists := fenceMaterialIDs[id]
	return exists
}
