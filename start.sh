#!/bin/bash

# ==============================================================================
# 服务启动、停止、管理脚本
#
# 优化点:
# 1. 日志和PID文件统一放入 ./logs 和 ./pids 目录。
# 2. 脚本自身的[INFO], [ERROR]等输出也会记录到 ./logs/startup.log 文件。
# 3. 重构了启动逻辑，减少代码重复。
# 4. 新增 clean 命令，用于清理日志和PID文件。
# ==============================================================================

set -e

# --- 配置区 ---

# 项目根目录
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$PROJECT_ROOT"

# 日志和PID目录
LOG_DIR="$PROJECT_ROOT/logs"
PID_DIR="$PROJECT_ROOT/pids"
SCRIPT_LOG_FILE="$LOG_DIR/startup.log"

# 定义服务和端口
declare -A SERVICES=(
    ["hello"]="8081"
    ["business"]="8082"
    ["zone"]="8083"
)
GATEWAY_PORT=8453
# 其他网关端口硬编码在脚本输出中，与原脚本保持一致
# GATEWAY_HTTP_PORT=8080
# GATEWAY_GRPC_PORT=8092

# 定义所有服务名称列表，方便循环操作
ALL_SERVICES=("hello" "business" "zone" "gateway")

# 颜色输出
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

# --- 日志函数 ---

# 统一日志处理函数，同时输出到控制台和日志文件
log_msg() {
    local type=$1
    local color=$2
    local message=$3
    echo -e "${color}[$type]${NC} $message" | tee -a "$SCRIPT_LOG_FILE"
}

log() { log_msg "INFO" "$GREEN" "$1"; }
error() { log_msg "ERROR" "$RED" "$1"; }
warn() { log_msg "WARN" "$YELLOW" "$1"; }

# --- 核心功能函数 ---

# 确保日志和PID目录存在
prepare_dirs() {
    mkdir -p "$LOG_DIR"
    mkdir -p "$PID_DIR"
}

# 构建项目
build() {
    log "构建项目..."
    # 优先使用 go build -o <output> <source> 的标准格式
    go build -o upstream ./cmd/upstream || go build -o upstream.exe ./cmd/upstream
    go build -o gatesvr ./cmd/gatesvr || go build -o gatesvr.exe ./cmd/gatesvr
    log "构建完成"
}

# 通用的服务启动函数
run_service() {
    local name=$1
    shift # 移除第一个参数 (name)
    local exec_file_stem=$1
    shift # 移除第二个参数 (exec_file_stem)
    
    log "启动 $name 服务..."
    
    # 自动选择可执行文件 (.exe 或无后缀)
    local exec_path="./$exec_file_stem"
    [ -f "./${exec_file_stem}.exe" ] && exec_path="./${exec_file_stem}.exe"

    if [ ! -f "$exec_path" ]; then
        error "$name 的可执行文件 ($exec_path) 不存在。请先执行 build 命令。"
        exit 1
    fi
    
    local log_file="$LOG_DIR/${name}.log"
    local pid_file="$PID_DIR/${name}.pid"

    # 启动命令，并将标准输出和错误输出重定向到日志文件
    nohup "$exec_path" "$@" > "$log_file" 2>&1 &
    
    # 保存PID
    echo $! > "$pid_file"
    
    # 等待一小段时间确保服务已启动
    sleep 2
    log "$name 服务已启动 (PID: $(cat "$pid_file"), 日志: $log_file)"
}

# 停止单个服务
stop_service() {
    local name=$1
    local pid_file="$PID_DIR/${name}.pid"
    
    if [ ! -f "$pid_file" ]; then
        # 如果pid文件不存在，则无需操作
        return
    fi
    
    local pid=$(cat "$pid_file")
    if kill -0 "$pid" 2>/dev/null; then
        log "正在停止 $name (PID: $pid)..."
        kill "$pid"
        sleep 1 # 等待进程退出
        log "$name 已停止。"
    else
        warn "$name (PID: $pid) 已经停止运行。"
    fi
    rm -f "$pid_file"
}

# --- 主命令函数 ---

# 启动所有服务
start() {
    log "================= 开始启动所有服务 =================="
    prepare_dirs
    build
    
    # 启动上游服务
    for service in "${!SERVICES[@]}"; do
        local port=${SERVICES[$service]}
        run_service "$service" "upstream" "-port=$port" "-service=$service"
    done
    
    # 启动网关服务
    run_service "gateway" "gatesvr" "-config=test-config.yaml"
    
    log "================= 所有服务启动完成 =================="
    echo
    log "服务端口信息:"
    log "  - Hello服务:    ${SERVICES[hello]}"
    log "  - Business服务: ${SERVICES[business]}"
    log "  - Zone服务:     ${SERVICES[zone]}"
    log "  - 网关 (QUIC):  $GATEWAY_PORT"
    log "  - 网关 (HTTP):  8080"
    log "  - 网关 (gRPC):  8092"
    echo
    log "使用 'tail -f $LOG_DIR/*.log' 查看实时日志"
    log "使用 './your_script_name.sh status' 查看服务状态"
}

# 停止所有服务
stop() {
    log "================= 开始停止所有服务 =================="
    # 倒序停止，先停网关
    for ((i=${#ALL_SERVICES[@]}-1; i>=0; i--)); do
        stop_service "${ALL_SERVICES[i]}"
    done
    log "================= 所有服务已停止 =================="
}

# 查看服务状态
status() {
    log_msg "STATUS" "$YELLOW" "检查服务状态..."
    echo "--------------------------------------------------"
    for name in "${ALL_SERVICES[@]}"; do
        local pid_file="$PID_DIR/${name}.pid"
        if [ -f "$pid_file" ]; then
            local pid=$(cat "$pid_file")
            if kill -0 "$pid" 2>/dev/null; then
                printf "  - %-10s: ${GREEN}%-10s${NC} (PID: %s)\n" "$name" "运行中" "$pid"
            else
                printf "  - %-10s: ${RED}%-10s${NC} (PID文件存在但进程已死)\n" "$name" "异常停止"
            fi
        else
            printf "  - %-10s: ${RED}%-10s${NC}\n" "$name" "未启动"
        fi
    done
    echo "--------------------------------------------------"
}

# 查看最近的日志
logs() {
    log_msg "LOGS" "$YELLOW" "查看各服务最新日志..."
    for name in "${ALL_SERVICES[@]}"; do
        local log_file="$LOG_DIR/${name}.log"
        if [ -f "$log_file" ]; then
            echo -e "\n--- ${YELLOW}日志: $log_file${NC} ---"
            tail -n 10 "$log_file"
        fi
    done
}

# 清理日志和PID文件
clean() {
    warn "此操作将删除所有日志和PID文件，是否继续? (y/n)"
    read -r -p "> " response
    if [[ "$response" =~ ^([yY][eE][sS]|[yY])$ ]]; then
        log "正在停止所有服务..."
        stop > /dev/null 2>&1 # 停止服务，但不输出到控制台
        log "正在清理日志和PID目录..."
        rm -rf "$LOG_DIR" "$PID_DIR"
        log "清理完成。"
    else
        log "操作已取消。"
    fi
}

# 主逻辑入口
main() {
    # 如果是第一次运行，清空旧的脚本日志
    if [ "$1" == "start" ] || [ "$1" == "restart" ]; then
      [ -f "$SCRIPT_LOG_FILE" ] && rm "$SCRIPT_LOG_FILE"
    fi
    prepare_dirs # 确保目录总是存在

    case "${1:-start}" in
        start)   start ;;
        stop)    stop ;;
        restart) stop; sleep 2; start ;;
        status)  status ;;
        logs)    logs ;;
        clean)   clean ;;
        build)   build ;;
        *)
            echo "用法: $0 [start|stop|restart|status|logs|build|clean]"
            echo
            echo "  命令:"
            echo "    start   - (默认) 构建并启动所有服务"
            echo "    stop    - 停止所有服务"
            echo "    restart - 重启所有服务"
            echo "    status  - 查看所有服务的运行状态"
            echo "    logs    - 查看每个服务最新的10行日志"
            echo "    build   - 仅构建项目，不启动服务"
            echo "    clean   - 停止服务并删除所有日志和PID文件"
            exit 1
            ;;
    esac
}

# 执行主函数
main "$@"