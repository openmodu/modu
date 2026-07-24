package tui

import (
	"strings"
	"time"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

// Dialog is the business-facing interface to interactive overlays and
// transcript/status updates. Business flows do not need to construct Bubble
// Tea messages or own response channels.
type Dialog struct {
	client    modutui.Client
	presenter Presenter
	statusTTL time.Duration
}

func NewDialog(client modutui.Client, statusTTL time.Duration) Dialog {
	return Dialog{
		client:    client,
		presenter: NewPresenter(client),
		statusTTL: statusTTL,
	}
}

func (d Dialog) Post(text string) {
	d.presenter.Text(modutui.RoleAssistant, text)
}

func (d Dialog) PostResult(title, output string, err error) {
	text := strings.TrimSpace(output)
	if err != nil {
		if text != "" {
			text += "\n"
		}
		text += "error: " + err.Error()
	}
	if text == "" {
		text = "completed"
	}
	d.Post(title + "\n\n" + text)
}

func (d Dialog) Status(status string) {
	d.client.SetStatus(status, d.statusTTL)
}
