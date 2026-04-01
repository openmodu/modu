package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/mailbox"
	"github.com/redis/go-redis/v9"
)

type MailboxClient struct {
	agentID string
	rdb     *redis.Client
	once    sync.Once
}

func NewMailboxClient(agentID, addr string) *MailboxClient {
	return &MailboxClient{
		agentID: agentID,
		rdb: redis.NewClient(&redis.Options{
			Addr: addr,
		}),
	}
}

// AgentID 返回当前 client 绑定的 agent ID
func (c *MailboxClient) AgentID() string {
	return c.agentID
}

// Register 向 Mailbox 注册自己，并自动启动后台心跳保活
func (c *MailboxClient) Register(ctx context.Context) error {
	res, err := c.rdb.Do(ctx, "AGENT.REG", c.agentID).Result()
	if err != nil {
		return err
	}
	if res != "OK" {
		return errors.New("failed to register")
	}
	c.once.Do(func() {
		c.startKeepAlive(ctx)
	})
	return nil
}

// Send 向目标 Agent 发送消息
func (c *MailboxClient) Send(ctx context.Context, targetID, msg string) error {
	_, err := c.rdb.Do(ctx, "MSG.SEND", targetID, msg).Result()
	return err
}

// Recv 轮询自己的信箱 (非阻塞)
func (c *MailboxClient) Recv(ctx context.Context) (string, error) {
	res, err := c.rdb.Do(ctx, "MSG.RECV", c.agentID).Result()
	if err == redis.Nil {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return res.(string), nil
}

// ListAgents 获取当前活跃的所有 Agent
func (c *MailboxClient) ListAgents(ctx context.Context) ([]string, error) {
	res, err := c.rdb.Do(ctx, "AGENT.LIST").StringSlice()
	if err == redis.Nil {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return res, nil
}

// Broadcast 广播消息给所有 Agent
func (c *MailboxClient) Broadcast(ctx context.Context, msg string) error {
	_, err := c.rdb.Do(ctx, "MSG.BCAST", msg).Result()
	return err
}

// Ping 发送心跳保持在线状态
func (c *MailboxClient) Ping(ctx context.Context) error {
	res, err := c.rdb.Do(ctx, "AGENT.PING", c.agentID).Result()
	if err != nil {
		return err
	}
	if res != "PONG" && res != "OK" {
		return errors.New("unexpected ping response")
	}
	return nil
}

// --- Agent 元数据方法 ---

// SetRole 设置当前 Agent 的角色
func (c *MailboxClient) SetRole(ctx context.Context, role string) error {
	res, err := c.rdb.Do(ctx, "AGENT.SETROLE", c.agentID, role).Result()
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("AGENT.SETROLE unexpected response: %v", res)
	}
	return nil
}

// SetStatus 设置当前 Agent 的状态，taskID 为正在处理的任务（空闲时传空字符串）
func (c *MailboxClient) SetStatus(ctx context.Context, status, taskID string) error {
	var res interface{}
	var err error
	if taskID == "" {
		res, err = c.rdb.Do(ctx, "AGENT.SETSTATUS", c.agentID, status).Result()
	} else {
		res, err = c.rdb.Do(ctx, "AGENT.SETSTATUS", c.agentID, status, taskID).Result()
	}
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("AGENT.SETSTATUS unexpected response: %v", res)
	}
	return nil
}

// GetAgentInfo 获取任意 Agent 的元数据
func (c *MailboxClient) GetAgentInfo(ctx context.Context, agentID string) (mailbox.AgentInfo, error) {
	raw, err := c.rdb.Do(ctx, "AGENT.INFO", agentID).Result()
	if err != nil {
		return mailbox.AgentInfo{}, err
	}
	var info mailbox.AgentInfo
	if err := json.Unmarshal([]byte(fmt.Sprintf("%s", raw)), &info); err != nil {
		return mailbox.AgentInfo{}, fmt.Errorf("unmarshal AgentInfo: %w", err)
	}
	return info, nil
}

// --- Project 方法 ---

// CreateProject 创建一个新项目，返回 project ID
func (c *MailboxClient) CreateProject(ctx context.Context, name string) (string, error) {
	res, err := c.rdb.Do(ctx, "PROJ.CREATE", c.agentID, name).Result()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s", res), nil
}

// CompleteProject 将项目标记为已完成
func (c *MailboxClient) CompleteProject(ctx context.Context, projectID string) error {
	res, err := c.rdb.Do(ctx, "PROJ.COMPLETE", projectID).Result()
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("PROJ.COMPLETE unexpected response: %v", res)
	}
	return nil
}

// GetProject 获取指定项目详情
func (c *MailboxClient) GetProject(ctx context.Context, projectID string) (mailbox.Project, error) {
	raw, err := c.rdb.Do(ctx, "PROJ.GET", projectID).Result()
	if err != nil {
		return mailbox.Project{}, err
	}
	var proj mailbox.Project
	if err := json.Unmarshal([]byte(fmt.Sprintf("%s", raw)), &proj); err != nil {
		return mailbox.Project{}, fmt.Errorf("unmarshal Project: %w", err)
	}
	return proj, nil
}

// ListProjects 获取所有项目列表
func (c *MailboxClient) ListProjects(ctx context.Context) ([]mailbox.Project, error) {
	raw, err := c.rdb.Do(ctx, "PROJ.LIST").Result()
	if err != nil {
		return nil, err
	}
	var projects []mailbox.Project
	if err := json.Unmarshal([]byte(fmt.Sprintf("%s", raw)), &projects); err != nil {
		return nil, fmt.Errorf("unmarshal projects: %w", err)
	}
	return projects, nil
}

// --- Task 方法 ---

// CreateTask 创建一个新任务，返回 task ID。可选传入 projectID 将任务归入项目。
func (c *MailboxClient) CreateTask(ctx context.Context, description string, projectID ...string) (string, error) {
	var res interface{}
	var err error
	if len(projectID) > 0 && projectID[0] != "" {
		res, err = c.rdb.Do(ctx, "TASK.CREATE", c.agentID, description, projectID[0]).Result()
	} else {
		res, err = c.rdb.Do(ctx, "TASK.CREATE", c.agentID, description).Result()
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s", res), nil
}

// AssignTask 将任务分配给一个或多个 Agent
func (c *MailboxClient) AssignTask(ctx context.Context, taskID string, agentIDs ...string) error {
	if len(agentIDs) == 0 {
		return nil
	}
	args := make([]interface{}, 0, 2+len(agentIDs))
	args = append(args, "TASK.ASSIGN", taskID)
	for _, id := range agentIDs {
		args = append(args, id)
	}
	res, err := c.rdb.Do(ctx, args...).Result()
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("TASK.ASSIGN unexpected response: %v", res)
	}
	return nil
}

// StartTask 将任务标记为运行中
func (c *MailboxClient) StartTask(ctx context.Context, taskID string) error {
	res, err := c.rdb.Do(ctx, "TASK.START", taskID).Result()
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("TASK.START unexpected response: %v", res)
	}
	return nil
}

// CompleteTask 将当前 agent 对任务的成果提交，标记为已完成
func (c *MailboxClient) CompleteTask(ctx context.Context, taskID, result string) error {
	res, err := c.rdb.Do(ctx, "TASK.DONE", taskID, c.agentID, result).Result()
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("TASK.DONE unexpected response: %v", res)
	}
	return nil
}

// FailTask 将任务标记为失败
func (c *MailboxClient) FailTask(ctx context.Context, taskID, errMsg string) error {
	res, err := c.rdb.Do(ctx, "TASK.FAIL", taskID, errMsg).Result()
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("TASK.FAIL unexpected response: %v", res)
	}
	return nil
}

// ListTasks 获取任务列表，可选按项目过滤
func (c *MailboxClient) ListTasks(ctx context.Context, projectID ...string) ([]mailbox.Task, error) {
	var raw interface{}
	var err error
	if len(projectID) > 0 && projectID[0] != "" {
		raw, err = c.rdb.Do(ctx, "TASK.LIST", projectID[0]).Result()
	} else {
		raw, err = c.rdb.Do(ctx, "TASK.LIST").Result()
	}
	if err != nil {
		return nil, err
	}
	var tasks []mailbox.Task
	if err := json.Unmarshal([]byte(fmt.Sprintf("%s", raw)), &tasks); err != nil {
		return nil, fmt.Errorf("unmarshal tasks: %w", err)
	}
	return tasks, nil
}

// GetTask 获取指定任务详情
func (c *MailboxClient) GetTask(ctx context.Context, taskID string) (mailbox.Task, error) {
	raw, err := c.rdb.Do(ctx, "TASK.GET", taskID).Result()
	if err != nil {
		return mailbox.Task{}, err
	}
	var task mailbox.Task
	if err := json.Unmarshal([]byte(fmt.Sprintf("%s", raw)), &task); err != nil {
		return mailbox.Task{}, fmt.Errorf("unmarshal task: %w", err)
	}
	return task, nil
}

// GetConversation 获取指定任务的对话记录
func (c *MailboxClient) GetConversation(ctx context.Context, taskID string) ([]mailbox.ConversationEntry, error) {
	raw, err := c.rdb.Do(ctx, "CONV.GET", taskID).Result()
	if err != nil {
		return nil, err
	}
	var entries []mailbox.ConversationEntry
	if err := json.Unmarshal([]byte(fmt.Sprintf("%s", raw)), &entries); err != nil {
		return nil, fmt.Errorf("unmarshal conversation: %w", err)
	}
	return entries, nil
}

// --- Swarm methods ---

// SetCapabilities declares the capability list for the current agent (used for swarm task matching).
func (c *MailboxClient) SetCapabilities(ctx context.Context, caps ...string) error {
	args := make([]interface{}, 0, 2+len(caps))
	args = append(args, "AGENT.SETCAPS", c.agentID)
	for _, cap := range caps {
		args = append(args, cap)
	}
	res, err := c.rdb.Do(ctx, args...).Result()
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("AGENT.SETCAPS unexpected response: %v", res)
	}
	return nil
}

// PublishTask adds a task to the shared swarm queue with optional required capabilities.
// Returns the task ID. The caller does not need to be a registered agent.
func (c *MailboxClient) PublishTask(ctx context.Context, description string, caps ...string) (string, error) {
	args := make([]interface{}, 0, 3+len(caps))
	args = append(args, "TASK.PUBLISH", c.agentID, description)
	for _, cap := range caps {
		args = append(args, cap)
	}
	res, err := c.rdb.Do(ctx, args...).Result()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s", res), nil
}

// ClaimTask atomically claims a task from the swarm queue that matches the agent's capabilities.
// Returns nil when no matching task is available.
func (c *MailboxClient) ClaimTask(ctx context.Context) (*mailbox.Task, error) {
	raw, err := c.rdb.Do(ctx, "TASK.CLAIM", c.agentID).Result()
	if err == redis.Nil {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	var task mailbox.Task
	if err := json.Unmarshal([]byte(fmt.Sprintf("%s", raw)), &task); err != nil {
		return nil, fmt.Errorf("unmarshal task: %w", err)
	}
	return &task, nil
}

// ListSwarmQueue returns all tasks currently waiting in the swarm queue.
func (c *MailboxClient) ListSwarmQueue(ctx context.Context) ([]mailbox.Task, error) {
	raw, err := c.rdb.Do(ctx, "TASK.QUEUE").Result()
	if err != nil {
		return nil, err
	}
	var tasks []mailbox.Task
	if err := json.Unmarshal([]byte(fmt.Sprintf("%s", raw)), &tasks); err != nil {
		return nil, fmt.Errorf("unmarshal swarm queue: %w", err)
	}
	return tasks, nil
}

// --- Adversarial validation methods ---

// PublishValidatedTask publishes a task that requires adversarial validation before
// it is considered done. A validator agent must score the result; scores below
// passThreshold cause the task to be re-queued (up to maxRetries times).
func (c *MailboxClient) PublishValidatedTask(ctx context.Context, description string, maxRetries int, passThreshold float64, caps ...string) (string, error) {
	args := make([]interface{}, 0, 5+len(caps))
	args = append(args, "TASK.PUBLISH_VALIDATED", c.agentID, description,
		fmt.Sprintf("%d", maxRetries),
		fmt.Sprintf("%.2f", passThreshold),
	)
	for _, cap := range caps {
		args = append(args, cap)
	}
	res, err := c.rdb.Do(ctx, args...).Result()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s", res), nil
}

// SubmitForValidation records the agent's result and enqueues a validator task.
// Returns the validate task ID. The agent's status is automatically set back to idle.
func (c *MailboxClient) SubmitForValidation(ctx context.Context, taskID, result string) (string, error) {
	res, err := c.rdb.Do(ctx, "TASK.SUBMIT", taskID, c.agentID, result).Result()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s", res), nil
}

// SubmitValidation sends the validator's score and feedback for a validate task.
// score must be between 0.0 (reject) and 1.0 (perfect).
func (c *MailboxClient) SubmitValidation(ctx context.Context, validateTaskID string, score float64, feedback string) error {
	res, err := c.rdb.Do(ctx, "TASK.VALIDATE", validateTaskID, c.agentID,
		fmt.Sprintf("%.4f", score), feedback).Result()
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("TASK.VALIDATE unexpected response: %v", res)
	}
	return nil
}

// PublishPipeline creates a pipeline on the server and enqueues its first step.
func (c *MailboxClient) PublishPipeline(ctx context.Context, creatorID string, steps []mailbox.PipelineStep) (string, error) {
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return "", fmt.Errorf("marshal steps: %w", err)
	}
	raw, err := c.rdb.Do(ctx, "PIPELINE.PUBLISH", creatorID, string(stepsJSON)).Result()
	if err != nil {
		return "", err
	}
	id, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("unexpected response: %v", raw)
	}
	return id, nil
}

// GetPipeline fetches the current snapshot of a pipeline by ID.
// Returns nil if the pipeline does not exist.
func (c *MailboxClient) GetPipeline(ctx context.Context, pipelineID string) (*mailbox.Pipeline, error) {
	raw, err := c.rdb.Do(ctx, "PIPELINE.GET", pipelineID).Result()
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	str, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("unexpected response type: %T", raw)
	}
	var p mailbox.Pipeline
	if err := json.Unmarshal([]byte(str), &p); err != nil {
		return nil, fmt.Errorf("unmarshal pipeline: %w", err)
	}
	return &p, nil
}

// startKeepAlive 启动一个后台协程，定期发送 PING 维持 Agent 在线状态
func (c *MailboxClient) startKeepAlive(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.Ping(ctx); err != nil {
					_ = c.Register(ctx)
				}
			}
		}
	}()
}
