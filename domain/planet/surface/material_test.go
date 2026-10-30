package surface

import "testing"

// 覆盖地形与道路共用纹理的服务端白名单。
func TestIsMaterialID(t *testing.T) {
	for _, id := range []string{
		"grass",
		"pebble",
		"yellow-pebble",
		"jade",
		"rockscape",
		"warm-rockscape",
		"warm-marble",
		"marble",
		"asphalt",
		"brick",
		"wood",
		"steel",
	} {
		if !IsMaterialID(id) {
			t.Fatalf("expected %q to be a material id", id)
		}
	}
	if IsMaterialID("stone") {
		t.Fatal("removed road style must not be a material id")
	}
}
