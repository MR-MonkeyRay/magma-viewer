# syntax=docker/dockerfile:1
ARG GO_IMAGE=golang:1.26
FROM ${GO_IMAGE} AS build
# 国内网络可构建时传入：--build-arg GOPROXY=https://goproxy.cn,direct
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags "-s -w" -o /out/magma-viewer . \
 && mkdir -p /out/tmp /out/data/images

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/magma-viewer /magma-viewer
# scratch 没有 /tmp，这里预建临时目录和存储目录
COPY --from=build --chmod=1777 /out/tmp /tmp
COPY --from=build /out/data /data
ENV CONFIG=/config/config.yaml TZ=Asia/Shanghai
EXPOSE 8080
VOLUME ["/data/images"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/magma-viewer", "healthcheck"]
ENTRYPOINT ["/magma-viewer"]
