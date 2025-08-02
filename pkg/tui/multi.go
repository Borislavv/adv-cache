package dashboard

import (
	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
)

var defaultLineColors = []ui.Color{
	ui.ColorGreen,
	ui.ColorCyan,
	ui.ColorMagenta,
	ui.ColorRed,
	ui.ColorYellow,
	ui.ColorBlue,
}

type MultiPlotPanel struct {
	title string
	keys  []string
	plot  *widgets.Plot
}

func NewMultiPlotPanel(title string, keys []string) *MultiPlotPanel {
	plot := widgets.NewPlot()
	plot.Title = title
	plot.Marker = widgets.MarkerBraille
	plot.AxesColor = ui.ColorWhite
	plot.Data = make([][]float64, len(keys))
	plot.LineColors = make([]ui.Color, len(keys))
	plot.DataLabels = make([]string, len(keys))

	for i, k := range keys {
		plot.DataLabels[i] = k
		plot.LineColors[i] = defaultLineColors[i%len(defaultLineColors)]
		plot.Data[i] = []float64{0, 0} // Placeholder until update
	}

	return &MultiPlotPanel{
		title: title,
		keys:  keys,
		plot:  plot,
	}
}

func (m *MultiPlotPanel) Update(metrics map[string]*MetricBuffer) {
	var data [][]float64
	for _, key := range m.keys {
		buf, ok := metrics[key]
		if !ok {
			data = append(data, []float64{0, 0})
			continue
		}
		pts := safeData(buf.pts())
		data = append(data, pts)
	}
	m.plot.Data = data
}

func safeData(pts []float64) []float64 {
	if len(pts) < 2 {
		return []float64{0, 0} // или дублируем последнее значение
	}
	return pts
}

func (m *MultiPlotPanel) Draw() ui.Drawable {
	return m.plot
}

func (m *MultiPlotPanel) Name() string {
	return m.title
}
