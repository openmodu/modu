package mailbox

// Store 是 Hub 数据持久化的抽象接口。
// 实现需保证线程安全。
// 默认实现 noopStore 不做任何持久化（向后兼容）。
type Store interface {
	// SaveTask 创建或更新一条任务记录
	SaveTask(task Task) error
	// LoadTasks 加载所有已持久化的任务
	LoadTasks() ([]Task, error)
	// SaveAgentRole 持久化 agent 的角色
	SaveAgentRole(agentID, role string) error
	// LoadAgentRoles 加载所有 agent 的角色映射
	LoadAgentRoles() (map[string]string, error)
	// Close 关闭持久化后端（释放连接等）
	Close() error
}

// noopStore 是不做持久化的默认实现
type noopStore struct{}

func (noopStore) SaveTask(_ Task) error                      { return nil }
func (noopStore) LoadTasks() ([]Task, error)                 { return nil, nil }
func (noopStore) SaveAgentRole(_, _ string) error            { return nil }
func (noopStore) LoadAgentRoles() (map[string]string, error) { return nil, nil }
func (noopStore) Close() error                               { return nil }
