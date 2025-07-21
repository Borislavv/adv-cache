package main

import (
	"fmt"
	"github.com/valyala/fasthttp"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	pathPrefix = "/api/v2/pagedata"
	iters      = 10_000_000
	workers    = 512 // tune for max RPS
	timeout    = 5 * time.Second
)

var counter int64 = 1

var client = &fasthttp.Client{
	MaxConnsPerHost:               10_000,
	MaxIdleConnDuration:           30 * time.Second,
	ReadTimeout:                   timeout,
	WriteTimeout:                  timeout,
	DisableHeaderNamesNormalizing: true,
	NoDefaultUserAgentHeader:      true,
}

var reqPool = sync.Pool{
	New: func() any {
		return fasthttp.AcquireRequest()
	},
}

var respPool = sync.Pool{
	New: func() any {
		return fasthttp.AcquireResponse()
	},
}

func buildPath(buf []byte, i int64) []byte {
	buf = append(buf, pathPrefix...)
	buf = append(buf, "?project[id]=285&domain=1x001.com&language=en"...)
	buf = append(buf, "&choice[name]=betting&choice[choice][name]=betting_live"...)
	buf = append(buf, "&choice[choice][choice][name]=betting_live_null"...)

	buf = append(buf, "&choice[choice][choice][choice][name]=betting_live_null_"...)
	buf = strconv.AppendInt(buf, i, 10)

	buf = append(buf, "&choice[choice][choice][choice][choice][name]=betting_live_null_"...)
	buf = strconv.AppendInt(buf, i, 10)
	buf = append(buf, '_')
	buf = strconv.AppendInt(buf, i, 10)

	buf = append(buf, "&choice[choice][choice][choice][choice][choice][name]=betting_live_null_"...)
	buf = strconv.AppendInt(buf, i, 10)
	buf = append(buf, '_')
	buf = strconv.AppendInt(buf, i, 10)
	buf = append(buf, '_')
	buf = strconv.AppendInt(buf, i, 10)

	buf = append(buf, "&choice[choice][choice][choice][choice][choice][choice]=null"...)
	return buf
}

func worker(wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		i := atomic.AddInt64(&counter, 1)
		if i > iters {
			return
		}

		req := reqPool.Get().(*fasthttp.Request)
		resp := respPool.Get().(*fasthttp.Response)

		fullPath := buildPath([]byte{}, i)

		req.Reset()
		req.SetRequestURIBytes(fullPath)
		req.Header.SetMethod(fasthttp.MethodGet)
		req.Header.SetHost("0.0.0.0:8021") // <- Укажи явно host
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.SetContentType("application/json")

		err := client.Do(req, resp)
		if err != nil {
			fmt.Printf("❌ i=%d error: %v\n", i, err)
		}

		reqPool.Put(req)
		respPool.Put(resp)
	}
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	start := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker(&wg)
	}

	wg.Wait()
	fmt.Printf("✅ Completed %d requests in %s\n", iters, time.Since(start))
}
