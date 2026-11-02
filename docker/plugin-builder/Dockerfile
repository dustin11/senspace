ARG NODE_IMAGE=node:22-alpine
ARG NPM_REGISTRY=https://registry.npmmirror.com

# 从前端源码生成工具，首次检出时不依赖本地预生成文件。
FROM ${NODE_IMAGE} AS display-prebuilder
ARG NPM_REGISTRY=https://registry.npmmirror.com

WORKDIR /workspace/senspace-web
COPY senspace-web/package.json senspace-web/yarn.lock ./
# 只安装生成工具需要的依赖；Vite 提供前端锁定的 esbuild 工具链。
RUN node -e "const fs=require('fs'); const pkg=require('./package.json'); const names=['vite','typescript','three','meshoptimizer']; const dependencies={}; for (const name of names) dependencies[name]=(pkg.dependencies&&pkg.dependencies[name])||pkg.devDependencies[name]; fs.writeFileSync('package.json', JSON.stringify({private:true,scripts:{'prepare:display-prebuilder':pkg.scripts['prepare:display-prebuilder']},dependencies}, null, 2) + '\\n');" \
    && sed -i "s#https://registry.npmjs.org#${NPM_REGISTRY}#g; s#https://registry.yarnpkg.com#${NPM_REGISTRY}#g" yarn.lock \
    && yarn config set registry "${NPM_REGISTRY}" \
    && yarn install --frozen-lockfile --ignore-scripts --non-interactive --network-timeout 600000 --network-concurrency 4

COPY senspace-web/src ./src
# 只复制生成器及其直接依赖，不引入其余前端脚本。
COPY senspace-web/scripts/prepare-display-prebuilder.mjs \
     senspace-web/scripts/builtin-artifact-build-plugin.mjs \
     senspace-web/scripts/plugin-prebuild-source.mjs \
     senspace-web/scripts/plugin-artifact-compression.mjs \
     ./scripts/
COPY senspace-web/tsconfig.json ./
RUN mkdir -p ../senspace/docker/plugin-builder \
    && yarn prepare:display-prebuilder

FROM ${NODE_IMAGE} AS runtime

ARG NPM_REGISTRY=https://registry.npmmirror.com

WORKDIR /builder

COPY senspace/docker/plugin-builder/package.json ./
RUN npm config set registry "${NPM_REGISTRY}" \
    && npm install --omit=dev

COPY senspace/docker/plugin-builder/build-plugin.mjs ./
COPY --from=display-prebuilder /workspace/senspace/docker/plugin-builder/display-prebuilder.mjs ./

RUN addgroup -S app && adduser -S app -G app
USER app

ENTRYPOINT ["node", "/builder/build-plugin.mjs"]
