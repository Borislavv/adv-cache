package dashboard

import (
	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
)

type HelpPanel struct {
	visible bool
	p       *widgets.Paragraph
}

func NewHelpPanel() *HelpPanel {
	p := widgets.NewParagraph()
	p.Title = "Help"
	p.Text = `[q]       quit
[h]       toggle help
[1|5|6]   time window (1m, 5m, 6h)
[resize]  adjust layout`
	p.BorderStyle.Fg = ui.ColorCyan
	p.TextStyle.Fg = ui.ColorWhite
	return &HelpPanel{
		p:       p,
		visible: false,
	}
}

func (h *HelpPanel) Update(_ map[string]*MetricBuffer) {
	// visibility toggled via dashboard state
}

func (h *HelpPanel) Draw() ui.Drawable {
	if !h.visible {
		return nil
	}
	return h.p
}

func (h *HelpPanel) Name() string {
	return "HelpPanel"
}

func (h *HelpPanel) SetVisible(v bool) {
	h.visible = v
}

func (h *HelpPanel) IsVisible() bool {
	return h.visible
}
