ARG GO_IMAGE=golang:1.26.2-alpine
ARG NODE_IMAGE=node:22-alpine
ARG DOCKER_CLI_IMAGE=docker:28-cli
ARG API_RUNTIME_IMAGE=alpine:3.21

# 每次 API 构建直接从共享源生成目录，不依赖前端先构建或手工复制。
FROM ${NODE_IMAGE} AS terrain-catalog
WORKDIR /workspace
COPY senspace-web/scripts/build/sync-terrain-plant-catalog.mjs senspace-web/scripts/build/sync-terrain-plant-catalog.mjs
COPY senspace-web/src/components/StarSky/terrain/domain/schema/TerrainPlantCatalog.data.json senspace-web/src/components/StarSky/terrain/domain/schema/TerrainPlantCatalog.data.json
COPY senspace/go.mod senspace/go.mod
RUN node senspace-web/scripts/build/sync-terrain-plant-catalog.mjs --require-backend \
    && node senspace-web/scripts/build/sync-terrain-plant-catalog.mjs --check --require-backend

FROM ${GO_IMAGE} AS builder

ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /src

COPY senspace/go.mod senspace/go.sum ./
RUN go mod download

COPY senspace/ .
COPY --from=terrain-catalog /workspace/senspace/service/planet/terrain/plant_catalog.json service/planet/terrain/plant_catalog.json
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/senspace main.go

FROM ${DOCKER_CLI_IMAGE} AS dockercli

FROM ${API_RUNTIME_IMAGE}

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /out/senspace /app/senspace
COPY --from=builder /src/asset /app/asset
COPY --from=dockercli /usr/local/bin/docker /usr/local/bin/docker

ENV SENSPACE_ENV=uat

EXPOSE 9010

CMD ["/app/senspace"]
