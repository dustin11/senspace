package factory_service

import "strings"

// 单个插件的发布冻结与资产生成配置。
type pluginFactoryTooling struct {
	// 允许执行发布冻结的地址列表。
	FreezeOperators []string
	// 插件资产生成器配置；未配置表示不支持生成入口。
	Generator *pluginAssetGeneratorTooling
}

// 插件资产生成器配置。
type pluginAssetGeneratorTooling struct {
	// 生成器目录候选列表。
	DirCandidates []string
	// 测试生成写入的子目录名。
	TestDirName string
}

var pluginFactoryToolingRegistry = map[string]pluginFactoryTooling{
	"FishTank": {
		FreezeOperators: []string{
			"0xe9a51481d67ca775d19b02a220833ce6c4575f53",
		},
		Generator: &pluginAssetGeneratorTooling{
			DirCandidates: []string{
				"../senspace-web/src/components/StarSky/Desktop/Plugins/FishTank/fish-generator",
				"senspace-web/src/components/StarSky/Desktop/Plugins/FishTank/fish-generator",
			},
			TestDirName: "fish-test",
		},
	},
}

// 读取插件工具配置。
func pluginTooling(pluginId string) (pluginFactoryTooling, bool) {
	tooling, ok := pluginFactoryToolingRegistry[strings.TrimSpace(pluginId)]
	return tooling, ok
}
