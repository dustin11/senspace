package terrain

import (
	"encoding/json"
	"strings"
	"testing"

	terrain_domain "senspace/domain/planet/terrain"
	"senspace/pkg/bizerr"
)

// 覆盖 JSON 压缩、非法输入和记录上限。
func TestValidateState(t *testing.T) {
	t.Run("preserves reference mountain parts and material", func(t *testing.T) {
		for _, presetID := range []string{"rock-jointed-pillar", "mountain-canopy", "mountain-pine", "mountain-tree"} {
			source := strings.ReplaceAll(`{"platforms":[],"objects":[{"id":"rock","kind":"object","presetId":"PRESET","variantSeed":10788,"transform":{"position":[1,2,3],"rotation":[0,0.3,0],"scale":[3,8,4]}}],"prefabs":[{"id":"mountain","kind":"prefab","name":"云栈秋山","parts":[{"presetId":"PRESET","variantSeed":10788,"transform":{"position":[1,2,3],"rotation":[0,0.3,0],"scale":[3,8,4]}}]}]}`, "PRESET", presetID)
			state, err := validateState(json.RawMessage(source))
			if err != nil {
				t.Fatalf("validate %s: %v", presetID, err)
			}
			if strings.Count(string(state), `"presetId":"`+presetID+`"`) != 2 {
				t.Fatalf("lost rock part: %s", state)
			}
			if presetID == "rock-jointed-pillar" {
				source = strings.ReplaceAll(source, `"variantSeed":10788`, `"materialId":"warm-rockscape","variantSeed":10788`)
				state, err = validateState(json.RawMessage(source))
				if err != nil || strings.Count(string(state), `"materialId":"warm-rockscape"`) != 2 {
					t.Fatalf("lost warm stone material: %s, %v", state, err)
				}
			}
		}
	})
	t.Run("compacts a valid state", func(t *testing.T) {
		state, err := validateState(json.RawMessage(`{
			"platforms": [{
				"id":"p1",
				"kind":"platform",
				"materialId":"grass",
				"transform":{
					"position":[0,6,0],
					"rotation":[0,0,0],
					"scale":[5,1,5]
				},
				"heightField": {
					"version":2,
					"enabled":true,
					"baseHeight":0.006,
					"cellSize":0.5,
					"samplesPerChunk":64,
					"heightUnit":0.005,
					"zeroCode":32768,
					"chunks":[{"x":0,"z":0,"encoding":"constant","code":32868}]
				}
			}],
			"objects": [{
				"id":"o1",
				"kind":"object",
				"platformId":"p1",
				"presetId":"box",
				"materialId":"marble",
				"variantSeed":42,
				"transform":{
					"position":[1,6,1],
					"rotation":[0,0.5,0],
					"scale":[1,1,1]
				}
			}]
		}`))
		if err != nil {
			t.Fatalf("validate state: %v", err)
		}
		if strings.Contains(string(state), "\n") {
			t.Fatalf("expected compact JSON, got %q", string(state))
		}
		if !strings.Contains(string(state), `"materialId":"marble"`) {
			t.Fatalf("expected shape material, got %q", string(state))
		}
		if !strings.Contains(string(state), `"platformId":"p1"`) {
			t.Fatalf("expected platform attachment, got %q", string(state))
		}
	})

	t.Run("accepts every existing flower preset", func(t *testing.T) {
		_, err := validateState(json.RawMessage(`{
			"platforms":[],
			"objects":[{
				"id":"flower-1",
				"kind":"object",
				"presetId":"trumpet-flower",
				"variantSeed":1,
				"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]}
			}]
		}`))
		if err != nil {
			t.Fatalf("validate trumpet flower: %v", err)
		}
	})

	t.Run("rejects an unknown platform attachment", func(t *testing.T) {
		_, err := validateState(json.RawMessage(`{
			"platforms":[],
			"objects":[{
				"id":"o1",
				"kind":"object",
				"platformId":"missing-platform",
				"presetId":"rock",
				"variantSeed":1,
				"transform":{
					"position":[0,0,0],
					"rotation":[0,0,0],
					"scale":[1,1,1]
				}
			}]
		}`))
		if !bizerr.IsKind(err, bizerr.KindParameter) {
			t.Fatalf("expected parameter error, got %v", err)
		}
	})

	t.Run("rejects malformed height chunks", func(t *testing.T) {
		_, err := validateState(json.RawMessage(`{
			"platforms":[],
			"objects":[]
			"heightField":{
				"version":2,
				"enabled":true,
				"baseHeight":4.7,
				"cellSize":0.5,
				"samplesPerChunk":64,
				"heightUnit":0.005,
				"zeroCode":32768,
				"chunks":[{"x":0,"z":0,"encoding":"delta-rle-v1","data":"AQ=="}]
			}
		}`))
		if !bizerr.IsKind(err, bizerr.KindParameter) {
			t.Fatalf("expected parameter error, got %v", err)
		}
	})

	t.Run("accepts a complete compressed height chunk", func(t *testing.T) {
		_, err := validateState(json.RawMessage(`{
			"platforms":[{
				"id":"p1",
				"kind":"platform",
				"materialId":"grass",
				"transform":{"position":[0,6,0],"rotation":[0,0,0],"scale":[5,1,5]},
				"heightField":{
					"version":2,
					"enabled":true,
					"baseHeight":0.006,
					"cellSize":0.5,
					"samplesPerChunk":64,
					"heightUnit":0.005,
					"zeroCode":32768,
					"chunks":[{"x":0,"z":0,"encoding":"delta-rle-v1","data":"AcgB/x8A"}]
				}
				}],
				"objects":[]
			}`))
		if err != nil {
			t.Fatalf("validate compressed chunk: %v", err)
		}
	})

	t.Run("rejects legacy automatic flat height chunks", func(t *testing.T) {
		_, err := validateState(json.RawMessage(`{
			"platforms":[],
			"objects":[],
			"heightField":{
				"version":2,
				"enabled":true,
				"baseHeight":4.7,
				"cellSize":0.5,
				"samplesPerChunk":64,
				"heightUnit":0.005,
				"zeroCode":32768,
				"chunks":[{"x":0,"z":0,"encoding":"constant","code":32768}]
			}
		}`))
		if !bizerr.IsKind(err, bizerr.KindParameter) {
			t.Fatalf("expected parameter error, got %v", err)
		}
	})

	t.Run("accepts textures on independent object presets", func(t *testing.T) {
		state, err := validateState(json.RawMessage(`{
			"platforms":[],
			"objects":[{
				"id":"o1",
				"kind":"object",
				"presetId":"tulip-patch",
				"materialId":"marble",
				"variantSeed":1,
				"transform":{
					"position":[0,0,0],
					"rotation":[0,0,0],
					"scale":[1,1,1]
				}
			}]
		}`))
		if err != nil {
			t.Fatalf("validate independent object texture: %v", err)
		}
		if !strings.Contains(string(state), `"materialId":"marble"`) {
			t.Fatalf("expected object texture to persist, got %s", state)
		}
	})

	t.Run("accepts textures on basic shapes and detailed building parts", func(t *testing.T) {
		_, err := validateState(json.RawMessage(`{
			"platforms":[],
			"objects":[
				{"id":"shape","kind":"object","presetId":"frustum","materialId":"wood","variantSeed":1,"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]}},
				{"id":"detail","kind":"object","presetId":"pavilion-railing","materialId":"steel","variantSeed":2,"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]}}
			]
		}`))
		if err != nil {
			t.Fatalf("validate detailed presets: %v", err)
		}
	})

	t.Run("rejects unknown presets", func(t *testing.T) {
		_, err := validateState(json.RawMessage(`{
			"platforms":[],
			"objects":[{
				"id":"o1",
				"kind":"object",
				"presetId":"scripted-model",
				"variantSeed":1,
				"transform":{
					"position":[0,0,0],
					"rotation":[0,0,0],
					"scale":[1,1,1]
				}
			}]
		}`))
		if !bizerr.IsKind(err, bizerr.KindParameter) {
			t.Fatalf("expected parameter error, got %v", err)
		}
	})

	t.Run("rejects duplicate ids", func(t *testing.T) {
		_, err := validateState(json.RawMessage(`{
			"platforms":[{
				"id":"same",
				"kind":"platform",
				"materialId":"grass",
				"transform":{
					"position":[0,0,0],
					"rotation":[0,0,0],
					"scale":[1,1,1]
				}
			}],
			"objects":[{
				"id":"same",
				"kind":"object",
				"presetId":"rock",
				"variantSeed":1,
				"transform":{
					"position":[0,0,0],
					"rotation":[0,0,0],
					"scale":[1,1,1]
				}
			}]
		}`))
		if !bizerr.IsKind(err, bizerr.KindParameter) {
			t.Fatalf("expected parameter error, got %v", err)
		}
	})

	t.Run("rejects degenerate transforms", func(t *testing.T) {
		_, err := validateState(json.RawMessage(`{
			"platforms":[],
			"objects":[{
				"id":"o1",
				"kind":"object",
				"presetId":"rock",
				"variantSeed":1,
				"transform":{
					"position":[0,0,0],
					"rotation":[0,0,0],
					"scale":[1,0,1]
				}
			}]
		}`))
		if !bizerr.IsKind(err, bizerr.KindParameter) {
			t.Fatalf("expected parameter error, got %v", err)
		}
	})

	t.Run("rejects non-object state", func(t *testing.T) {
		_, err := validateState(json.RawMessage(`[]`))
		if !bizerr.IsKind(err, bizerr.KindParameter) {
			t.Fatalf("expected parameter error, got %v", err)
		}
	})

	t.Run("rejects too many platforms", func(t *testing.T) {
		platforms := make([]map[string]string, maxPlatforms+1)
		for index := range platforms {
			platforms[index] = map[string]string{"id": "p"}
		}
		raw, err := json.Marshal(map[string]interface{}{
			"platforms": platforms,
			"objects":   []interface{}{},
		})
		if err != nil {
			t.Fatalf("marshal state: %v", err)
		}
		_, err = validateState(raw)
		if !bizerr.IsKind(err, bizerr.KindParameter) {
			t.Fatalf("expected parameter error, got %v", err)
		}
	})

	t.Run("accepts assemblies and prefabs", func(t *testing.T) {
		state, err := validateState(json.RawMessage(`{
			"platforms":[],
			"objects":[
				{"id":"a","kind":"object","presetId":"box","variantSeed":1,"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]}},
				{"id":"b","kind":"object","presetId":"cylinder","variantSeed":2,"transform":{"position":[1,0,0],"rotation":[0,0,0],"scale":[1,1,1]}}
			],
			"assemblies":[{"id":"assembly-1","kind":"assembly","name":"凉亭","transform":{"position":[0.5,0,0],"rotation":[0,0,0],"scale":[1,1,1]},"memberIds":["a","b"]}],
			"prefabs":[{"id":"prefab-1","kind":"prefab","name":"凉亭","parts":[{"presetId":"pavilion-column","materialId":"jade","variantSeed":1,"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]}}]}]
		}`))
		if err != nil {
			t.Fatalf("validate assembly state: %v", err)
		}
		if !strings.Contains(string(state), `"assemblies":[`) || !strings.Contains(string(state), `"prefabs":[`) {
			t.Fatalf("expected v5 collections, got %s", state)
		}
	})

	t.Run("rejects material fields on assemblies", func(t *testing.T) {
		_, err := validateState(json.RawMessage(`{
			"platforms":[],
			"objects":[
				{"id":"a","kind":"object","presetId":"box","variantSeed":1,"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]}},
				{"id":"b","kind":"object","presetId":"arch","variantSeed":2,"transform":{"position":[1,0,0],"rotation":[0,0,0],"scale":[1,1,1]}}
			],
			"assemblies":[{"id":"assembly-1","kind":"assembly","name":"组合","materialId":"jade","transform":{"position":[0.5,0,0],"rotation":[0,0,0],"scale":[1,1,1]},"memberIds":["a","b"]}]
		}`))
		if !bizerr.IsKind(err, bizerr.KindParameter) {
			t.Fatalf("expected assembly material rejection, got %v", err)
		}
	})

	t.Run("rejects an object in multiple assemblies", func(t *testing.T) {
		_, err := validateState(json.RawMessage(`{
			"platforms":[],
			"objects":[
				{"id":"a","kind":"object","presetId":"box","variantSeed":1,"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]}},
				{"id":"b","kind":"object","presetId":"box","variantSeed":2,"transform":{"position":[1,0,0],"rotation":[0,0,0],"scale":[1,1,1]}},
				{"id":"c","kind":"object","presetId":"box","variantSeed":3,"transform":{"position":[2,0,0],"rotation":[0,0,0],"scale":[1,1,1]}}
			],
			"assemblies":[
				{"id":"assembly-1","kind":"assembly","name":"一","transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]},"memberIds":["a","b"]},
				{"id":"assembly-2","kind":"assembly","name":"二","transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]},"memberIds":["b","c"]}
			],
			"prefabs":[]
		}`))
		if !bizerr.IsKind(err, bizerr.KindParameter) {
			t.Fatalf("expected parameter error, got %v", err)
		}
	})
}

// 围栏配置必须同时命中模型和纹理白名单。
func TestValidateFence(t *testing.T) {
	if err := validateFence(&terrainFence{ModelId: "brick-curb", MaterialId: "brick"}); err != nil {
		t.Fatalf("validate fence: %v", err)
	}
	for name, fence := range map[string]*terrainFence{
		"model":    {ModelId: "scripted", MaterialId: "brick"},
		"material": {ModelId: "brick-wall", MaterialId: "plastic"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateFence(fence); !bizerr.IsKind(err, bizerr.KindParameter) {
				t.Fatalf("expected parameter error, got %v", err)
			}
		})
	}
}

// 植被发布必须同时校验宿主、分类归属、精确锚点和固定容量遮罩。
func TestValidateVegetationState(t *testing.T) {
	valid := json.RawMessage(`{
		"platforms":[],
		"objects":[{
			"id":"host-1","kind":"object","presetId":"box","variantSeed":1,
			"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]}
		}],
		"assemblies":[],"prefabs":[],
		"vegetationPatches":[{
			"id":"patch-1","kind":"vegetation-patch","presetId":"moss-patch",
			"categoryId":"moss-accent","seed":7,
			"surface":{"kind":"object","hostObjectId":"host-1"},
			"instances":[{
				"kind":"mesh","triangleIndex":0,"barycentric":[0.2,0.3,0.5],
				"fallbackLocalPoint":[0.5,0,0],"tangentRotation":0.4,
				"normalOffset":0.01,"scale":[0.8,0.8,0.8],"variantSeed":9
			}]
		}],
		"vegetationCoverageLayers":[{
			"id":"coverage-1","kind":"vegetation-coverage","categoryId":"moss-coverage",
			"surface":{"kind":"object","hostObjectId":"host-1"},"seed":11,
			"projection":"local-triplanar","tiles":[{
				"projectionAxis":"xz","tileX":0,"tileY":0,"resolution":128,
				"encoding":"predict-rle-base64","data":"gIAE/w=="
			}]
		}]
	}`)
	if _, err := validateState(valid); err != nil {
		t.Fatalf("validate vegetation state: %v", err)
	}

	t.Run("rejects category preset mismatch", func(t *testing.T) {
		invalid := strings.Replace(string(valid), `"categoryId":"moss-accent"`, `"categoryId":"flowers"`, 1)
		if _, err := validateState(json.RawMessage(invalid)); !bizerr.IsKind(err, bizerr.KindParameter) {
			t.Fatalf("expected parameter error, got %v", err)
		}
	})

	t.Run("rejects coverage decode length mismatch", func(t *testing.T) {
		invalid := strings.Replace(string(valid), `"data":"gIAE/w=="`, `"data":"Af8="`, 1)
		if _, err := validateState(json.RawMessage(invalid)); !bizerr.IsKind(err, bizerr.KindParameter) {
			t.Fatalf("expected parameter error, got %v", err)
		}
	})
}

// 确保历史文档公开读取时升级为平台自带高度场的结构。
func TestMapLegacyDocument(t *testing.T) {
	response := mapDocument(terrain_domain.Document{
		SchemaVersion: 1,
		Revision:      7,
		StateJson:     `{"platforms":[],"objects":[]}`,
		ContentHash:   strings.Repeat("0", 64),
	})
	if response.SchemaVersion != currentSchemaVersion {
		t.Fatalf("expected schema %d, got %d", currentSchemaVersion, response.SchemaVersion)
	}
	if string(response.State) != `{"assemblies":[],"objects":[],"platforms":[],"prefabs":[],"vegetationCoverageLayers":[],"vegetationPatches":[]}` {
		t.Fatalf("expected clean migrated state, got %s", response.State)
	}
	if response.ContentHash == strings.Repeat("0", 64) {
		t.Fatal("expected migrated state hash")
	}
}

// schema 7 的落叶数据在服务端无需改写字段，只升级信封版本供客户端执行语义迁移。
func TestMapLeafDistributionDocument(t *testing.T) {
	response := mapDocument(terrain_domain.Document{
		SchemaVersion: 7,
		Revision:      10,
		StateJson:     `{"platforms":[],"objects":[],"assemblies":[],"prefabs":[],"vegetationPatches":[],"vegetationCoverageLayers":[]}`,
		ContentHash:   strings.Repeat("0", 64),
	})
	if response.SchemaVersion != currentSchemaVersion {
		t.Fatalf("expected schema %d, got %d", currentSchemaVersion, response.SchemaVersion)
	}
	if response.ContentHash == strings.Repeat("0", 64) {
		t.Fatal("expected upgraded state hash")
	}
}

// 确保阶段三初版自动高度场读取时被整体清理。
func TestMapAutomaticHeightFieldDocument(t *testing.T) {
	response := mapDocument(terrain_domain.Document{
		SchemaVersion: 2,
		Revision:      8,
		StateJson: `{
			"platforms":[],
			"objects":[],
			"heightField":{
				"version":1,
				"enabled":true,
				"baseHeight":4.7,
				"cellSize":0.5,
				"samplesPerChunk":64,
				"heightUnit":0.005,
				"zeroCode":32768,
				"chunks":[{"x":0,"z":0,"encoding":"constant","code":32868}]
			}
		}`,
		ContentHash: strings.Repeat("0", 64),
	})
	if string(response.State) != `{"assemblies":[],"objects":[],"platforms":[],"prefabs":[],"vegetationCoverageLayers":[],"vegetationPatches":[]}` {
		t.Fatalf("expected removed unowned height field, got %s", response.State)
	}
	if response.ContentHash == strings.Repeat("0", 64) {
		t.Fatal("expected cleaned state hash")
	}
}

// schema 3 已经属于平台的高度场在增加围栏字段时必须完整保留。
func TestMapHeightFieldDocumentToFenceSchema(t *testing.T) {
	response := mapDocument(terrain_domain.Document{
		SchemaVersion: 3,
		Revision:      9,
		StateJson:     `{"platforms":[{"id":"p1","kind":"platform","materialId":"grass","transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]},"heightField":{"version":2,"enabled":true,"baseHeight":0.006,"cellSize":0.5,"samplesPerChunk":64,"heightUnit":0.005,"zeroCode":32768,"chunks":[{"x":0,"z":0,"encoding":"constant","code":32868}]}}],"objects":[]}`,
		ContentHash:   strings.Repeat("0", 64),
	})
	if response.SchemaVersion != currentSchemaVersion {
		t.Fatalf("expected schema %d, got %d", currentSchemaVersion, response.SchemaVersion)
	}
	if !strings.Contains(string(response.State), `"code":32868`) {
		t.Fatalf("expected preserved height field, got %s", response.State)
	}
	if response.ContentHash == strings.Repeat("0", 64) {
		t.Fatal("expected migrated state hash")
	}
}

// 确保地形发布只级联本次删除的平台，不影响仍存在的平台。
func TestFindRemovedPlatformIds(t *testing.T) {
	removed, err := findRemovedPlatformIds(
		json.RawMessage(`{"platforms":[{"id":"p1"},{"id":"p2"}],"objects":[]}`),
		json.RawMessage(`{"platforms":[{"id":"p2"},{"id":"p3"}],"objects":[]}`),
	)
	if err != nil {
		t.Fatalf("find removed platforms: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("expected one removed platform, got %v", removed)
	}
	if _, exists := removed["p1"]; !exists {
		t.Fatalf("expected p1 to be removed, got %v", removed)
	}
}
