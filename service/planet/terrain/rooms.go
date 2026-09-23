package terrain

import (
	"math"
	"regexp"
	"senspace/pkg/bizerr"
)

// 房间布局绑定静态形状锚点，坐标使用锚点局部空间。
type terrainRoomLayout struct {
	Signature string              `json:"signature"`
	Rooms     []terrainRoom       `json:"rooms"`
	Portals   []terrainRoomPortal `json:"portals"`
}

// 有界室内区域。
type terrainRoom struct {
	ID  string    `json:"id"`
	Min []float64 `json:"min"`
	Max []float64 `json:"max"`
}

// 连接室内与外部的凸平面开口。
type terrainRoomPortal struct {
	From        string      `json:"from"`
	To          string      `json:"to"`
	Polygon     [][]float64 `json:"polygon"`
	DoorRigID   string      `json:"doorRigId,omitempty"`
	ClosedValue float64     `json:"closedValue,omitempty"`
	Transparent bool        `json:"transparent,omitempty"`
}

var roomIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// 保存时验证边界和引用；封闭性由模板或用户确认，结构指纹在运行时验证。
func validateTerrainRooms(layout *terrainRoomLayout) error {
	if layout == nil {
		return nil
	}
	invalid := func() error { return bizerr.Parameter("房间区域或开口描述无效") }
	if len(layout.Signature) > 128 || len(layout.Rooms) == 0 || len(layout.Rooms) > 64 || len(layout.Portals) > 256 {
		return invalid()
	}
	point := func(p []float64) bool {
		if len(p) != 3 {
			return false
		}
		for _, n := range p {
			if math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 10000 {
				return false
			}
		}
		return true
	}
	ids := map[string]bool{"outside": true}
	for _, room := range layout.Rooms {
		if !roomIDPattern.MatchString(room.ID) || ids[room.ID] || !point(room.Min) || !point(room.Max) {
			return invalid()
		}
		for i := 0; i < 3; i++ {
			if room.Min[i] >= room.Max[i] {
				return invalid()
			}
		}
		ids[room.ID] = true
	}
	sub := func(a, b []float64) []float64 { return []float64{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
	cross := func(a, b []float64) []float64 {
		return []float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
	}
	dot := func(a, b []float64) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
	for _, portal := range layout.Portals {
		p := portal.Polygon
		if !ids[portal.From] || !ids[portal.To] || portal.From == portal.To || len(p) < 3 || len(p) > 16 || len(portal.DoorRigID) > 128 || math.IsNaN(portal.ClosedValue) || math.IsInf(portal.ClosedValue, 0) {
			return invalid()
		}
		for _, v := range p {
			if !point(v) {
				return invalid()
			}
		}
		normal := cross(sub(p[1], p[0]), sub(p[2], p[1]))
		length := math.Sqrt(dot(normal, normal))
		if length < 1e-8 {
			return invalid()
		}
		for i, v := range p {
			if math.Abs(dot(sub(v, p[0]), normal)) > length*1e-5 || dot(cross(sub(p[(i+1)%len(p)], v), sub(p[(i+2)%len(p)], p[(i+1)%len(p)])), normal) <= 0 {
				return invalid()
			}
		}
	}
	return nil
}
