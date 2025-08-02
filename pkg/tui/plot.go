package dashboard

import (
	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
	"strings"
)

type PlotPanel struct {
	title    string
	metric   string
	plot     *widgets.Plot
	lastUnit string
}

func NewPlotPanel(title, metric string) Panel {
	return &PlotPanel{
		title:  title,
		metric: metric,
		plot:   widgets.NewPlot(),
	}
}

func (p *PlotPanel) Update(metrics map[string]*MetricBuffer) {
	buf, ok := metrics[p.metric]
	if !ok {
		p.plot.Data = [][]float64{{0, 0}}
		return
	}

	pts := safeData(buf.pts())

	// Авто-переключение MB <-> GB
	if strings.Contains(strings.ToLower(p.plot.Title), "memory") {
		if maxFloat64(pts) > 1024*1024*1024 {
			for i := range pts {
				pts[i] /= 1024 * 1024 * 1024
			}
			p.plot.Title = "Memory Usage (GB)"
		} else {
			for i := range pts {
				pts[i] /= 1024 * 1024
			}
			p.plot.Title = "Memory Usage (MB)"
		}
	}

	p.plot.Data = [][]float64{pts}
}

func maxFloat64(a []float64) float64 {
	var max float64
	for _, v := range a {
		if v > max {
			max = v
		}
	}
	return max
}

func (p *PlotPanel) Draw() ui.Drawable {
	return p.plot
}

func (p *PlotPanel) Name() string {
	return p.title
}
