package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	codetui "github.com/openmodu/modu/cmd/modu_code/internal/tui"
	"github.com/openmodu/modu/pkg/channels"
	"github.com/openmodu/modu/pkg/channels/feishu"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/tgbot"
)

func startModuTUIFeishuBot(ctx context.Context, session *coding_agent.CodingSession, promptMu *sync.Mutex, client modutui.Client) {
	feishu.RuntimeLogf("modu_code feishu startup check")
	presenter := codetui.NewPresenter(client)
	cfg, err := feishu.EffectiveConfig()
	if err != nil {
		feishu.RuntimeLogf("config error: %v", err)
		presenter.Text(modutui.RoleAssistant, "feishu config error: "+err.Error())
		return
	}
	if cfg == nil || !cfg.Ready() {
		feishu.RuntimeLogf("bot disabled: app_id_set=%v app_secret_set=%v", cfg != nil && strings.TrimSpace(cfg.AppID) != "", cfg != nil && strings.TrimSpace(cfg.AppSecret) != "")
		return
	}
	bot, err := feishu.NewBot(cfg.AppID, cfg.AppSecret, nil, nil)
	if err != nil {
		feishu.RuntimeLogf("bot create failed: %v", err)
		printer := &moduTUIChannelPrinter{presenter: presenter, channel: "feishu"}
		printer.PrintError(err)
		return
	}
	bot.SetAllowedChatIDs(cfg.ChatIDs)
	bot.SetDebugLogger(feishu.RuntimeLogf)
	feishu.RuntimeLogf("bot configured: app_id=%s chat_allowlist=%d", cfg.AppID, len(cfg.ChatIDs))

	printer := &moduTUIChannelPrinter{presenter: presenter, channel: "feishu"}
	if err := channels.StartCodingBridge(ctx, channels.CodingBridgeOptions{
		Channel:  bot,
		Session:  session,
		Printer:  printer,
		PromptMu: promptMu,
		Logf:     feishu.RuntimeLogf,
	}); err != nil {
		feishu.RuntimeLogf("bot start failed: %v", err)
		printer.PrintError(err)
		return
	}
	feishu.RuntimeLogf("bot start requested")
}

func startModuTUITelegramBot(ctx context.Context, session *coding_agent.CodingSession, promptMu *sync.Mutex, client modutui.Client) {
	printer := &moduTUIChannelPrinter{presenter: codetui.NewPresenter(client), channel: "telegram"}
	token, err := moduTUITelegramToken()
	if err != nil {
		printer.PrintError(fmt.Errorf("telegram config: %w", err))
		return
	}
	if token == "" {
		return
	}
	attachDir := filepath.Join(os.TempDir(), "modu_code_tg")
	if _, err := tgbot.Start(ctx, token, attachDir, session, printer, promptMu, nil); err != nil {
		printer.PrintError(err)
	}
}

func moduTUITelegramToken() (string, error) {
	token := strings.TrimSpace(os.Getenv("MOMS_TG_TOKEN"))
	cfg, err := tgbot.LoadConfig()
	if err != nil {
		return "", err
	}
	if configured := strings.TrimSpace(cfg.Token); configured != "" {
		token = configured
	}
	return token, nil
}

type moduTUIChannelPrinter struct {
	presenter codetui.Presenter
	channel   string
}

func (p *moduTUIChannelPrinter) ClearLine() {}

func (p *moduTUIChannelPrinter) PrintUser(text string) {
	if p == nil || strings.TrimSpace(text) == "" {
		return
	}
	p.presenter.Text(modutui.RoleUser, text)
}

func (p *moduTUIChannelPrinter) PrintInfo(text string) {
	if p == nil || strings.TrimSpace(text) == "" {
		return
	}
	p.presenter.Text(modutui.RoleAssistant, "["+p.channel+"] "+text)
}

func (p *moduTUIChannelPrinter) PrintError(err error) {
	if err != nil {
		p.PrintInfo("error: " + err.Error())
	}
}
