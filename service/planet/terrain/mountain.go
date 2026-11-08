package terrain

import (
	"math"
	"senspace/pkg/bizerr"
)

// 山脊控制点，尺寸为相对主山的比例。
type terrainMountainNode struct {
	// 稳定身份与局部坐标。
	Id string  `json:"id"`
	X  float64 `json:"x"`
	Z  float64 `json:"z"`
	// 相对高度和半宽。
	Height float64 `json:"height"`
	Width  float64 `json:"width"`
}

// 可弯曲的侧峰山脊。
type terrainMountainBranch struct {
	// 稳定身份、有序节点及内侧陡度。
	Id             string                `json:"id"`
	Points         []terrainMountainNode `json:"points"`
	InnerSteepness float64               `json:"innerSteepness"`
}

// 雕入山体的局部岩台，位置和大小相对山半径，高程相对山高。
type terrainMountainLedge struct {
	// 稳定身份与山局部中心。
	Id string  `json:"id"`
	X  float64 `json:"x"`
	Z  float64 `json:"z"`
	// 顶面高度、长轴半径、短长轴比例及朝向。
	Height   float64 `json:"height"`
	Radius   float64 `json:"radius"`
	Aspect   float64 `json:"aspect"`
	Rotation float64 `json:"rotation"`
	// 可选草量，缺省时不写入记录，显式零值仍须保留。
	Grass *float64 `json:"grass,omitempty"`
}

// 平台上的可重建山体，不保存派生网格或纹理。
type terrainMountain struct {
	// 稳定身份和算法版本。
	Id      string `json:"id"`
	Version int    `json:"version"`
	// 平台局部落点、山高、半径与朝向。
	Center   []float64 `json:"center"`
	Height   float64   `json:"height"`
	Radius   float64   `json:"radius"`
	Rotation float64   `json:"rotation"`
	// 稳定随机源与侧峰定义。
	Seed     int64                   `json:"seed"`
	Branches []terrainMountainBranch `json:"branches"`
	// 没有雕刻时省略岩台定义。
	Ledges []terrainMountainLedge `json:"ledges,omitempty"`
	// 同次地形的地表强度。
	Sediment float64 `json:"sediment"`
	Grass    float64 `json:"grass"`
	Gravel   float64 `json:"gravel"`
	Erosion  float64 `json:"erosion"`
}

// 校验山体和曲线容量，防止不可重建或无限量生成的发布参数。
func validateTerrainMountains(mountains []terrainMountain) error {
	if len(mountains) > 8 {
		return bizerr.Parameter("山地数量超过上限")
	}
	ids := make(map[string]struct{})
	valid := func(n, low, high float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) && n >= low && n <= high }
	for _, m := range mountains {
		if err := validateTerrainRecordId(m.Id, ids); err != nil {
			return err
		}
		if m.Version != 1 || len(m.Center) != 2 || !valid(m.Center[0], -10000, 10000) || !valid(m.Center[1], -10000, 10000) ||
			!valid(m.Radius, 1, 500) || !valid(m.Height, .2, 1000) || m.Height/m.Radius > 2.5 ||
			!valid(m.Rotation, -math.Pi*8, math.Pi*8) || m.Seed < 0 || m.Seed > 2147483647 ||
			!valid(m.Sediment, 0, 2) || !valid(m.Grass, 0, 2) || !valid(m.Gravel, 0, 2) || !valid(m.Erosion, 0, 2) || len(m.Branches) > 6 {
			return bizerr.Parameter("山地参数无效")
		}
		if len(m.Ledges) > 12 {
			return bizerr.Parameter("每座山最多雕刻 12 处平台")
		}
		nodes := make(map[string]struct{})
		for _, ledge := range m.Ledges {
			if err := validateTerrainRecordId(ledge.Id, nodes); err != nil {
				return err
			}
			if !valid(ledge.X, -4, 4) || !valid(ledge.Z, -4, 4) || !valid(ledge.Height, .01, 1.25) ||
				!valid(ledge.Radius, .04, .65) || !valid(ledge.Aspect, .4, 1) || !valid(ledge.Rotation, -math.Pi*8, math.Pi*8) ||
				(ledge.Grass != nil && !valid(*ledge.Grass, 0, 2)) {
				return bizerr.Parameter("山体平台参数无效")
			}
		}
		for _, branch := range m.Branches {
			if err := validateTerrainRecordId(branch.Id, nodes); err != nil {
				return err
			}
			if len(branch.Points) < 2 || len(branch.Points) > 8 || !valid(branch.InnerSteepness, 0, 1) {
				return bizerr.Parameter("山脊参数无效")
			}
			for _, p := range branch.Points {
				if err := validateTerrainRecordId(p.Id, nodes); err != nil {
					return err
				}
				if !valid(p.X, -3, 3) || !valid(p.Z, -3, 3) || !valid(p.Height, .05, 1) || !valid(p.Width, .08, .65) {
					return bizerr.Parameter("山脊控制点无效")
				}
			}
			if err := validateMountainRidgePath(branch.Points); err != nil {
				return err
			}
		}
	}
	return nil
}

// 路径保持连接主山，拒绝重合节点和自交控制线。
func validateMountainRidgePath(points []terrainMountainNode) error {
	if math.Hypot(points[0].X, points[0].Z) > .65 {
		return bizerr.Parameter("山脊根部须保留在主山山肩内")
	}
	cross := func(a, b, c terrainMountainNode) float64 { return (b.X-a.X)*(c.Z-a.Z) - (b.Z-a.Z)*(c.X-a.X) }
	for i := 0; i < len(points)-1; i++ {
		a, b := points[i], points[i+1]
		if math.Hypot(a.X-b.X, a.Z-b.Z) < .04 {
			return bizerr.Parameter("相邻山脊节点过近")
		}
		for j := i + 2; j < len(points)-1; j++ {
			c, d := points[j], points[j+1]
			if math.Max(a.X, b.X) < math.Min(c.X, d.X) || math.Max(c.X, d.X) < math.Min(a.X, b.X) ||
				math.Max(a.Z, b.Z) < math.Min(c.Z, d.Z) || math.Max(c.Z, d.Z) < math.Min(a.Z, b.Z) {
				continue
			}
			if cross(a, b, c)*cross(a, b, d) <= 0 && cross(c, d, a)*cross(c, d, b) <= 0 {
				return bizerr.Parameter("山脊不能交叉或闭合")
			}
		}
	}
	return nil
}
