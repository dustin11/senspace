package terrain

import (
	"encoding/json"
	"testing"
)

func TestRoomLayoutRoundTripAndValidation(t *testing.T) {
	source := `{"signature":"123","rooms":[{"id":"inside","min":[-2,0,-2],"max":[2,3,2]}],"portals":[{"from":"outside","to":"inside","polygon":[[-1,0,2],[1,0,2],[1,2,2],[-1,2,2]],"doorRigId":"door","transparent":true}]}`
	var layout terrainRoomLayout
	if err := json.Unmarshal([]byte(source), &layout); err != nil {
		t.Fatal(err)
	}
	if err := validateTerrainRooms(&layout); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(layout)
	if err != nil {
		t.Fatal(err)
	}
	var restored terrainRoomLayout
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Portals[0].DoorRigID != "door" || !restored.Portals[0].Transparent {
		t.Fatal("开口状态丢失")
	}
	layout.Portals[0].To = "missing"
	if validateTerrainRooms(&layout) == nil {
		t.Fatal("应拒绝悬空区域")
	}
	layout.Portals[0].To = "inside"
	layout.Portals[0].Polygon[3][2] = 3
	if validateTerrainRooms(&layout) == nil {
		t.Fatal("应拒绝非平面开口")
	}
}
