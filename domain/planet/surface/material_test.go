package surface

import "testing"

// 覆盖地形与道路共用纹理的服务端白名单。
func TestIsMaterialID(t *testing.T) {
	for _, id := range []string{
		"plain",
		"grass",
		"alpine-grass",
		"muddy-grass",
		"mud",
		"sand",
		"moss",
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

// 围栏可保存菜单中的纹理，仍拒绝未开放的地表材质。
func TestIsFenceMaterialID(t *testing.T) {
	for _, id := range []string{"grass", "pebble", "yellow-pebble", "jade", "rockscape", "marble", "asphalt", "brick", "wood", "steel"} {
		if !IsFenceMaterialID(id) {
			t.Fatalf("围栏纹理 %q 未被支持", id)
		}
	}
	for _, id := range []string{"plastic", "plain", "mud"} {
		if IsFenceMaterialID(id) {
			t.Fatalf("接受了非法围栏纹理 %q", id)
		}
	}
}
