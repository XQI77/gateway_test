# 多阶段构建 - 上游服务(hellosvr, businesssvr, zonesvr)
FROM golang:1.24-alpine AS builder

# 设置构建参数
ARG BUILD_DATE
ARG GIT_COMMIT
ARG VERSION=latest
ARG SERVICE_TYPE=upstream

# 安装构建依赖
RUN apk add --no-cache git ca-certificates tzdata

# 设置工作目录
WORKDIR /app

# 复制go.mod和go.sum文件，利用Docker缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 构建上游服务二进制文件
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.BuildDate=${BUILD_DATE} -X main.GitCommit=${GIT_COMMIT}" \
    -o /app/bin/upstream ./cmd/upstream

# 运行阶段 - 最小化镜像
FROM alpine:latest

# 设置服务类型环境变量
ARG SERVICE_TYPE=upstream
ENV SERVICE_TYPE=${SERVICE_TYPE}

# 安装运行时依赖
RUN apk --no-cache add ca-certificates tzdata curl

# 创建非root用户
RUN addgroup -g 1000 appgroup && \
    adduser -u 1000 -G appgroup -s /bin/sh -D appuser

# 设置工作目录
WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /app/bin/upstream ./upstream

# 修改文件权限
RUN chown -R appuser:appgroup /app && \
    chmod +x /app/upstream

# 切换到非root用户
USER appuser

# 暴露端口 (gRPC服务端口和健康检查端口)
EXPOSE 8081 8082 8083 9081 9082 9083

# 健康检查脚本 (使用健康检查端口 = 服务端口+1000)
HEALTHCHECK --interval=30s --timeout=10s --start-period=40s --retries=3 \
    CMD ./upstream -addr ":${SERVICE_PORT:-8081}" -health-check 2>/dev/null || exit 1

# 设置标签
LABEL maintainer="gatesvr-team" \
      version="${VERSION}" \
      description="gatesvr上游服务器 (${SERVICE_TYPE})" \
      build-date="${BUILD_DATE}" \
      git-commit="${GIT_COMMIT}" \
      service-type="${SERVICE_TYPE}"

# 启动命令 (可以通过环境变量SERVICE_PORT覆盖端口)
ENTRYPOINT ["./upstream"]
CMD ["-addr", ":8081"]