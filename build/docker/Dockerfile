# syntax=docker/dockerfile:1

# 构建阶段固定跑在构建机架构上，交叉编译交给 GOOS/GOARCH。
ARG GO_IMAGE=golang:1.27-alpine
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
# 国内网络可传入 --build-arg GOPROXY=https://goproxy.cn,direct
ARG GOPROXY=https://proxy.golang.org,direct
ARG TARGETOS
ARG TARGETARCH
ENV GOPROXY=${GOPROXY} CGO_ENABLED=0
WORKDIR /src

# 依赖层与源码层分离：go.mod/go.sum 未变时直接复用依赖层。
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

# 编译缓存挂载不进入镜像；空 buildid 让产物可复现。
RUN --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/magma-viewer . \
 && mkdir -p /out/tmp /out/data/images

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/magma-viewer /magma-viewer
# scratch 无 /tmp，预建临时目录和存储目录
COPY --from=build --chmod=1777 /out/tmp /tmp
COPY --from=build /out/data /data
ENV CONFIG=/config/config.yaml TZ=Asia/Shanghai
EXPOSE 8080
VOLUME ["/data/images"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/magma-viewer", "healthcheck"]
ENTRYPOINT ["/magma-viewer"]