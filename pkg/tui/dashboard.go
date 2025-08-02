// Package dashboard provides an interactive TUI for Prometheus metrics visualization.
package dashboard

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"github.com/gizak/termui/v3/widgets"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics/keyword"
	metrics2 "github.com/VictoriaMetrics/metrics"
	ui "github.com/gizak/termui/v3"
)

type Panel interface {
	Update(metrics map[string]*MetricBuffer)
	Draw() ui.Drawable
	Name() string
	SetWindow(time.Duration)
}

type MetricBuffer struct {
	points []dataPoint
	mu     sync.Mutex
}

type dataPoint struct {
	t time.Time
	v float64
}

func NewBottomPanel() Panel {
	p := widgets.NewParagraph()
	p.Title = "Legend"
	p.Text = "[Hits](fg:green)  [Misses](fg:red)  [Errored](fg:yellow)  [Panicked](fg:magenta)  [Proxied](fg:cyan)\n" +
		"[q] Quit  [h] Help  [1|5|6] Window  [+] Zoom Out  [-] Zoom In"
	p.Border = false
	return &bottomPanel{p: p}
}

type bottomPanel struct {
	p *widgets.Paragraph
}

func (l *bottomPanel) SetWindow(window time.Duration)    {}
func (l *bottomPanel) Update(_ map[string]*MetricBuffer) {}
func (l *bottomPanel) Draw() ui.Drawable                 { return l.p }
func (l *bottomPanel) Name() string                      { return "BottomPanel" }

type DashboardState struct {
	currentWindow time.Duration
	helpVisible   bool
	collapsed     bool
}

type Dashboard struct {
	ctx        context.Context
	cancel     context.CancelFunc
	interval   time.Duration
	maxWindow  time.Duration
	metrics    map[string]*MetricBuffer
	memLimitMB float64
	panels     []Panel
	state      DashboardState
}

func NewDashboard(ctx context.Context, interval, maxWindow time.Duration, memLimitBytes float64) *Dashboard {
	if maxWindow < 5*time.Minute {
		maxWindow = 5 * time.Minute
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Dashboard{
		ctx:        ctx,
		cancel:     cancel,
		interval:   interval,
		maxWindow:  maxWindow,
		metrics:    make(map[string]*MetricBuffer),
		memLimitMB: memLimitBytes / 1024 / 1024,
		state: DashboardState{
			currentWindow: 5 * time.Minute,
		},
		panels: []Panel{
			NewPlotPanel("RPS", keyword.RPS),
			NewPlotPanel("Avg Duration (ms)", keyword.AvgDuration),
			NewPlotPanel("Memory Usage (MB)", keyword.MapMemoryUsageMetricName),
			NewPlotPanel("Cache Length", keyword.MapLength),
			NewPlotPanel("CPU Usage (%)", "cpu_usage"),
			NewMultiPlotPanel("Total Events", []string{keyword.Hits, keyword.Misses, keyword.Errored, keyword.Panicked, keyword.Proxied}),
			NewBottomPanel(),
			NewHelpPanel(),
		},
	}
}

func (d *Dashboard) Run() error {
	if err := ui.Init(); err != nil {
		return fmt.Errorf("termui init failed: %w", err)
	}
	defer func() {
		ui.Clear()
		ui.Close()
		fmt.Println("[dashboard] released terminal")
	}()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	events := ui.PollEvents()
	go func() {
		for range time.Tick(time.Second) {
			curr := getCPUPercent()
			d.getOrCreateMetric("cpu_usage").Append(time.Now(), curr, d.maxWindow)
		}
	}()

	for {
		select {
		case <-d.ctx.Done():
			return nil
		case e := <-events:
			if err := d.handleEvent(e); err != nil {
				return err
			}
		case now := <-ticker.C:
			d.updateMetrics(now)
			d.render()
		}
	}
}

func getCPUPercent() float64 {
	return float64(runtime.NumGoroutine()) * 1.0 // simple placeholder
}

func (d *Dashboard) Stop() {
	d.cancel()
}

func (d *Dashboard) handleEvent(e ui.Event) error {
	switch e.Type {
	case ui.KeyboardEvent:
		switch e.ID {
		case "q", "Q", "<C-c>":
			d.Stop()
		case "h":
			d.state.helpVisible = !d.state.helpVisible
		case "+":
			d.state.currentWindow *= 2
			if d.state.currentWindow > d.maxWindow {
				d.state.currentWindow = d.maxWindow
			}
		case "-":
			d.state.currentWindow /= 2
			if d.state.currentWindow < time.Minute {
				d.state.currentWindow = time.Minute
			}
		}
	case ui.ResizeEvent:
		ui.Clear()
		d.render()
	}
	return nil
}

func (d *Dashboard) updateMetrics(now time.Time) {
	var buf bytes.Buffer
	metrics2.WritePrometheus(&buf, false)
	s := bufio.NewScanner(&buf)

	for s.Scan() {
		l := s.Text()
		if strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		if len(f) != 2 {
			continue
		}
		val, err := strconv.ParseFloat(f[1], 64)
		if err != nil {
			continue
		}
		key := f[0]
		d.getOrCreateMetric(key).Append(now, val, d.maxWindow)
	}
}

func (d *Dashboard) getOrCreateMetric(name string) *MetricBuffer {
	if buf, ok := d.metrics[name]; ok {
		return buf
	}
	buf := &MetricBuffer{}
	d.metrics[name] = buf
	return buf
}

func (m *MetricBuffer) Append(t time.Time, v float64, window time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tm := t.Add(-window)
	m.points = append(m.points, dataPoint{t, v})
	for len(m.points) > 0 && m.points[0].t.Before(tm) {
		m.points = m.points[1:]
	}
}

func (m *MetricBuffer) GetPoints(window time.Duration) []float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	tm := time.Now().Add(-window)
	out := make([]float64, 0, len(m.points))
	for _, p := range m.points {
		if p.t.After(tm) {
			out = append(out, p.v)
		}
	}
	return out
}

func (d *Dashboard) render() {
	w, h := ui.TerminalDimensions()
	grid := ui.NewGrid()
	grid.SetRect(0, 0, w, h)

	var leftCol, rightCol, bottom []interface{}

	for _, p := range d.panels {
		if mp, ok := p.(*HelpPanel); ok {
			mp.SetVisible(d.state.helpVisible)
		}
		p.SetWindow(d.state.currentWindow)
		p.Update(d.metrics)

		switch p.Name() {
		case "RPS", "Memory Usage (MB)", "Memory Usage (GB)", "CPU Usage (%)":
			leftCol = append(leftCol, ui.NewRow(0.33, p.Draw()))
		case "Avg Duration (ms)", "Cache Length":
			rightCol = append(rightCol, ui.NewRow(0.5, p.Draw()))
		case "Total Events":
			bottom = append(bottom, ui.NewRow(0.8, p.Draw()))
		case "BottomPanel":
			bottom = append(bottom, ui.NewRow(0.2, p.Draw()))
		case "HelpPanel":
			if d.state.helpVisible {
				bottom = append(bottom, ui.NewRow(0.2, p.Draw()))
			}
		}
	}

	grid.Set(
		ui.NewRow(0.6,
			ui.NewCol(0.5, leftCol...),
			ui.NewCol(0.5, rightCol...),
		),
		ui.NewRow(0.4, bottom...),
	)
	ui.Render(grid)
}
