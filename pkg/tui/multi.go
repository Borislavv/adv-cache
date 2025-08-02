package dashboard

import (
	"fmt"
	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
	"time"
)

var defaultLineColors = []ui.Color{
	ui.ColorCyan,    // 1
	ui.ColorGreen,   // 2
	ui.ColorYellow,  // 3
	ui.ColorRed,     // 4
	ui.ColorMagenta, // 5
	ui.ColorBlue,    // 6+
}

type MultiPlotPanel struct {
	title  string
	keys   []string
	plot   *widgets.Plot
	window time.Duration
}

func NewMultiPlotPanel(title string, keys []string) *MultiPlotPanel {
	plot := widgets.NewPlot()
	plot.Marker = widgets.MarkerBraille
	plot.AxesColor = ui.ColorWhite
	plot.Data = make([][]float64, len(keys))
	plot.LineColors = make([]ui.Color, len(keys))
	plot.DataLabels = make([]string, len(keys))

	for i, k := range keys {
		plot.DataLabels[i] = k
		plot.LineColors[i] = defaultLineColors[i%len(defaultLineColors)]
		plot.Data[i] = []float64{0, 0}
	}

	return &MultiPlotPanel{
		title:  title,
		keys:   keys,
		plot:   plot,
		window: 5 * time.Minute,
	}
}

func (m *MultiPlotPanel) SetWindow(window time.Duration) {
	m.window = window
	m.plot.Title = fmt.Sprintf("%s (last %s)", m.title, window.String())
}

func (m *MultiPlotPanel) Update(metrics map[string]*MetricBuffer) {
	var data [][]float64
	for _, key := range m.keys {
		buf, ok := metrics[key]
		if !ok {
			data = append(data, []float64{0, 0})
			continue
		}
		pts := safeData(buf.GetPoints(m.window))
		data = append(data, pts)
	}
	m.plot.Data = data
}

func (m *MultiPlotPanel) Draw() ui.Drawable {
	return m.plot
}

func (m *MultiPlotPanel) Name() string {
	return m.title
}

func safeData(pts []float64) []float64 {
	if len(pts) < 2 {
		return []float64{0, 0} // или дублируем последнее значение
	}
	return pts
}
