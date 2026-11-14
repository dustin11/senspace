package terrain

import (
	"math"

	"senspace/pkg/bizerr"
)

// 道路截面使用平台局部米制坐标，持久化定义复用既有山洞的体积绘制。
type terrainRoadTunnelSection struct {
	// 路面中心及保持道路姿态的单位标架。
	Point []float64 `json:"point"`
	Right []float64 `json:"right"`
	Up    []float64 `json:"up"`
	// 平底半宽包含路肩和侧向余量，净高从路面向上计。
	HalfWidth float64 `json:"halfWidth"`
	Height    float64 `json:"height"`
}

// 道路身份用于重新加载后更新或删除通路，不保存网格、运行时版本或拖动预览。
type terrainRoadTunnel struct {
	Id       string                     `json:"id"`
	Sections []terrainRoadTunnelSection `json:"sections"`
}

// 与前端道路容量、截面精简和向量校验保持一致，拒绝不可重建的发布内容。
func validateTerrainRoadTunnels(tunnels []terrainRoadTunnel) error {
	if len(tunnels) > 2000 {
		return bizerr.Parameter("道路山洞数量超过上限")
	}
	ids := make(map[string]struct{}, len(tunnels))
	for _, tunnel := range tunnels {
		if err := validateTerrainRecordId(tunnel.Id, ids); err != nil {
			return err
		}
		if len(tunnel.Sections) < 2 || len(tunnel.Sections) > 4096 {
			return bizerr.Parameter("道路山洞截面数量无效")
		}
		for _, section := range tunnel.Sections {
			if !validTerrainVector(section.Point, false, 1_000_000) ||
				!validTerrainRoadTunnelDirection(section.Right) || !validTerrainRoadTunnelDirection(section.Up) ||
				math.IsNaN(section.HalfWidth) || math.IsInf(section.HalfWidth, 0) || section.HalfWidth <= 0 || section.HalfWidth > 10_000 ||
				math.IsNaN(section.Height) || math.IsInf(section.Height, 0) || section.Height <= 0 || section.Height > 10_000 {
				return bizerr.Parameter("道路山洞截面参数无效")
			}
		}
	}
	return nil
}

// 防止零向量或损坏标架使洞室无法与道路宽度一致。
func validTerrainRoadTunnelDirection(direction []float64) bool {
	return validTerrainVector(direction, false, 1.001) &&
		math.Abs(math.Sqrt(direction[0]*direction[0]+direction[1]*direction[1]+direction[2]*direction[2])-1) <= .001
}
