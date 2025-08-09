package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/valyala/fasthttp"
)

type dummyBackend struct {
	name string
	cfg  *config.Backend
}

func (d *dummyBackend) ID() string                       { return d.name }
func (d *dummyBackend) Name() string                     { return d.name }
func (d *dummyBackend) IsHealthy() error                 { return nil }
func (d *dummyBackend) Cfg() *config.Backend             { return d.cfg }
func (d *dummyBackend) Update(cfg *config.Backend) error { d.cfg = cfg; return nil }
func (d *dummyBackend) Fetch(rule *config.Rule, inCtx *fasthttp.RequestCtx, inReq *fasthttp.Request) (*fasthttp.Request, *fasthttp.Response, func(*fasthttp.Request, *fasthttp.Response), error) {
	return inReq, &fasthttp.Response{}, func(*fasthttp.Request, *fasthttp.Response) {}, nil
}

func BenchmarkTestNoAlloc(b *testing.B) {
	cfgs := []*config.Backend{
		{Name: "a", Rate: 1_000_000},
		{Name: "b", Rate: 1_000_000},
	}
	var backs []*Backend
	for _, c := range cfgs {
		db := &dummyBackend{name: c.Name, cfg: c}
		var b Backend = db
		backs = append(backs, &b)
	}
	cl, _ := New(context.Background(), &config.AtomicCache{})
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < 10_0000; i++ {
		_, _, rel, err := cl.Fetch(nil, &ctx, &req)
		if err != nil {
			b.Fatal(err)
		}
		if rel == nil {
			b.Fatal("no releaser")
		}
	}
	b.StopTimer()
	b.ReportAllocs()
	time.Sleep(10 * time.Millisecond)
}
