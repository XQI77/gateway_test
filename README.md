# GateSvr - 高性能QUIC网关服务器

![Go Version](https://img.shields.io/badge/go-1.23+-blue.svg)
![License](https://img.shields.io/badge/license-MIT-green.svg)
![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS%20%7C%20Windows-lightgrey.svg)

GateSvr 是一个基于 QUIC 协议的高性能网关服务器，专为大规模实时通信系统设计。它提供了可靠的消息传递、智能负载均衡、故障转移和高可用性支持。

## 🚀 主要特性

### 核心功能
- **QUIC 协议支持**: 基于 QUIC 的高性能传输层，提供低延迟和可靠连接
- **智能会话管理**: 支持会话重连、消息确认和超时处理
- **多协议支持**: 同时支持 QUIC、HTTP 和 gRPC 协议
- **消息序列化**: 基于 Protocol Buffers 的高效消息编解码

### 高可用性
- **主备热切换**: 支持 Primary/Backup 模式的自动故障转移
- **数据同步**: 实时同步会话状态和消息队列
- **健康监控**: 完整的监控指标和健康检查机制
- **过载保护**: 多层次过载保护，包括连接数、QPS 和上游并发控制

### 分区路由
- **Zone 路由**: 基于用户 OpenID 的智能分区路由（支持6个Zone：001-006）
- **动态负载均衡**: 上游服务自动注册和健康检查
- **广播支持**: 支持单播和广播消息推送

## 📋 目录结构

```
onlygate/
├── cmd/gatesvr/           # 主程序入口
│   └── main.go
├── internal/              # 内部模块
│   ├── backup/           # 备份和高可用模块
│   ├── config/           # 配置管理
│   ├── gateway/          # 网关核心逻辑
│   ├── message/          # 消息编解码
│   ├── session/          # 会话管理
│   └── upstream/         # 上游服务管理
├── proto/                # Protocol Buffers定义
│   ├── message.proto     # 客户端消息协议
│   └── upstream.proto    # 上游服务协议
├── pkg/metrics/          # 监控指标
├── certs/                # TLS证书
├── test-config.yaml      # 测试配置文件
├── Dockerfile           # Docker构建文件
├── Makefile            # 构建脚本
└── README.md           # 项目文档
```

## 🛠️ 快速开始

### 环境要求

- **Go**: 1.23.0 或更高版本
- **Protocol Buffers**: 用于消息序列化
- **OpenSSL**: 用于生成TLS证书（可选）

### 安装步骤

1. **克隆项目**
```bash
git clone <repository-url>
cd onlygate
```

2. **安装依赖**
```bash
make deps
```

3. **生成开发证书**
```bash
make certs
```

4. **生成 Protocol Buffers 文件**
```bash
make proto
```

5. **编译项目**
```bash
make build
```

### 运行服务

1. **使用默认配置运行**
```bash
make run
```

2. **使用指定配置文件运行**
```bash
make run-config
# 或者
./build/gatesvr -config=test-config.yaml
```

3. **使用 Docker 运行**
```bash
make docker
make docker-run
```

## ⚙️ 配置说明

### 基础配置示例

```yaml
server:
  quic_addr: ":8453"          # QUIC服务端口
  http_addr: ":8080"          # HTTP API端口
  grpc_addr: ":8092"          # gRPC服务端口
  metrics_addr: ":9090"       # 监控指标端口
  cert_file: "certs/server.crt"
  key_file: "certs/server.key"
  session_timeout: "5m"
  ack_timeout: "30s"
  max_retries: 3

# 过载保护配置
overload_protection:
  enabled: true
  max_connections: 1000
  max_qps: 2000
  qps_window_seconds: 10
  max_upstream_concurrent: 100
  upstream_timeout: "30s"

# 备份配置（高可用）
backup:
  enabled: false            # 启用备份功能
  mode: "primary"          # primary 或 backup
  server_id: "gate-001"
  heartbeat:
    interval: "2s"
    peer_addr: "backup-server:8093"
    timeout: "6s"
  sync:
    peer_addr: "backup-server:8094"
    batch_size: 50
    timeout: "200ms"
    buffer_size: 1000
```

### Zone 路由配置

系统支持基于 OpenID 的智能分区路由：

- **Zone 001**: OpenID Hash % 6 == 0
- **Zone 002**: OpenID Hash % 6 == 1  
- **Zone 003**: OpenID Hash % 6 == 2
- **Zone 004**: OpenID Hash % 6 == 3
- **Zone 005**: OpenID Hash % 6 == 4
- **Zone 006**: OpenID Hash % 6 == 5

上游服务启动示例：
```bash
./upstream --zone=001 --addr=localhost:9001 --gateway=localhost:8092
./upstream --zone=002 --addr=localhost:9002 --gateway=localhost:8092
# ... 更多Zone
```

## 🔌 协议接口

### 客户端消息协议

#### 连接建立
```protobuf
message StartRequest {
  string openid = 1;            # 用户唯一标识
  string auth_token = 2;        # 认证令牌
  uint64 last_acked_seq_id = 3; # 断线重连时的序列号
}
```

#### 心跳消息
```protobuf
message HeartbeatRequest {
  int64 client_timestamp = 1;   # 客户端时间戳
}
```

#### 业务消息
```protobuf
message ClientRequest {
  uint32 msg_id = 1;           # 消息ID
  uint64 seq_id = 2;           # 序列号
  RequestType type = 3;        # 消息类型
  bytes payload = 4;           # 消息载荷
  string openid = 6;           # 客户端标识
}
```

### 上游服务协议

#### 服务注册
```protobuf
message UpstreamRegisterRequest {
  string address = 1;        # 服务地址 "ip:port"
  string zone_id = 2;        # Zone ID "001"-"006"
  string service_name = 3;   # 服务名称
}
```

#### 单播推送
```protobuf
message UnicastPushRequest {
  string target_type = 1;      # 目标类型: session, openid
  string target_id = 2;        # 目标标识符
  string msg_type = 3;         # 消息类型
  string content = 5;          # 推送内容
  bytes data = 6;              # 附加数据
}
```

## 🔍 监控和指标

### HTTP API 端点

- **健康检查**: `GET /health`
- **会话统计**: `GET /stats`
- **性能监控**: `GET /performance`
- **调试信息**: `GET /debug/pprof/`

### Prometheus 指标

访问 `http://localhost:9090/metrics` 获取完整的监控指标，包括：

- `gatesvr_active_connections` - 活跃连接数
- `gatesvr_total_requests` - 总请求数
- `gatesvr_request_duration` - 请求处理时间
- `gatesvr_upstream_requests` - 上游请求数
- `gatesvr_message_queue_size` - 消息队列大小

## 🚧 开发工具

### 可用的 Make 目标

```bash
# 开发相关
make setup          # 设置开发环境
make fmt            # 格式化代码
make vet            # 静态分析
make lint           # 代码检查（需要 golangci-lint）

# 构建和测试
make build          # 构建二进制文件
make build-all      # 多平台构建
make test           # 运行测试
make test-cover     # 测试覆盖率
make bench          # 性能基准测试

# Protocol Buffers
make proto          # 生成 protobuf 文件

# 部署相关
make docker         # 构建 Docker 镜像
make certs          # 生成开发证书
make clean          # 清理构建文件
```

### 开发工具安装

```bash
make tools          # 安装开发工具
```

这将安装：
- `protoc-gen-go` - Protocol Buffers Go 插件
- `protoc-gen-go-grpc` - gRPC Go 插件  
- `golangci-lint` - Go 代码检查工具

## 🐳 Docker 部署

### 构建镜像

```bash
# 构建镜像
make docker

# 或者手动构建
docker build -t gatesvr:latest .
```

### 运行容器

```bash
# 使用 make 快速运行
make docker-run

# 或者手动运行
docker run -d --name gatesvr \
  -p 8453:8453 \
  -p 8080:8080 \
  -p 8092:8092 \
  -p 9090:9090 \
  gatesvr:latest
```

### 环境变量配置

容器支持通过环境变量覆盖配置：

```bash
docker run -d \
  -e GATESVR_QUIC_ADDR=":8453" \
  -e GATESVR_HTTP_ADDR=":8080" \
  -e GATESVR_UPSTREAM_ADDR="upstream:8082" \
  gatesvr:latest
```

## 🏗️ 架构设计

### 系统架构图

```
┌─────────────────┐    QUIC/TLS    ┌─────────────────┐
│   QUIC Client   │◄──────────────►│    GateSvr      │
└─────────────────┘                │                 │
                                   │  ┌─────────────┐│
┌─────────────────┐    HTTP/gRPC   │  │Session Mgr  ││
│ Monitor Client  │◄──────────────►│  └─────────────┘│
└─────────────────┘                │  ┌─────────────┐│
                                   │  │Backup Mgr   ││
┌─────────────────┐    gRPC        │  └─────────────┘│
│ Upstream Svc 1  │◄──────────────►│  ┌─────────────┐│
│   (Zone 001)    │                │  │Zone Router  ││
└─────────────────┘                │  └─────────────┘│
┌─────────────────┐                └─────────────────┘
│ Upstream Svc 2  │◄──────────────┐
│   (Zone 002)    │                │
└─────────────────┘                │
        ...                        │
┌─────────────────┐                │
│ Upstream Svc 6  │◄──────────────┘
│   (Zone 006)    │
└─────────────────┘
```

### 关键组件

1. **会话管理器**: 维护客户端连接状态和消息队列
2. **Zone 路由器**: 基于 OpenID 的智能路由分发
3. **备份管理器**: 提供主备切换和数据同步
4. **过载保护器**: 多维度的系统保护机制
5. **监控系统**: 完整的指标收集和健康检查

## 📚 API 文档

### 客户端集成

客户端需要实现以下消息流程：

1. **建立连接**: 发送 `StartRequest` 消息
2. **认证确认**: 接收 `StartResponse` 确认连接成功
3. **心跳维持**: 定期发送 `HeartbeatRequest` 保持连接活跃
4. **业务交互**: 发送 `BusinessRequest` 处理业务逻辑
5. **消息确认**: 对收到的推送消息发送 `ClientAck`
6. **优雅断开**: 发送 `StopRequest` 主动关闭连接

### 上游服务集成

上游服务需要实现：

1. **服务注册**: 调用 `RegisterUpstream` 注册到网关
2. **请求处理**: 实现 `ProcessRequest` 处理业务请求  
3. **消息推送**: 调用 `PushToClient` 推送消息到客户端
4. **广播消息**: 调用 `BroadcastToClients` 广播消息
5. **健康检查**: 实现 `GetStatus` 提供服务状态

## 🔧 故障排查

### 常见问题

1. **连接建立失败**
   - 检查 TLS 证书是否正确
   - 确认端口未被占用
   - 验证防火墙设置

2. **消息推送失败**  
   - 检查上游服务是否正常注册
   - 验证 OpenID 路由配置
   - 查看会话管理器状态

3. **性能问题**
   - 启用过载保护配置
   - 调整连接池大小
   - 监控系统资源使用

### 日志配置

系统支持日志轮转，默认配置：
- 日志文件：`gatesvr.log`
- 最大大小：3MB
- 保留文件：5个

查看实时日志：
```bash
tail -f gatesvr.log
```

## 🤝 贡献指南

1. Fork 项目
2. 创建功能分支 (`git checkout -b feature/AmazingFeature`)
3. 提交更改 (`git commit -m 'Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 打开 Pull Request

## 📄 许可证

本项目采用 MIT 许可证 - 详情请查看 [LICENSE](LICENSE) 文件。

## 📞 支持

如有问题或建议，请通过以下方式联系：

- 提交 Issue: [GitHub Issues](https://github.com/your-org/gatesvr/issues)
- 邮件支持: gatesvr-support@example.com
- 文档Wiki: [项目Wiki](https://github.com/your-org/gatesvr/wiki)

---

**注意**: 这是一个生产级别的网关服务器，建议在部署到生产环境前进行充分的测试和性能调优。