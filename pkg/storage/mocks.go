package storage

import (
	"context"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"runtime"
	"strconv"
	"time"
)

var respTemplate = `{
  "data": {
    "type": "seo/pagedata",
    "attributes": {
      "title": "1xBet[%d]: It repeats some phrases multiple times. This is a long description for SEO page data.",
      "description": "1xBet[%d]: his is a long description for SEO page data. This description is intentionally made verbose to increase the JSON payload size.",
      "metaRobots": [],
      "hierarchyMetaRobots": [
        {
          "name": "robots",
          "content": "noindex, nofollow"
        }
      ],
      "ampPageUrl": null,
      "alternativeLinks": [],
      "alternateMedia": [],
      "customCanonical": null,
      "metas": [],
      "siteName": null
    }
  }
}`

func LoadMocks(ctx context.Context, config config.Config, storage Storage, num int) {
	go func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()

		log.Info().Msg("[mocks] mock data start loading")
		defer log.Info().Msg("[mocks] mocked data finished loading")

		path := []byte("/api/v2/pagedata")
		for entry := range streamEntryPointersConsecutive(ctx, config, path, num) {
			storage.Set(entry)
		}
	}()
}

func GetSingleMock(i int, path []byte, cfg config.Config) *model.Entry {
	query := make([]byte, 0, 512)
	query = append(query, []byte("project[id]=285")...)
	query = append(query, []byte("&domain=1x001.com")...)
	query = append(query, []byte("&language=en")...)
	query = append(query, []byte("&choice[name]=betting")...)
	query = append(query, []byte("&choice[choice][name]=betting_live")...)
	query = append(query, []byte("&choice[choice][choice][name]=betting_live_null")...)
	query = append(query, []byte("&choice[choice][choice][choice][name]=betting_live_null_"+strconv.Itoa(i))...)
	query = append(query, []byte("&choice[choice][choice][choice][choice][name]betting_live_null_"+strconv.Itoa(i)+"_"+strconv.Itoa(i))...)
	query = append(query, []byte("&choice[choice][choice][choice][choice][choice][name]betting_live_null_"+strconv.Itoa(i)+"_"+strconv.Itoa(i)+"_"+strconv.Itoa(i))...)
	query = append(query, []byte("&choice[choice][choice][choice][choice][choice][choice]=null")...)

	queryHeaders := [][2][]byte{
		{[]byte("Host"), []byte("0.0.0.0:8020")},
		{[]byte("Accept-Encoding"), []byte("gzip, deflate, br")},
		{[]byte("Accept-Language"), []byte("en-US,en;q=0.9")},
		{[]byte("Content-Type"), []byte("application/json")},
	}

	responseHeaders := [][2][]byte{
		{[]byte("Content-Type"), []byte("application/json")},
		{[]byte("Vary"), []byte("Accept-Encoding, Accept-Language")},
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()

	req.URI().SetPathBytes(path)
	req.URI().SetQueryStringBytes(query)

	for _, kv := range queryHeaders {
		req.Header.AddBytesKV(kv[0], kv[1])
	}

	for _, kv := range responseHeaders {
		resp.Header.AddBytesKV(kv[0], kv[1])
	}

	resp.SetStatusCode(200)
	resp.SetBody(copiedBodyBytes(i))
	resp.Header.SetLastModified(time.Now())

	entry, err := model.NewMockEntry(cfg, req, resp)
	if err != nil {
		panic(err)
	}

	fasthttp.ReleaseRequest(req)
	fasthttp.ReleaseResponse(resp)

	return entry
}

func streamEntryPointersConsecutive(ctx context.Context, cfg config.Config, path []byte, num int) <-chan *model.Entry {
	outCh := make(chan *model.Entry, runtime.GOMAXPROCS(0)*4)
	go func() {
		defer close(outCh)

		i := 1
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if i > num {
					return
				}
				query := make([]byte, 0, 512)
				query = append(query, []byte("project[id]=285")...)
				query = append(query, []byte("&domain=1x001.com")...)
				query = append(query, []byte("&language=en")...)
				query = append(query, []byte("&choice[name]=betting")...)
				query = append(query, []byte("&choice[choice][name]=betting_live")...)
				query = append(query, []byte("&choice[choice][choice][name]=betting_live_null")...)
				query = append(query, []byte("&choice[choice][choice][choice][name]=betting_live_null_"+strconv.Itoa(i))...)
				query = append(query, []byte("&choice[choice][choice][choice][choice][name]betting_live_null_"+strconv.Itoa(i)+"_"+strconv.Itoa(i))...)
				query = append(query, []byte("&choice[choice][choice][choice][choice][choice][name]betting_live_null_"+strconv.Itoa(i)+"_"+strconv.Itoa(i)+"_"+strconv.Itoa(i))...)
				query = append(query, []byte("&choice[choice][choice][choice][choice][choice][choice]=null")...)

				queryHeaders := [][2][]byte{
					{[]byte("Host"), []byte("0.0.0.0:8020")},
					{[]byte("Accept-Encoding"), []byte("gzip, deflate, br")},
					{[]byte("Accept-Language"), []byte("en-US,en;q=0.9")},
					{[]byte("Content-Type"), []byte("application/json")},
				}

				responseHeaders := [][2][]byte{
					{[]byte("Content-Type"), []byte("application/json")},
					{[]byte("Vary"), []byte("Accept-Encoding, Accept-Language")},
				}

				req := fasthttp.AcquireRequest()
				resp := fasthttp.AcquireResponse()

				req.URI().SetPathBytes(path)
				req.URI().SetQueryStringBytes(query)

				for _, kv := range queryHeaders {
					req.Header.AddBytesKV(kv[0], kv[1])
				}

				for _, kv := range responseHeaders {
					resp.Header.AddBytesKV(kv[0], kv[1])
				}

				resp.SetStatusCode(200)
				resp.SetBody(copiedBodyBytes(i))
				resp.Header.SetLastModified(time.Now())

				entry, err := model.NewMockEntry(cfg, req, resp)
				if err != nil {
					panic(err)
				}

				fasthttp.ReleaseRequest(req)
				fasthttp.ReleaseResponse(resp)

				outCh <- entry
				i++
			}
		}
	}()
	return outCh
}

// copiedBodyBytes returns a random ASCII string of length between minStrLen and maxStrLen.
func copiedBodyBytes(idx int) []byte {
	return []byte(fmt.Sprintf(respTemplate, idx, idx))
}
