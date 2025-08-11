# 多阶段构建 - gatesvr网关服务
FROM golang:1.24-alpine AS builder

# 设置构建参数
ARG BUILD_DATE
ARG GIT_COMMIT
ARG VERSION=latest

# 安装构建依赖
RUN apk add --no-cache git ca-certificates tzdata

# 设置工作目录
WORKDIR /app

# 复制go.mod和go.sum文件，利用Docker缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 构建gatesvr二进制文件
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.BuildDate=${BUILD_DATE} -X main.GitCommit=${GIT_COMMIT}" \
    -o /app/bin/gatesvr ./cmd/gatesvr

# 运行阶段 - 最小化镜像
FROM alpine:latest

# 安装运行时依赖
RUN apk --no-cache add ca-certificates tzdata wget curl

# 创建非root用户
RUN addgroup -g 1000 appgroup && \
    adduser -u 1000 -G appgroup -s /bin/sh -D appuser

# 设置工作目录
WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /app/bin/gatesvr ./gatesvr

# 复制证书文件
COPY --from=builder /app/certs ./certs/

# 创建配置文件目录
RUN mkdir -p /app/config

# 修改文件权限
RUN chown -R appuser:appgroup /app && \
    chmod +x /app/gatesvr

# 切换到非root用户
USER appuser

# 暴露端口
EXPOSE 8080 8092 8453 9090

# 健康检查
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD curl -f http://localhost:8080/health

# 设置标签
LABEL maintainer="gatesvr-team" \
      version="${VERSION}" \
      description="gatesvr网关服务器" \
      build-date="${BUILD_DATE}" \
      git-commit="${GIT_COMMIT}"

# 启动命令
ENTRYPOINT ["./gatesvr"]
CMD ["-config", "docker-config.yaml"]