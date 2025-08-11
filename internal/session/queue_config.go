package session

// 队列配置
type QueueConfig struct {
	MaxQueueSize    int `json:"max_queue_size"`
	MaxRetries      int `json:"max_retries"`
	CleanupInterval int `json:"cleanup_interval_ms"`
}

func LoadQueueConfigFromEnv() *QueueConfig {
	return &QueueConfig{
		MaxQueueSize:    1000,
		MaxRetries:      MaxRetries,
		CleanupInterval: 10000, // 10s
	}
}

func (c *QueueConfig) Validate() error {
	if c.MaxQueueSize <= 0 {
		c.MaxQueueSize = 1000
	}

	if c.MaxRetries < 0 {
		c.MaxRetries = MaxRetries
	}

	if c.CleanupInterval <= 0 {
		c.CleanupInterval = 10000
	}

	return nil
}
