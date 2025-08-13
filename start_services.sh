#!/bin/bash

# Gateway服务启动脚本
# 按顺序启动hellosvr, businesssvr, zonesvr和gatesvr

set -e  # 如果任何命令失败则退出

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 日志函数
log() {
    echo -e "${GREEN}[$(date +'%Y-%m-%d %H:%M:%S')] $1${NC}"
}

error() {
    echo -e "${RED}[$(date +'%Y-%m-%d %H:%M:%S')] ERROR: $1${NC}"
}

warn() {
    echo -e "${YELLOW}[$(date +'%Y-%m-%d %H:%M:%S')] WARNING: $1${NC}"
}

# 检查Go是否安装
check_go() {
    if ! command -v go &> /dev/null; then
        error "Go未安装，请先安装Go语言环境"
        exit 1
    fi
    log "Go版本: $(go version)"
}

# 检查端口是否被占用
check_port() {
    local port=$1
    local service=$2
    if lsof -Pi :$port -sTCP:LISTEN -t >/dev/null 2>&1; then
        warn "端口 $port 已被占用 ($service)"
        return 1
    fi
    return 0
}

# 等待服务进程启动（不依赖端口检查）
wait_for_service() {
    local pid=$1
    local service=$2
    local max_wait=${3:-10}
    local count=0
    
    log "等待 $service 进程启动..."
    while [ $count -lt $max_wait ]; do
        if ps -p $pid > /dev/null 2>&1; then
            log "$service 进程已启动 (PID: $pid)"
            return 0
        fi
        sleep 1
        count=$((count + 1))
    done
    
    error "$service 进程启动失败或已退出 (PID: $pid)"
    return 1
}

# 启动上游服务
start_upstream_service() {
    local zone=$1
    local port=$2
    local service_name=$3
    
    log "启动 $service_name (Zone $zone, 端口 $port)..."
    
    # 检查端口是否已被占用
    if ! check_port $port "$service_name"; then
        error "$service_name 端口 $port 已被占用"
        return 1
    fi
    
    # 启动服务
    nohup go run ./cmd/upstream --zone=$zone --addr=:$port --gateway=localhost:8092 > logs/${service_name}.log 2>&1 &
    local pid=$!
    echo $pid > pids/${service_name}.pid
    
    # 等待服务进程启动
    if wait_for_service $pid "$service_name" 10; then
        log "$service_name 启动成功 (PID: $pid, 端口: $port)"
        return 0
    else
        error "$service_name 启动失败"
        kill $pid 2>/dev/null || true
        rm -f pids/${service_name}.pid
        return 1
    fi
}

# 启动网关服务
start_gateway() {
    log "启动网关服务..."
    
    # 检查配置文件
    if [ ! -f "test-config.yaml" ]; then
        error "配置文件 test-config.yaml 不存在"
        return 1
    fi
    
    # 检查端口
    if ! check_port 8080 "Gateway HTTP"; then
        error "网关HTTP端口 8080 已被占用"
        return 1
    fi
    
    if ! check_port 8092 "Gateway GRPC"; then
        error "网关GRPC端口 8092 已被占用"
        return 1
    fi
    
    if ! check_port 8453 "Gateway QUIC"; then
        error "网关QUIC端口 8453 已被占用"
        return 1
    fi
    
    # 启动网关
    nohup go run ./cmd/gatesvr -config=test-config.yaml > logs/gatesvr.log 2>&1 &
    local pid=$!
    echo $pid > pids/gatesvr.pid
    
    # 等待网关进程启动
    if wait_for_service $pid "Gateway" 15; then
        log "网关服务启动成功 (PID: $pid)"
        log "HTTP端口: 8080, GRPC端口: 8092, QUIC端口: 8453"
        return 0
    else
        error "网关服务启动失败"
        kill $pid 2>/dev/null || true
        rm -f pids/gatesvr.pid
        return 1
    fi
}

# 停止所有服务
stop_services() {
    log "停止所有服务..."
    
    # 停止网关
    if [ -f "pids/gatesvr.pid" ]; then
        local pid=$(cat pids/gatesvr.pid)
        if kill $pid 2>/dev/null; then
            log "网关服务已停止 (PID: $pid)"
        fi
        rm -f pids/gatesvr.pid
    fi
    
    # 停止上游服务
    for service in hellosvr businesssvr zonesvr; do
        if [ -f "pids/${service}.pid" ]; then
            local pid=$(cat pids/${service}.pid)
            if kill $pid 2>/dev/null; then
                log "${service} 已停止 (PID: $pid)"
            fi
            rm -f pids/${service}.pid
        fi
    done
}

# 检查服务状态
check_services() {
    log "检查服务状态..."
    
    local all_running=true
    
    # 检查上游服务
    for port in 8081 8082 8083; do
        case $port in
            8081) service="hellosvr" ;;
            8082) service="businesssvr" ;;
            8083) service="zonesvr" ;;
        esac
        
        if lsof -Pi :$port -sTCP:LISTEN -t >/dev/null 2>&1; then
            log "$service 运行中 (端口 $port)"
        else
            error "$service 未运行 (端口 $port)"
            all_running=false
        fi
    done
    
    # 检查网关服务
    if lsof -Pi :8080 -sTCP:LISTEN -t >/dev/null 2>&1; then
        log "网关服务运行中 (HTTP端口 8080)"
    else
        error "网关服务未运行 (HTTP端口 8080)"
        all_running=false
    fi
    
    if lsof -Pi :8092 -sTCP:LISTEN -t >/dev/null 2>&1; then
        log "网关服务运行中 (GRPC端口 8092)"
    else
        error "网关服务未运行 (GRPC端口 8092)"
        all_running=false
    fi
    
    if $all_running; then
        log "所有服务运行正常"
    else
        error "部分服务未正常运行"
        return 1
    fi
}

# 主函数
main() {
    case "${1:-start}" in
        start)
            log "开始启动所有服务..."
            
            # 检查Go环境
            check_go
            
            # 创建必要的目录
            mkdir -p logs pids
            
            # 启动上游服务
            log "启动上游服务..."
            start_upstream_service "hello" 8081 "hellosvr" || exit 1
            sleep 2
            
            start_upstream_service "business" 8082 "businesssvr" || exit 1
            sleep 2
            
            start_upstream_service "zone" 8083 "zonesvr" || exit 1
            sleep 2
            
            # 启动网关服务
            start_gateway || exit 1
            
            log "所有服务启动完成！"
            log "网关HTTP端口: 8080"
            log "网关GRPC端口: 8092" 
            log "网关QUIC端口: 8453"
            log "监控端口: 9090"
            log ""
            log "使用 './start_services.sh status' 检查服务状态"
            log "使用 './start_services.sh stop' 停止所有服务"
            ;;
            
        stop)
            stop_services
            ;;
            
        restart)
            log "重启所有服务..."
            stop_services
            sleep 3
            $0 start
            ;;
            
        status)
            check_services
            ;;
            
        *)
            echo "用法: $0 {start|stop|restart|status}"
            echo ""
            echo "命令说明:"
            echo "  start   - 启动所有服务 (默认)"
            echo "  stop    - 停止所有服务"
            echo "  restart - 重启所有服务"  
            echo "  status  - 检查服务状态"
            exit 1
            ;;
    esac
}

# 设置信号处理器
trap 'stop_services; exit' INT TERM

# 执行主函数
main "$@"