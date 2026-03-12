package client

import (
	"context"
	"errors"
	"sync"
	"time"

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

// Register 向 Mailbox 注册自己，并自动启动后台心跳保活
func (c *MailboxClient) Register(ctx context.Context) error {
	// rdb.Do 用于发送自定义 RESP 指令
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
		return "", nil // 没有新消息
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
				err := c.Ping(ctx)
				if err != nil {
					// 尝试重新注册
					_ = c.Register(ctx)
				}
			}
		}
	}()
}
