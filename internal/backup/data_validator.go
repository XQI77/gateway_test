package backup

import (
	"fmt"
	"hash/crc32"

	"gatesvr/internal/session"
)

type dataValidator struct {
	crcTable *crc32.Table
}

func NewDataValidator() DataValidator {
	return &dataValidator{
		crcTable: crc32.MakeTable(crc32.IEEE),
	}
}

// 校验会话数据
func (v *dataValidator) ValidateSession(data *SessionSyncData) error {
	if data == nil {
		return fmt.Errorf("会话数据为空")
	}

	// 检查必要字段
	if data.SessionID == "" {
		return fmt.Errorf("会话ID不能为空")
	}

	if data.OpenID == "" {
		return fmt.Errorf("OpenID不能为空")
	}

	// 检查状态值
	if data.State < 0 || data.State > 3 {
		return fmt.Errorf("无效的会话状态: %d", data.State)
	}

	// 检查时间戳
	if data.CreateTime.IsZero() {
		return fmt.Errorf("创建时间不能为空")
	}

	if data.LastActivity.IsZero() {
		return fmt.Errorf("最后活动时间不能为空")
	}

	// 检查序列号逻辑
	if data.MaxClientSeq > 0 && data.ServerSeq == 0 {
		return fmt.Errorf("客户端序列号存在但服务器序列号为0")
	}

	// 检查队列元数据
	if data.QueueMeta != nil {
		if err := v.ValidateQueue(data.QueueMeta); err != nil {
			return fmt.Errorf("队列元数据校验失败: %w", err)
		}
	}

	// 检查待确认消息
	if data.PendingMsgs != nil {
		if err := v.ValidateMessages(data.PendingMsgs); err != nil {
			return fmt.Errorf("待确认消息校验失败: %w", err)
		}
	}

	return nil
}

func (v *dataValidator) ValidateQueue(meta *QueueMetadata) error {
	if meta == nil {
		return fmt.Errorf("队列元数据为空")
	}

	// 检查序列号逻辑
	if meta.LastAckedSeq > meta.LastSentSeq {
		return fmt.Errorf("最后确认序列号(%d)大于最后发送序列号(%d)",
			meta.LastAckedSeq, meta.LastSentSeq)
	}

	if meta.NextExpectedSeq == 0 {
		return fmt.Errorf("下一个期望序列号不能为0")
	}

	// 检查计数逻辑
	if meta.PendingCount < 0 {
		return fmt.Errorf("待确认数量不能为负数: %d", meta.PendingCount)
	}

	if meta.QueueSize < 0 {
		return fmt.Errorf("队列大小不能为负数: %d", meta.QueueSize)
	}

	// 检查业务逻辑一致性
	expectedPending := meta.LastSentSeq - meta.LastAckedSeq
	if expectedPending < 0 {
		expectedPending = 0
	}

	// 允许一定的误差（由于异步处理可能导致的不一致）
	if int64(meta.PendingCount) > int64(expectedPending+10) {
		return fmt.Errorf("待确认数量(%d)与计算值(%d)差距过大",
			meta.PendingCount, expectedPending)
	}

	return nil
}

// 校验消息数据
func (v *dataValidator) ValidateMessages(messages []*session.OrderedMessage) error {
	if len(messages) == 0 {
		return nil // 空列表有效
	}

	seqNumbers := make(map[uint64]bool)

	for i, msg := range messages {
		if msg == nil {
			return fmt.Errorf("消息[%d]为空", i)
		}

		// 检查序列号
		if msg.ServerSeq == 0 {
			return fmt.Errorf("消息[%d]序列号不能为0", i)
		}

		// 检查序列号重复
		if seqNumbers[msg.ServerSeq] {
			return fmt.Errorf("消息[%d]序列号重复: %d", i, msg.ServerSeq)
		}
		seqNumbers[msg.ServerSeq] = true

		// 检查时间戳
		if msg.Timestamp.IsZero() {
			return fmt.Errorf("消息[%d]时间戳不能为空", i)
		}

		// 检查重试次数
		if msg.Retries < 0 {
			return fmt.Errorf("消息[%d]重试次数不能为负数: %d", i, msg.Retries)
		}

		// 检查数据长度
		if len(msg.Data) == 0 {
			return fmt.Errorf("消息[%d]数据不能为空", i)
		}

		if len(msg.Data) > 1024*1024 { // 1MB限制
			return fmt.Errorf("消息[%d]数据过大: %d bytes", i, len(msg.Data))
		}
	}

	return nil
}

// 计算校验和
func (v *dataValidator) CalculateChecksum(data interface{}) uint32 {
	var bytes []byte

	switch d := data.(type) {
	case *SessionSyncData:
		key := fmt.Sprintf("%s|%s|%d|%d|%d",
			d.SessionID, d.OpenID, d.State, d.Gid, d.ServerSeq)
		bytes = []byte(key)

	case *QueueMetadata:
		key := fmt.Sprintf("%d|%d|%d|%d",
			d.NextExpectedSeq, d.LastSentSeq, d.LastAckedSeq, d.PendingCount)
		bytes = []byte(key)

	case []*session.OrderedMessage:

		key := ""
		for _, msg := range d {
			if msg != nil {
				key += fmt.Sprintf("%d|", msg.ServerSeq)
			}
		}
		bytes = []byte(key)

	default:

		bytes = []byte(fmt.Sprintf("%v", data))
	}

	return crc32.Checksum(bytes, v.crcTable)
}

func (v *dataValidator) VerifyChecksum(data interface{}, checksum uint32) bool {
	calculated := v.CalculateChecksum(data)
	return calculated == checksum
}

func (v *dataValidator) ValidateHeartbeat(data *HeartbeatData) error {
	if data == nil {
		return fmt.Errorf("心跳数据为空")
	}

	if data.ServerID == "" {
		return fmt.Errorf("服务器ID不能为空")
	}

	if data.Timestamp <= 0 {
		return fmt.Errorf("时间戳无效: %d", data.Timestamp)
	}

	if data.Mode != ModePrimary && data.Mode != ModeBackup {
		return fmt.Errorf("无效的服务器模式: %d", data.Mode)
	}

	if data.SessionCount < 0 {
		return fmt.Errorf("会话数量不能为负数: %d", data.SessionCount)
	}

	if data.QueueCount < 0 {
		return fmt.Errorf("队列数量不能为负数: %d", data.QueueCount)
	}

	return nil
}

// 校验全量同步数据
func (v *dataValidator) ValidateFullSync(data *FullSyncData) error {
	if data == nil {
		return fmt.Errorf("全量同步数据为空")
	}

	if data.ServerID == "" {
		return fmt.Errorf("服务器ID不能为空")
	}

	if data.Timestamp <= 0 {
		return fmt.Errorf("时间戳无效: %d", data.Timestamp)
	}

	if data.TotalBatches <= 0 {
		return fmt.Errorf("总批次数必须大于0: %d", data.TotalBatches)
	}

	if data.BatchIndex < 0 || data.BatchIndex >= data.TotalBatches {
		return fmt.Errorf("批次索引无效: %d, 总批次: %d", data.BatchIndex, data.TotalBatches)
	}

	if data.TotalCount < 0 {
		return fmt.Errorf("总数量不能为负数: %d", data.TotalCount)
	}

	if len(data.Sessions) > 1000 { // 单批次限制
		return fmt.Errorf("单批次会话数量过多: %d", len(data.Sessions))
	}

	for i, sessionData := range data.Sessions {
		if err := v.ValidateSession(sessionData); err != nil {
			return fmt.Errorf("会话[%d]校验失败: %w", i, err)
		}
	}

	return nil
}

// 校验同步消息
func (v *dataValidator) ValidateSyncMessage(msg *SyncMessage) error {
	if msg == nil {
		return fmt.Errorf("同步消息为空")
	}

	if msg.Type < SyncTypeCreateSession || msg.Type > SyncTypeFullSync {
		return fmt.Errorf("无效的消息类型: %d", msg.Type)
	}

	if msg.Timestamp <= 0 {
		return fmt.Errorf("时间戳无效: %d", msg.Timestamp)
	}

	// 根据消息类型校验数据
	switch msg.Type {
	case SyncTypeCreateSession, SyncTypeUpdateSession:
		if msg.SessionID == "" {
			return fmt.Errorf("会话相关消息必须有SessionID")
		}
		// 这里可以进一步校验Data字段

	case SyncTypeDeleteSession:
		if msg.SessionID == "" {
			return fmt.Errorf("删除会话消息必须有SessionID")
		}

	case SyncTypeSyncQueue:
		if msg.SessionID == "" {
			return fmt.Errorf("队列同步消息必须有SessionID")
		}

	case SyncTypeSyncMessages:
		if msg.SessionID == "" {
			return fmt.Errorf("消息同步必须有SessionID")
		}

	case SyncTypeHeartbeat:
		// 心跳消息SessionID可以为空

	case SyncTypeFullSync:
		// 全量同步SessionID为空
	}

	return nil
}
