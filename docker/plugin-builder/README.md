# 插件构建镜像

在同时包含 `senspace`、`senspace-web` 的工作区根目录构建：

```sh
docker build -f senspace/docker/plugin-builder/Dockerfile -t senspace/plugin-builder:latest .
```

镜像先安装前端锁定依赖并执行 `yarn prepare:display-prebuilder`，再将生成工具复制到运行阶段。部署脚本和 ldev 共用此流程，Docker 缓存负责复用未变化的构建步骤。

构建阶段只复制以下四个前端脚本。最终阶段只接收生成工具，不复制前端源码目录、脚本目录或构建阶段的 `node_modules`；需要的共享运行时代码已打包在生成工具中：

- `prepare-display-prebuilder.mjs`：打包生成工具。
- `builtin-artifact-build-plugin.mjs`：生成时替换浏览器产物索引，避免依赖已有产物。
- `plugin-prebuild-source.mjs`：静态识别插件入口的 `prebuild()`。
- `plugin-artifact-compression.mjs`：生成插件显示资源的 gzip、Brotli 分发副本。

`display-prebuilder.mjs` 不纳入 Git。需要本地运行工具时，在前端目录执行 `yarn prepare:display-prebuilder`；本地文件无需复制进镜像构建上下文。

它是后端插件发布使用的共享预构建工具，封装插件 SDK、Three.js、TypeScript 静态解析及 CPU 几何编译能力。`build-plugin.mjs` 使用它识别插件静态预构建入口，执行 `prebuild()`，生成显示几何包、首显入口和压缩副本，再写入该插件版本的发布目录与清单。没有静态预构建入口的插件只正常打包，不执行入口模块。

它不收集所有插件的实现或模型数据，新增插件通常不会使这个文件变大；增加的是该插件自己的代码包与显示资源。工具的体积主要来自 TypeScript 解析器、Three.js 及共享框架代码，只有共享 SDK、预构建逻辑或打包依赖发生变化时，体积才可能变化。
