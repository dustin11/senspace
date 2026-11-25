package terrain

import (
	"math"

	"senspace/pkg/bizerr"
)

// 湖盆保存在平台局部坐标内。湖床网格与可蓄水范围从这些参数重建。
type terrainLake struct {
	Version  int       `json:"version"`
	Id       string    `json:"id"`
	Center   []float64 `json:"center"`
	Rotation float64   `json:"rotation"`
	Radius   float64   `json:"radius"`
	Width    float64   `json:"width"`
	Depth    float64   `json:"depth"`
	// 内圈占岸线半径的比例，缺省时由湖形采用原有比例。
	InnerRatio *float64 `json:"innerRatio,omitempty"`
	// 岸线控制点按角度排列；缺省时使用种子岸线。
	Shoreline []terrainLakeShorePoint `json:"shoreline,omitempty"`
	Seed      int64                   `json:"seed"`
	Geology   string                  `json:"geology"`
}

// 岸线半径、坡度和浅滩宽度以湖盆局部比例保存。
type terrainLakeShorePoint struct {
	RadiusScale float64 `json:"radiusScale"`
	Steepness   float64 `json:"steepness"`
	ShelfRatio  float64 `json:"shelfRatio"`
}

func validateTerrainLakes(lakes []terrainLake) error {
	if len(lakes) > 48 {
		return bizerr.Parameter("每个平台最多容纳 48 个湖盆")
	}
	ids := make(map[string]struct{}, len(lakes))
	within := func(v, min, max float64) bool { return v >= min && v <= max }
	for _, lake := range lakes {
		if err := validateTerrainRecordId(lake.Id, ids); err != nil {
			return err
		}
		if lake.Version != 1 || len(lake.Center) != 2 || !within(lake.Center[0], -10000, 10000) ||
			!within(lake.Center[1], -10000, 10000) || !within(lake.Rotation, -math.Pi, math.Pi) ||
			!within(lake.Radius, .8, 64) || !within(lake.Width, .8, 64) || !within(lake.Depth, .15, 24) ||
			(lake.InnerRatio != nil && !within(*lake.InnerRatio, .15, .85)) ||
			lake.Seed < 0 || lake.Seed > 2147483647 || (lake.Geology != "sediment" && lake.Geology != "bedrock") {
			return bizerr.Parameter("湖盆地质或形态参数无效")
		}
		if len(lake.Shoreline) != 0 && len(lake.Shoreline) != 8 {
			return bizerr.Parameter("湖盆岸线需要 8 个控制点")
		}
		for _, point := range lake.Shoreline {
			if !within(point.RadiusScale, .5, 1.55) || !within(point.Steepness, .65, 1.6) || !within(point.ShelfRatio, 0, .45) {
				return bizerr.Parameter("湖盆岸线控制点参数无效")
			}
		}
	}
	return nil
}
