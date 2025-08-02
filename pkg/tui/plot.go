package dashboard

import (
	"fmt"
	"strings"
	"time"

	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
)

type PlotPanel struct {
	title  string
	metric string
	plot   *widgets.Plot
	window time.Duration
	color  ui.Color
}

func NewPlotPanel(title, metric string) Panel {
	plot := widgets.NewPlot()
	plot.Title = fmt.Sprintf("%s (last %s)", title, "5m") // default view
	plot.Marker = widgets.MarkerBraille
	plot.AxesColor = ui.ColorWhite
	plot.LineColors = []ui.Color{ui.ColorCyan}

	return &PlotPanel{
		title:  title,
		metric: metric,
		plot:   plot,
		window: 5 * time.Minute,
		color:  ui.ColorCyan,
	}
}

func (p *PlotPanel) SetWindow(window time.Duration) {
	p.window = window
	p.plot.Title = fmt.Sprintf("%s (last %s)", p.title, window.String())
}

func (p *PlotPanel) Update(metrics map[string]*MetricBuffer) {
	buf, ok := metrics[p.metric]
	if !ok {
		p.plot.Data = [][]float64{{0, 0}}
		return
	}

	pts := safeData(buf.GetPoints(p.window))

	// Auto switch MB/GB for memory panel
	if strings.Contains(strings.ToLower(p.plot.Title), "memory") {
		if maxFloat64(pts) > 1024*1024*1024 {
			for i := range pts {
				pts[i] /= 1024 * 1024 * 1024
			}
			p.plot.Title = fmt.Sprintf("%s (last %s)", p.title, p.window.String())
		} else {
			for i := range pts {
				pts[i] /= 1024 * 1024
			}
			p.plot.Title = fmt.Sprintf("%s (last %s)", p.title, p.window.String())
		}
	}

	p.plot.Data = [][]float64{pts}
}

func (p *PlotPanel) Draw() ui.Drawable {
	return p.plot
}

func (p *PlotPanel) Name() string {
	return p.title
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
