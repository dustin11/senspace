package terrain

import (
	"encoding/json"
	"strings"
	"testing"
)

// 日志一次报告各层未知字段，已知山地草量及动态形状参数不会误报。
func TestTerrainUnknownFieldPaths(t *testing.T) {
	state := json.RawMessage(`{"platforms":[{"mountains":[{"grass":1,"ledges":[{"unknownLedge":1}]}]}],"objects":[{"shape":{"parameters":{"grass":1},"unknownShape":1}}],"unknownRoot":1}`)
	_, err := validateState(state)
	if err == nil {
		t.Fatal("未知字段未被拒绝")
	}
	for _, path := range []string{
		"state.platforms[0].mountains[0].ledges[0].unknownLedge",
		"state.objects[0].shape.unknownShape",
		"state.unknownRoot",
	} {
		if !strings.Contains(err.Error(), path) {
			t.Fatalf("缺少字段路径 %s: %v", path, err)
		}
	}
	if strings.Contains(err.Error(), ".grass") {
		t.Fatalf("已知字段被误报: %v", err)
	}
}
