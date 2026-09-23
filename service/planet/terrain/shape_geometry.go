package terrain

import (
	"math"
	"senspace/pkg/bizerr"
)

const shapeEpsilon = 1e-8

// 有向面积用于与前端一致的闭合轮廓相交检测。
func shapeCross(a, b, c []float64) float64 {
	return (b[0]-a[0])*(c[1]-a[1]) - (b[1]-a[1])*(c[0]-a[0])
}
func shapeSegmentsIntersect(a, b, c, d []float64) bool {
	for axis := 0; axis < 2; axis++ {
		if math.Max(a[axis], b[axis])+shapeEpsilon < math.Min(c[axis], d[axis]) || math.Max(c[axis], d[axis])+shapeEpsilon < math.Min(a[axis], b[axis]) {
			return false
		}
	}
	return shapeCross(a, b, c)*shapeCross(a, b, d) <= shapeEpsilon && shapeCross(c, d, a)*shapeCross(c, d, b) <= shapeEpsilon
}
func shapePointInside(point []float64, polygon [][]float64) bool {
	inside := false
	for i, a := range polygon {
		b := polygon[(i+len(polygon)-1)%len(polygon)]
		if (a[1] > point[1]) != (b[1] > point[1]) && point[0] < (b[0]-a[0])*(point[1]-a[1])/(b[1]-a[1])+a[0] {
			inside = !inside
		}
	}
	return inside
}

// 发布前拒绝自交、零面积、越界和相互重叠的孔洞，避免存档无法重新加载。
func validateShapePolygon(profile [][]float64, holes [][][]float64) error {
	rings := append([][][]float64{profile}, holes...)
	for _, ring := range rings {
		area := 0.0
		for i, a := range ring {
			b := ring[(i+1)%len(ring)]
			if math.Hypot(a[0]-b[0], a[1]-b[1]) < shapeEpsilon {
				return bizerr.Parameter("轮廓存在重复相邻点")
			}
			area += a[0]*b[1] - b[0]*a[1]
			for j := i + 2; j < len(ring); j++ {
				if i == 0 && j == len(ring)-1 {
					continue
				}
				if shapeSegmentsIntersect(a, b, ring[j], ring[(j+1)%len(ring)]) {
					return bizerr.Parameter("轮廓不能自相交")
				}
			}
		}
		if math.Abs(area) < shapeEpsilon {
			return bizerr.Parameter("轮廓面积不能为零")
		}
	}
	for r := 1; r < len(rings); r++ {
		if !shapePointInside(rings[r][0], profile) {
			return bizerr.Parameter("孔洞必须位于外轮廓内部")
		}
		for s := 0; s < r; s++ {
			for i, a := range rings[r] {
				for j, c := range rings[s] {
					if shapeSegmentsIntersect(a, rings[r][(i+1)%len(rings[r])], c, rings[s][(j+1)%len(rings[s])]) {
						return bizerr.Parameter("孔洞不能与其他边界接触")
					}
				}
			}
			if s > 0 && (shapePointInside(rings[r][0], rings[s]) || shapePointInside(rings[s][0], rings[r])) {
				return bizerr.Parameter("孔洞不能重叠或嵌套")
			}
		}
	}
	return nil
}

// 扫掠路径拒绝重复点和原地折返；尚不验证任意曲线的全局自交。
func validateShapePath(path [][]float64) error {
	for i := 1; i < len(path); i++ {
		var length, previous, dot float64
		for axis := 0; axis < 3; axis++ {
			v := path[i][axis] - path[i-1][axis]
			length += v * v
			if i > 1 {
				u := path[i-1][axis] - path[i-2][axis]
				previous += u * u
				dot += u * v
			}
		}
		if math.Sqrt(length) < 1e-5 {
			return bizerr.Parameter("路径相邻点不能重复")
		}
		if i > 1 && dot/math.Sqrt(length*previous) < -0.99 {
			return bizerr.Parameter("路径不能原地折返")
		}
	}
	return nil
}
