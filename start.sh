#!/bin/bash

# 网关测试启动脚本
# 用法: ./start.sh [start|stop|restart|status]

set -e

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_DIR="$PROJECT_ROOT/logs"

# 创建日志目录
mkdir -p "$LOG_DIR"

# 定义服务配置
declare -A SERVICES=(
    ["hello"]="8081"
    ["business"]="8082" 
    ["zone"]="8083"
)

GATEWAY_PORT="8453"
CONFIG_FILE="$PROJECT_ROOT/test-config.yaml"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[$(date '+%Y-%m-%d %H:%M:%S')]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[$(date '+%Y-%m-%d %H:%M:%S')] WARNING:${NC} $1"
}

error() {
    echo -e "${RED}[$(date '+%Y-%m-%d %H:%M:%S')] ERROR:${NC} $1"
}

# 检查端口是否被占用
check_port() {
    local port=$1
    if netstat -tlnp 2>/dev/null | grep -q ":$port "; then
        return 0  # 端口被占用
    else
        return 1  # 端口空闲
    fi
}

# 等待端口启动
wait_for_port() {
    local port=$1
    local timeout=${2:-10}
    local count=0
    
    log "等待端口 $port 启动..."
    while [ $count -lt $timeout ]; do
        if check_port $port; then
            log "端口 $port 已启动"
            return 0
        fi
        sleep 1
        count=$((count + 1))
    done
    
    error "端口 $port 启动超时"
    return 1
}

# 构建项目
build_project() {
    log "构建项目..."
    cd "$PROJECT_ROOT"
    
    # 构建上游服务
    if ! go build -o upstream ./cmd/upstream; then
        error "构建上游服务失败"
        exit 1
    fi
    
    # 构建网关服务
    if ! go build -o gatesvr ./cmd/gatesvr; then
        error "构建网关服务失败"
        exit 1
    fi
    
    log "项目构建完成"
}

# 启动上游服务
start_upstream() {
    local service_name=$1
    local port=$2
    local log_file="$LOG_DIR/upstream_${service_name}.log"
    
    if check_port $port; then
        warn "端口 $port 已被占用，跳过启动 $service_name 服务"
        return 0
    fi
    
    log "启动上游服务: $service_name (端口: $port)"
    cd "$PROJECT_ROOT"
    
    # 启动上游服务并重定向日志
    nohup ./upstream -port=$port -service=$service_name > "$log_file" 2>&1 &
    local pid=$!
    
    # 保存PID
    echo $pid > "$LOG_DIR/upstream_${service_name}.pid"
    
    # 等待服务启动
    if wait_for_port $port 10; then
        log "上游服务 $service_name 启动成功 (PID: $pid)"
    else
        error "上游服务 $service_name 启动失败"
        return 1
    fi
}

# 启动网关服务
start_gateway() {
    local log_file="$LOG_DIR/gateway.log"
    
    if check_port $GATEWAY_PORT; then
        warn "网关端口 $GATEWAY_PORT 已被占用"
        return 1
    fi
    
    log "启动网关服务..."
    cd "$PROJECT_ROOT"
    
    # 启动网关并重定向日志
    nohup ./gatesvr -config="$CONFIG_FILE" > "$log_file" 2>&1 &
    local pid=$!
    
    # 保存PID
    echo $pid > "$LOG_DIR/gateway.pid"
    
    # 等待网关启动
    if wait_for_port $GATEWAY_PORT 15; then
        log "网关服务启动成功 (PID: $pid)"
        log "QUIC地址: :$GATEWAY_PORT"
        log "HTTP API: :8080"
        log "gRPC地址: :8092"
        log "监控地址: :9090"
    else
        error "网关服务启动失败"
        return 1
    fi
}

# 停止服务
stop_service() {
    local pid_file=$1
    local service_name=$2
    
    if [ -f "$pid_file" ]; then
        local pid=$(cat "$pid_file")
        if ps -p $pid > /dev/null 2>&1; then
            log "停止服务: $service_name (PID: $pid)"
            kill $pid
            
            # 等待进程结束
            local count=0
            while ps -p $pid > /dev/null 2>&1 && [ $count -lt 10 ]; do
                sleep 1
                count=$((count + 1))
            done
            
            # 如果进程还在运行，强制杀死
            if ps -p $pid > /dev/null 2>&1; then
                warn "强制停止服务: $service_name"
                kill -9 $pid
            fi
        fi
        rm -f "$pid_file"
    fi
}

# 启动所有服务
start_all() {
    log "开始启动所有服务..."
    
    # 构建项目
    build_project
    
    # 启动上游服务
    for service_name in "${!SERVICES[@]}"; do
        start_upstream "$service_name" "${SERVICES[$service_name]}"
    done
    
    # 等待一下确保上游服务完全启动
    sleep 2
    
    # 启动网关
    if start_gateway; then
        log "所有服务启动完成！"
        echo
        log "服务状态:"
        status_all
    else
        error "网关启动失败，停止所有服务"
        stop_all
        exit 1
    fi
}

# 停止所有服务
stop_all() {
    log "停止所有服务..."
    
    # 停止网关
    stop_service "$LOG_DIR/gateway.pid" "网关服务"
    
    # 停止上游服务
    for service_name in "${!SERVICES[@]}"; do
        stop_service "$LOG_DIR/upstream_${service_name}.pid" "上游服务($service_name)"
    done
    
    log "所有服务已停止"
}

# 查看服务状态
status_all() {
    echo
    echo -e "${BLUE}=== 服务状态 ===${NC}"
    
    # 检查上游服务状态
    for service_name in "${!SERVICES[@]}"; do
        local port="${SERVICES[$service_name]}"
        local pid_file="$LOG_DIR/upstream_${service_name}.pid"
        
        if [ -f "$pid_file" ]; then
            local pid=$(cat "$pid_file")
            if ps -p $pid > /dev/null 2>&1; then
                echo -e "上游服务($service_name): ${GREEN}运行中${NC} (PID: $pid, 端口: $port)"
            else
                echo -e "上游服务($service_name): ${RED}已停止${NC} (端口: $port)"
            fi
        else
            echo -e "上游服务($service_name): ${RED}未启动${NC} (端口: $port)"
        fi
    done
    
    # 检查网关状态
    local gateway_pid_file="$LOG_DIR/gateway.pid"
    if [ -f "$gateway_pid_file" ]; then
        local pid=$(cat "$gateway_pid_file")
        if ps -p $pid > /dev/null 2>&1; then
            echo -e "网关服务: ${GREEN}运行中${NC} (PID: $pid, 端口: $GATEWAY_PORT)"
        else
            echo -e "网关服务: ${RED}已停止${NC} (端口: $GATEWAY_PORT)"
        fi
    else
        echo -e "网关服务: ${RED}未启动${NC} (端口: $GATEWAY_PORT)"
    fi
    
    echo
    echo -e "${BLUE}=== API端点 ===${NC}"
    echo "健康检查: http://localhost:8080/health"
    echo "服务统计: http://localhost:8080/stats"
    echo "性能监控: http://localhost:8080/performance"
    echo "监控指标: http://localhost:9090/metrics"
}

# 查看日志
show_logs() {
    echo -e "${BLUE}=== 最新日志 ===${NC}"
    echo
    
    for service_name in "${!SERVICES[@]}"; do
        local log_file="$LOG_DIR/upstream_${service_name}.log"
        if [ -f "$log_file" ]; then
            echo -e "${YELLOW}上游服务($service_name)日志:${NC}"
            tail -5 "$log_file"
            echo
        fi
    done
    
    local gateway_log="$LOG_DIR/gateway.log"
    if [ -f "$gateway_log" ]; then
        echo -e "${YELLOW}网关服务日志:${NC}"
        tail -10 "$gateway_log"
    fi
}

# 主函数
main() {
    case "${1:-start}" in
        start)
            start_all
            ;;
        stop)
            stop_all
            ;;
        restart)
            stop_all
            sleep 2
            start_all
            ;;
        status)
            status_all
            ;;
        logs)
            show_logs
            ;;
        *)
            echo "用法: $0 [start|stop|restart|status|logs]"
            echo
            echo "命令说明:"
            echo "  start   - 启动所有服务（默认）"
            echo "  stop    - 停止所有服务"
            echo "  restart - 重启所有服务"
            echo "  status  - 查看服务状态"
            echo "  logs    - 查看服务日志"
            exit 1
            ;;
    esac
}

# 确保在项目根目录运行
cd "$PROJECT_ROOT"

# 执行主函数
main "$@"