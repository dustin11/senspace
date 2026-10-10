ARG NODE_IMAGE=node:22-alpine
ARG NPM_REGISTRY=https://registry.npmmirror.com
FROM ${NODE_IMAGE}

ARG NPM_REGISTRY=https://registry.npmmirror.com

WORKDIR /builder

COPY package.json ./
RUN npm config set registry "${NPM_REGISTRY}" \
    && npm install --omit=dev

COPY build-plugin.mjs ./
COPY display-prebuilder.mjs ./

RUN addgroup -S app && adduser -S app -G app
USER app

ENTRYPOINT ["node", "/builder/build-plugin.mjs"]
