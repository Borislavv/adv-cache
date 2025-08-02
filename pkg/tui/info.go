package dashboard

import (
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics/keyword"
	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
	"time"
)

type InfoPanel struct {
	p      *widgets.Paragraph
	fields []string
}

func NewInfoPanel() *InfoPanel {
	p := widgets.NewParagraph()
	p.Title = "Current Metrics"
	p.BorderStyle.Fg = ui.ColorYellow
	p.TextStyle.Fg = ui.ColorWhite
	return &InfoPanel{
		p: p,
		fields: []string{
			keyword.RPS,
			keyword.AvgDuration,
			keyword.MapMemoryUsageMetricName,
			keyword.MapLength,
		},
	}
}

func (i *InfoPanel) Update(metrics map[string]*MetricBuffer) {
	t := time.Now()
	var lines []string
	for _, k := range i.fields {
		buf, ok := metrics[k]
		if !ok || len(buf.points) == 0 {
			lines = append(lines, fmt.Sprintf("%-20s: --", k))
			continue
		}
		last := buf.points[len(buf.points)-1]
		age := t.Sub(last.t).Truncate(time.Millisecond)
		lines = append(lines, fmt.Sprintf("%-20s: %8.2f (age %s)", k, last.v, age))
	}
	i.p.Text = "" + joinLines(lines)
}

func (i *InfoPanel) Draw() ui.Drawable {
	return i.p
}

func (i *InfoPanel) Name() string {
	return "InfoPanel"
}

func joinLines(lines []string) string {
	return fmt.Sprintf("%s", string([]byte(fmt.Sprint(lines))))[1 : len(fmt.Sprint(lines))-1]
}
