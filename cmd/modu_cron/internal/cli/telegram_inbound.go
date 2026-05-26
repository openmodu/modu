package cli

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/channels"
	"github.com/openmodu/modu/pkg/channels/telegram"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
	"github.com/openmodu/modu/cmd/modu_cron/internal/provider"
)

type telegramInboundManager struct {
	mu   sync.Mutex
	bots map[string]*telegramInboundBot
}

type telegramInboundBot struct {
	cfgPath string
	cancel  context.CancelFunc

	allowedMu sync.RWMutex
	allowed   map[int64]bool

	runMu sync.Mutex
}

type telegramBinding struct {
	token  string
	chatID int64
	name   string
}

func newTelegramInboundManager() *telegramInboundManager {
	return &telegramInboundManager{bots: make(map[string]*telegramInboundBot)}
}

func (m *telegramInboundManager) Reload(parent context.Context, cfgPath string) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Printf("telegram inbound reload failed: %v", err)
		return
	}
	bindings := telegramBindings(cfg)
	desired := make(map[string]map[int64]bool)
	for _, b := range bindings {
		if desired[b.token] == nil {
			desired[b.token] = make(map[int64]bool)
		}
		desired[b.token][b.chatID] = true
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for token, bot := range m.bots {
		allowed, keep := desired[token]
		if !keep {
			bot.cancel()
			delete(m.bots, token)
			continue
		}
		bot.setAllowed(allowed)
		delete(desired, token)
	}

	attachDir := filepath.Join(filepath.Dir(cfgPath), "attachments", "telegram")
	for token, allowed := range desired {
		botCtx, cancel := context.WithCancel(parent)
		running := &telegramInboundBot{
			cfgPath: cfgPath,
			cancel:  cancel,
			allowed: allowed,
		}
		tgBot, err := telegram.NewBot(token, attachDir, running.handleMessage, nil)
		if err != nil {
			cancel()
			log.Printf("telegram inbound start failed: %v", err)
			continue
		}
		m.bots[token] = running
		log.Printf("telegram inbound started: @%s allowed_chats=%d", tgBot.Username(), len(allowed))
		go func() {
			if err := tgBot.Run(botCtx); err != nil && botCtx.Err() == nil {
				log.Printf("telegram inbound stopped: %v", err)
			}
		}()
	}
}

func (m *telegramInboundManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for token, bot := range m.bots {
		bot.cancel()
		delete(m.bots, token)
	}
}

func (b *telegramInboundBot) setAllowed(allowed map[int64]bool) {
	b.allowedMu.Lock()
	defer b.allowedMu.Unlock()
	b.allowed = allowed
}

func (b *telegramInboundBot) allowedChat(chatID int64) bool {
	b.allowedMu.RLock()
	defer b.allowedMu.RUnlock()
	return b.allowed[chatID]
}

func (b *telegramInboundBot) handleMessage(ctx context.Context, chCtx channels.ChannelContext) {
	if !b.allowedChat(chCtx.ChatID()) {
		return
	}
	text := strings.TrimSpace(chCtx.MessageText())
	if text == "" {
		_ = chCtx.RespondInThread("请发送要查看、添加或删除的 cron 任务描述。")
		return
	}

	_ = chCtx.SetWorking(true)
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cfg, err := config.Load(b.cfgPath)
	if err != nil {
		_ = chCtx.RespondInThread("error: " + err.Error())
		return
	}
	model, getAPIKey := provider.ResolveWithConfig(cfg)
	fallbackCwd, err := os.Getwd()
	if err != nil {
		_ = chCtx.RespondInThread("error: " + err.Error())
		return
	}
	cwd := config.ResolveWorkingDir(b.cfgPath, cfg, fallbackCwd)
	opts := ManageOptions{
		CfgPath:   b.cfgPath,
		Cwd:       cwd,
		AgentDir:  coding_agent.DefaultAgentDir(),
		Model:     model,
		GetAPIKey: getAPIKey,
	}

	var out bytes.Buffer
	b.runMu.Lock()
	err = ManageCron(runCtx, opts, text, &out)
	b.runMu.Unlock()

	reply := strings.TrimSpace(out.String())
	if err != nil {
		if reply != "" {
			reply += "\n"
		}
		reply += "error: " + err.Error()
	}
	if reply == "" {
		reply = "已处理。"
	}
	_ = chCtx.RespondInThread(reply)
}

func telegramBindings(cfg *config.Config) []telegramBinding {
	if cfg == nil {
		return nil
	}
	var bindings []telegramBinding
	for name, ch := range cfg.Channels {
		if strings.ToLower(strings.TrimSpace(ch.Type)) != "telegram" {
			continue
		}
		token := strings.TrimSpace(valueOrEnv(ch.Token, ch.TokenEnv))
		chatIDText := strings.TrimSpace(valueOrEnv(ch.ChatID, ch.ChatIDEnv))
		if token == "" || chatIDText == "" {
			log.Printf("telegram channel %q missing token or chat_id; inbound disabled", name)
			continue
		}
		chatID, err := strconv.ParseInt(chatIDText, 10, 64)
		if err != nil {
			log.Printf("telegram channel %q invalid chat_id %q: %v", name, chatIDText, err)
			continue
		}
		bindings = append(bindings, telegramBinding{token: token, chatID: chatID, name: name})
	}
	return bindings
}

func valueOrEnv(value, envName string) string {
	if value != "" {
		return os.ExpandEnv(value)
	}
	if envName == "" {
		return ""
	}
	return os.Getenv(envName)
}
