// Package dashboard provides an interactive TUI for Prometheus metrics visualization.
package dashboard

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics/keyword"
	metrics2 "github.com/VictoriaMetrics/metrics"
	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
)

type Panel interface {
	Update(metrics map[string]*MetricBuffer)
	Draw() ui.Drawable
	Name() string
}

type MetricBuffer struct {
	points []dataPoint
	mu     sync.Mutex
}

type dataPoint struct {
	t time.Time
	v float64
}

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
			currentWindow: maxWindow,
		},
		panels: []Panel{
			NewPlotPanel("RPS", keyword.RPS),
			NewPlotPanel("Avg Duration (ms)", keyword.AvgDuration),
			NewPlotPanel("Memory Usage (MB)", keyword.MapMemoryUsageMetricName),
			NewPlotPanel("Cache Length", keyword.MapLength),
			NewMultiPlotPanel("Total Events", []string{keyword.Hits, keyword.Misses, keyword.Errored, keyword.Panicked, keyword.Proxied}),
			NewInfoPanel(),
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
		case "1":
			d.state.currentWindow = time.Minute
		case "5":
			d.state.currentWindow = 5 * time.Minute
		case "6":
			d.state.currentWindow = 6 * time.Hour
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
		d.getOrCreateMetric(key).Append(now, val, d.state.currentWindow)
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

func (m *MetricBuffer) pts() []float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]float64, len(m.points))
	for i, p := range m.points {
		out[i] = p.v
	}
	return out
}

// NewBottomPanel creates a bottom legend panel.
func NewBottomPanel() Panel {
	p := widgets.NewParagraph()
	p.Title = "Legend"
	p.Text = "[Hits](fg:green)  [Misses](fg:red)  [Errored](fg:yellow)  [Panicked](fg:magenta)  [Proxied](fg:cyan)[q] Quit  [h] Help  [1|5|6] Window  [resize] Layout"
	p.Border = false
	return &bottomPanel{p: p}
}

type bottomPanel struct {
	p *widgets.Paragraph
}

func (l *bottomPanel) Update(_ map[string]*MetricBuffer) {}
func (l *bottomPanel) Draw() ui.Drawable                 { return l.p }
func (l *bottomPanel) Name() string                      { return "BottomPanel" }

func (d *Dashboard) render() {
	w, h := ui.TerminalDimensions()
	grid := ui.NewGrid()
	grid.SetRect(0, 0, w, h)

	var leftCol, rightCol, bottom []interface{}

	for _, p := range d.panels {
		if mp, ok := p.(*HelpPanel); ok {
			mp.SetVisible(d.state.helpVisible)
		}
		p.Update(d.metrics)
		switch p.Name() {
		case "RPS", "Memory Usage (MB)":
			leftCol = append(leftCol, ui.NewRow(0.5, p.Draw()))
		case "Avg Duration (ms)", "Cache Length":
			rightCol = append(rightCol, ui.NewRow(0.5, p.Draw()))
		case "Total Events":
			bottom = append(bottom, ui.NewRow(0.85, p.Draw()))
		case "InfoPanel", "BottomPanel":
			bottom = append(bottom, ui.NewRow(0.075, p.Draw()))
		case "HelpPanel":
			if d.state.helpVisible {
				bottom = append(bottom, ui.NewRow(0.075, p.Draw()))
			}
		}
	}

	grid.Set(
		ui.NewRow(0.55,
			ui.NewCol(0.5, leftCol...),
			ui.NewCol(0.5, rightCol...),
		),
		ui.NewRow(0.45, bottom...),
	)
	ui.Render(grid)
}
