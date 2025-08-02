// metrics_dashboard.go
// Full-screen TUI dashboard for in-memory Prometheus metrics
package tui

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics/keyword"
	metrics2 "github.com/VictoriaMetrics/metrics"
	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
	"github.com/rs/zerolog"
)

// dataPoint holds a timestamped metric value
type dataPoint struct {
	t time.Time
	v float64
}

// RunMetricsDashboard displays a dashboard of RPS, AvgDuration, and Memory Usage
// over the last 5 minutes, plus a sorted table of current values. Exit with 'q' or 'Q'.
func RunMetricsDashboard(ctx context.Context, interval time.Duration, memLimitBytes float64) (err error) {
	// Handle panic
	defer func() {
		if r := recover(); r != nil {
			ui.Close()
			fmt.Fprintf(os.Stderr, "Dashboard panic: %v\n", r)
			err = fmt.Errorf("dashboard panic: %v", r)
		}
	}()

	// Init UI
	if err := ui.Init(); err != nil {
		return fmt.Errorf("termui init: %w", err)
	}
	defer ui.Close()

	// suppress logs
	oldOut, oldErr := os.Stdout, os.Stderr
	nullF, _ := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	os.Stdout, os.Stderr = nullF, nullF
	zerolog.SetGlobalLevel(zerolog.Disabled)
	defer func() { nullF.Close(); os.Stdout, os.Stderr = oldOut, oldErr }()

	// layout
	w, h := ui.TerminalDimensions()
	heightGraph := (h - 2) / 2

	// RPS plot
	rpsPlot := widgets.NewPlot()
	rpsPlot.Title = "RPS (last 5m)"
	rpsPlot.SetRect(0, 0, w, heightGraph)
	rpsPlot.DataLabels = []string{"RPS"}
	rpsPlot.MaxVal = 250000
	rpsPlot.Marker = widgets.MarkerBraille

	// Avg Duration plot
	avgPlot := widgets.NewPlot()
	avgPlot.Title = "Avg Duration (ms, last 5m)"
	avgPlot.SetRect(0, heightGraph, w, 2*heightGraph)
	avgPlot.DataLabels = []string{"AvgDur"}
	avgPlot.MaxVal = 25 // ms
	avgPlot.Marker = widgets.MarkerBraille

	// Memory Usage plot
	memPlot := widgets.NewPlot()
	memPlot.Title = fmt.Sprintf("Mem Usage (MB, limit=%.1fGB) (last 5m)", memLimitBytes/1024/1024/1024)
	memPlot.SetRect(0, 2*heightGraph, w, h-1)
	memPlot.DataLabels = []string{"Mem"}
	// convert limit to MB for scale
	memPlot.MaxVal = memLimitBytes / 1024 / 1024
	memPlot.Marker = widgets.MarkerBraille

	// Table of current metrics
	table := widgets.NewTable()
	table.Title = "Current Metrics"
	table.SetRect(0, h-1, w, h)
	table.RowSeparator = true

	// data stores
	rpsData := []dataPoint{}
	avgData := []dataPoint{}
	memData := []dataPoint{}

	// ticker
	t := time.NewTicker(interval)
	defer t.Stop()

	// Instruction paragraph
	instr := widgets.NewParagraph()
	instr.Text = "Press 'q' or 'Q' to exit dashboard"
	instr.SetRect(0, h-1, w, h)
	instr.Border = false

	events := ui.PollEvents()

	for {
		select {
		case <-ctx.Done():
			return nil
		case e := <-events:
			if e.Type == ui.KeyboardEvent {
				if e.ID == "q" || e.ID == "Q" || e.ID == "<C-c>" {
					// Exit dashboard on 'q', 'Q', or Ctrl+C
					return nil
				}
			}

		case now := <-t.C:
			// gather metrics
			var buf bytes.Buffer
			metrics2.WritePrometheus(&buf, false)
			// parse
			m := map[string]float64{}
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
				v, _ := strconv.ParseFloat(f[1], 64)
				m[f[0]] = v
			}
			// current table rows
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			table.Rows = [][]string{{"Metric", "Value"}}
			for _, k := range keys {
				val := m[k]
				if k == keyword.AvgDuration { // ns to ms
					val = val / 1e6
				}
				if k == keyword.MapMemoryUsageMetricName { // bytes to MB
					val = val / 1024 / 1024
				}
				table.Rows = append(table.Rows, []string{k, fmt.Sprintf("%.2f", val)})
			}

			// append to histories
			if v, ok := m[keyword.RPS]; ok {
				rpsData = append(rpsData, dataPoint{now, v})
			}
			if v, ok := m[keyword.AvgDuration]; ok {
				avgData = append(avgData, dataPoint{now, v / 1e6})
			}
			if v, ok := m[keyword.MapMemoryUsageMetricName]; ok {
				memData = append(memData, dataPoint{now, v / 1024 / 1024})
			}
			// prune older than 5m
			cut := now.Add(-5 * time.Minute)
			rpsData = prune(rpsData, cut)
			avgData = prune(avgData, cut)
			memData = prune(memData, cut)

			// build plot arrays
			rpsArr := pts(rpsData)
			avgArr := pts(avgData)
			memArr := pts(memData)

			// assign to plots
			rpsPlot.Data = [][]float64{rpsArr}
			avgPlot.Data = [][]float64{avgArr}
			memPlot.Data = [][]float64{memArr}

			// render all
			ui.Clear()
			ui.Render(rpsPlot, avgPlot, memPlot, table)
		}
		// always render instruction
		ui.Render(instr)
	}
}

// prune removes points older than cutoff
func prune(data []dataPoint, cutoff time.Time) []dataPoint {
	i := 0
	for ; i < len(data); i++ {
		if data[i].t.After(cutoff) {
			break
		}
	}
	return data[i:]
}

// pts converts dataPoints to slice of float64
func pts(data []dataPoint) []float64 {
	o := make([]float64, len(data))
	for i, d := range data {
		o[i] = d.v
	}
	return o
}
