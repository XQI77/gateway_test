// Package gateway 提供异步请求处理功能
package gateway

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"

	"gatesvr/internal/session"
	pb "gatesvr/proto"
)

// 异步任务结构
type AsyncTask struct {
	TaskID      string
	SessionID   string
	Session     *session.Session
	Request     *pb.ClientRequest
	BusinessReq *pb.BusinessRequest
	UpstreamReq *pb.UpstreamRequest
	Context     context.Context
	IsLogin     bool
}

// 异步处理配置
type AsyncConfig struct {
	MaxWorkers   int           `json:"max_workers"`
	MaxQueueSize int           `json:"max_queue_size"`
	TaskTimeout  time.Duration `json:"task_timeout"`
}

// 默认异步配置
var DefaultAsyncConfig = &AsyncConfig{
	MaxWorkers:   runtime.NumCPU() * 4,
	MaxQueueSize: 10000,
	TaskTimeout:  30 * time.Second,
}

// 异步请求处理器
type AsyncRequestProcessor struct {
	config     *AsyncConfig
	taskQueue  chan *AsyncTask
	server     *Server
	stopCh     chan struct{}
	wg         sync.WaitGroup
	started    bool
	startMutex sync.Mutex
}

// 创建异步请求处理器
func NewAsyncRequestProcessor(server *Server, config *AsyncConfig) *AsyncRequestProcessor {
	if config == nil {
		config = DefaultAsyncConfig
	}

	return &AsyncRequestProcessor{
		config:    config,
		taskQueue: make(chan *AsyncTask, config.MaxQueueSize),
		server:    server,
		stopCh:    make(chan struct{}),
		started:   false,
	}
}

// 启动异步处理器
func (ap *AsyncRequestProcessor) Start() error {
	ap.startMutex.Lock()
	defer ap.startMutex.Unlock()

	if ap.started {
		return fmt.Errorf("异步处理器已启动")
	}

	// 启动工作协程池
	for i := 0; i < ap.config.MaxWorkers; i++ {
		ap.wg.Add(1)
		go ap.worker(i)
	}

	ap.started = true
	log.Printf("异步请求处理器已启动 - 工作协程数: %d, 队列大小: %d",
		ap.config.MaxWorkers, ap.config.MaxQueueSize)

	return nil
}

// 停止异步处理器
func (ap *AsyncRequestProcessor) Stop() error {
	ap.startMutex.Lock()
	defer ap.startMutex.Unlock()

	if !ap.started {
		return nil
	}

	close(ap.stopCh)
	ap.wg.Wait()

	ap.started = false
	log.Printf("异步请求处理器已停止")

	return nil
}

// 提交异步任务
func (ap *AsyncRequestProcessor) SubmitTask(task *AsyncTask) bool {
	if !ap.started {
		log.Printf("异步处理器未启动，拒绝任务: %s", task.TaskID)
		return false
	}

	select {
	case ap.taskQueue <- task:
		log.Printf("异步任务已提交 - 任务ID: %s, 会话: %s, 动作: %s",
			task.TaskID, task.SessionID, task.BusinessReq.Action)
		return true
	default:
		// 队列已满，拒绝任务
		log.Printf("异步队列已满，拒绝任务 - 任务ID: %s, 会话: %s",
			task.TaskID, task.SessionID)
		return false
	}
}

// 获取队列长度
func (ap *AsyncRequestProcessor) GetQueueLength() int {
	return len(ap.taskQueue)
}

// 获取统计信息
func (ap *AsyncRequestProcessor) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"max_workers":    ap.config.MaxWorkers,
		"max_queue_size": ap.config.MaxQueueSize,
		"queue_length":   ap.GetQueueLength(),
		"started":        ap.started,
	}
}

// 工作协程
func (ap *AsyncRequestProcessor) worker(workerID int) {
	defer ap.wg.Done()
	log.Printf("异步工作协程启动 - ID: %d", workerID)

	for {
		select {
		case task := <-ap.taskQueue:
			ap.processTask(workerID, task)
		case <-ap.stopCh:
			log.Printf("异步工作协程停止 - ID: %d", workerID)
			return
		}
	}
}

// 处理异步任务
func (ap *AsyncRequestProcessor) processTask(workerID int, task *AsyncTask) {
	startTime := time.Now()
	log.Printf("开始处理异步任务 - 工作协程: %d, 任务: %s, 会话: %s, 动作: %s",
		workerID, task.TaskID, task.SessionID, task.BusinessReq.Action)

	defer func() {
		processingTime := time.Since(startTime)
		log.Printf("异步任务处理完成 - 工作协程: %d, 任务: %s, 耗时: %.2fms",
			workerID, task.TaskID, processingTime.Seconds()*1000)

	}()

	// 检查会话是否仍然有效
	if !ap.isSessionValid(task.Session) {
		log.Printf("会话已失效，跳过任务处理 - 任务: %s, 会话: %s", task.TaskID, task.SessionID)
		return
	}

	// 直接使用传入的上下文（已包含上游超时设置）
	ctx := task.Context

	upstreamResp, err := ap.server.callUpstreamService(ctx, task.Session.OpenID, task.UpstreamReq)

	if err != nil {
		serviceInfo := ap.server.getUpstreamServiceInfo(task.Session.OpenID)
		log.Printf("异步调用上游服务失败 - 任务: %s, 服务: %s, 错误: %v",
			task.TaskID, serviceInfo, err)
		ap.server.metrics.IncError("upstream_error")
		ap.server.sendErrorResponse(task.Session, task.Request.MsgId, 500, "上游服务错误", err.Error())
		return
	}

	if err := ap.server.orderedSender.SendBusinessResponse(task.Session, task.Request.MsgId,
		upstreamResp.Code, upstreamResp.Message, upstreamResp.Data, upstreamResp.Headers); err != nil {
		log.Printf("异步发送业务响应失败 - 任务: %s, 错误: %v", task.TaskID, err)
		return
	}

	// 处理绑定notify消息
	grid := uint32(task.Request.SeqId)
	if err := ap.server.processBoundNotifies(task.Session, grid); err != nil {
		log.Printf("异步处理绑定notify消息失败 - 任务: %s, 错误: %v", task.TaskID, err)
	}

	log.Printf("异步任务处理成功 - 任务: %s, 响应码: %d",
		task.TaskID, upstreamResp.Code)
}

// 检查会话是否仍然有效
func (ap *AsyncRequestProcessor) isSessionValid(sess *session.Session) bool {
	if sess == nil {
		return false
	}

	// 检查会话状态是否已关闭
	if int32(sess.State()) == int32(session.SessionClosed) {
		return false
	}

	// 检查连接是否仍然有效
	if sess.Connection == nil {
		return false
	}

	return true
}
