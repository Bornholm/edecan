FROM golang:1.25 AS build

RUN apt-get update \
  && apt-get install -y make git

RUN --mount=type=cache,target=/go/pkg/mod/ \
  --mount=type=bind,source=go.sum,target=go.sum \
  --mount=type=bind,source=go.mod,target=go.mod \
  go mod download -x

WORKDIR /src

COPY . /src

ARG GOCACHE=/root/.cache/go-build

RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=cache,target="/root/.cache/go-build" \
    --mount=type=cache,target=./tools \
    make goreleaser GORELEASER_ARGS="build --clean --auto-snapshot --single-target"

FROM alpine:3.23 AS runtime

RUN apk add \
    ca-certificates \
    tzdata \
  && update-ca-certificates

COPY --from=build /src/dist/edecan_linux_amd64_v1/edecan /usr/local/bin/edecan

# Cf. misc/docker/edecan.Dockerfile : config.yaml + base SQLite attendus dans /data.
VOLUME /data

EXPOSE 8080

CMD ["/usr/local/bin/edecan", "-config", "/data/config.yaml"]
