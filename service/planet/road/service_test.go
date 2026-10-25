package road

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	road_domain "senspace/domain/planet/road"
	"senspace/pkg/bizerr"
)

// 覆盖道路 JSON 压缩、拓扑和严格字段校验。
func TestValidateState(t *testing.T) {
	valid := json.RawMessage(`{
		"nodes":[
			{"id":"n1","anchor":{"kind":"surface","surfaceId":"desktop","surfacePoint":[0,0,0]},"tangentMode":"auto","width":1.2},
			{"id":"n2","anchor":{"kind":"connector","connectorId":"door:outside","side":"outside"},"tangentMode":"manual","tangentIn":[-1,0,0],"width":1.5}
		],
		"edges":[{"id":"e1","fromNodeId":"n1","toNodeId":"n2","styleId":"asphalt","surfaceMode":"bridge","shoulderWidth":0.4,"maxGrade":0.12,"elevationOffset":0.75,"direction":"both","speedLimit":12,"routeModes":["ground","air"],"fence":{"modelId":"highway-guardrail","materialId":"steel"}}]
	}`)

	t.Run("compacts a valid graph", func(t *testing.T) {
		state, err := validateState(valid)
		if err != nil {
			t.Fatalf("validate state: %v", err)
		}
		if strings.Contains(string(state), "\n") {
			t.Fatalf("expected compact JSON, got %q", state)
		}
	})

	t.Run("accepts relaxed maximum grade", func(t *testing.T) {
		steep := strings.Replace(string(valid), `"maxGrade":0.12`, `"maxGrade":100`, 1)
		if _, err := validateState(json.RawMessage(steep)); err != nil {
			t.Fatalf("validate relaxed maximum grade: %v", err)
		}
	})

	t.Run("keeps corridor and junction relations", func(t *testing.T) {
		raw := json.RawMessage(`{
			"nodes":[
				{"id":"a","anchor":{"kind":"frame","frameId":"planet","localPoint":[-1,0,0]},"tangentMode":"auto","width":1},
				{"id":"b","anchor":{"kind":"frame","frameId":"planet","localPoint":[0,0,0]},"tangentMode":"auto","width":1,"junction":{"type":"t-junction","primaryEdgeIds":["ab","bc"],"cornerRadius":0.75,"branchSide":"right"}},
				{"id":"c","anchor":{"kind":"frame","frameId":"planet","localPoint":[1,0,0]},"tangentMode":"auto","width":1},
				{"id":"d","anchor":{"kind":"frame","frameId":"planet","localPoint":[0,0,1]},"tangentMode":"auto","width":1}
			],
			"edges":[
				{"id":"ab","fromNodeId":"a","toNodeId":"b","corridorId":"main","tangentTo":[1,0,0],"styleId":"asphalt","surfaceMode":"auto","shoulderWidth":0.3,"maxGrade":0.1,"elevationOffset":0,"direction":"both","speedLimit":8,"routeModes":["ground"]},
				{"id":"bc","fromNodeId":"b","toNodeId":"c","corridorId":"main","styleId":"asphalt","surfaceMode":"auto","shoulderWidth":0.3,"maxGrade":0.1,"elevationOffset":0,"direction":"both","speedLimit":8,"routeModes":["ground"]},
				{"id":"bd","fromNodeId":"b","toNodeId":"d","corridorId":"branch","styleId":"asphalt","surfaceMode":"auto","shoulderWidth":0.3,"maxGrade":0.1,"elevationOffset":0,"direction":"both","speedLimit":8,"routeModes":["ground"]}
			]
		}`)
		state, err := validateState(raw)
		if err != nil {
			t.Fatalf("validate junction state: %v", err)
		}
		if !strings.Contains(string(state), `"corridorId":"main"`) ||
			!strings.Contains(string(state), `"primaryEdgeIds":["ab","bc"]`) ||
			!strings.Contains(string(state), `"branchSide":"right"`) ||
			!strings.Contains(string(state), `"tangentTo":[1,0,0]`) {
			t.Fatalf("expected persisted junction relation, got %s", state)
		}
	})

	tests := map[string]string{
		"unknown field":     `{"nodes":[],"edges":[],"mesh":[]}`,
		"removed style":     strings.Replace(string(valid), `"styleId":"asphalt"`, `"styleId":"stone"`, 1),
		"missing node":      `{"nodes":[],"edges":[{"id":"e","fromNodeId":"a","toNodeId":"b","styleId":"asphalt","surfaceMode":"auto","shoulderWidth":0,"maxGrade":0.1,"direction":"both","speedLimit":1,"routeModes":["ground"]}]}`,
		"self loop":         `{"nodes":[{"id":"a","anchor":{"kind":"frame","frameId":"planet","localPoint":[0,0,0]},"tangentMode":"auto","width":1}],"edges":[{"id":"e","fromNodeId":"a","toNodeId":"a","styleId":"asphalt","surfaceMode":"auto","shoulderWidth":0,"maxGrade":0.1,"direction":"both","speedLimit":1,"routeModes":["ground"]}]}`,
		"invalid anchor":    `{"nodes":[{"id":"a","anchor":{"kind":"surface","surfaceId":"desk","surfacePoint":[0,0]},"tangentMode":"auto","width":1}],"edges":[]}`,
		"mixed anchor":      `{"nodes":[{"id":"a","anchor":{"kind":"surface","surfaceId":"desk","surfacePoint":[0,0,0],"side":"outside"},"tangentMode":"auto","width":1}],"edges":[]}`,
		"control id":        `{"nodes":[{"id":"a\u0000b","anchor":{"kind":"frame","frameId":"planet","localPoint":[0,0,0]},"tangentMode":"auto","width":1}],"edges":[]}`,
		"duplicate mode":    `{"nodes":[{"id":"a","anchor":{"kind":"frame","frameId":"planet","localPoint":[0,0,0]},"tangentMode":"auto","width":1},{"id":"b","anchor":{"kind":"frame","frameId":"planet","localPoint":[1,0,0]},"tangentMode":"auto","width":1}],"edges":[{"id":"e","fromNodeId":"a","toNodeId":"b","styleId":"asphalt","surfaceMode":"auto","shoulderWidth":0,"maxGrade":0.1,"direction":"both","speedLimit":1,"routeModes":["air","air"]}]}`,
		"invalid elevation": strings.Replace(string(valid), `"elevationOffset":0.75`, `"elevationOffset":1001`, 1),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := validateState(json.RawMessage(raw))
			if !bizerr.IsKind(err, bizerr.KindParameter) {
				t.Fatalf("expected parameter error, got %v", err)
			}
		})
	}
}

// 围栏配置必须同时命中模型和纹理白名单。
func TestValidateFence(t *testing.T) {
	if err := validateFence(&roadFence{ModelId: "brick-curb", MaterialId: "brick"}); err != nil {
		t.Fatalf("validate fence: %v", err)
	}
	for name, fence := range map[string]*roadFence{
		"model":    {ModelId: "scripted", MaterialId: "wood"},
		"material": {ModelId: "park-railing", MaterialId: "plastic"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateFence(fence); !bizerr.IsKind(err, bizerr.KindParameter) {
				t.Fatalf("expected parameter error, got %v", err)
			}
		})
	}
}

// 历史道路文档读取时只升级信封，原有拓扑保持不变。
func TestMapLegacyDocument(t *testing.T) {
	response := mapDocument(road_domain.Document{
		SchemaVersion: 1,
		Revision:      3,
		StateJson:     `{"nodes":[],"edges":[]}`,
		ContentHash:   strings.Repeat("0", 64),
	})
	if response.SchemaVersion != currentSchemaVersion {
		t.Fatalf("expected schema %d, got %d", currentSchemaVersion, response.SchemaVersion)
	}
	if string(response.State) != `{"nodes":[],"edges":[]}` {
		t.Fatalf("expected unchanged topology, got %s", response.State)
	}
	if response.ContentHash == strings.Repeat("0", 64) {
		t.Fatal("expected migrated state hash")
	}
}

// 旧道路只升级普通节点；门接口、拓扑和已升级文档的自定义宽度保持不变。
func TestMapDocumentRoadWidths(t *testing.T) {
	for _, version := range []int{1, 2, 3, 4} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			original := roadState{
				Nodes: []roadNode{
					{Id: "door", Anchor: roadAnchor{Kind: "connector", ConnectorId: "door:outside"}, Width: 0.8},
					{Id: "road", Anchor: roadAnchor{Kind: "surface", SurfaceId: "terrain", SurfacePoint: []float64{1, 0, 0}}, Width: 1.2},
					{Id: "bridge", Anchor: roadAnchor{Kind: "frame", FrameId: "world", LocalPoint: []float64{2, 0, 0}}, Width: 3.6},
				},
				Edges: []roadEdge{{Id: "edge", FromNodeId: "door", ToNodeId: "road"}},
			}
			if version == 3 {
				original.Nodes[1].Width = 2.6
			}
			state, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(state)
			document := road_domain.Document{SchemaVersion: version, Revision: 7, StateJson: string(state), ContentHash: hex.EncodeToString(hash[:])}
			response := mapDocument(document)
			var actual roadState
			if err := json.Unmarshal(response.State, &actual); err != nil {
				t.Fatal(err)
			}
			if version < 4 {
				original.Nodes[1].Width = 3
				original.Nodes[2].Width = 3
			}
			if !reflect.DeepEqual(actual, original) {
				t.Fatalf("unexpected road state: %+v", actual)
			}
			if response.SchemaVersion != 4 || response.Revision != 7 || document.StateJson != string(state) {
				t.Fatal("migration changed source or optimistic lock revision")
			}
			hash = sha256.Sum256(response.State)
			if response.ContentHash != hex.EncodeToString(hash[:]) {
				t.Fatal("migrated content hash mismatch")
			}
		})
	}
}
