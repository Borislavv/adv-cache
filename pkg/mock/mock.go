package mock

import (
	"bytes"
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"net/http"
	"runtime"
	"strconv"
)

// GenerateRandomRequests produces a slice of *model.Request for use in tests and benchmarks.
// Each request gets a unique combination of project, domain, language, and tags.
func GenerateRandomRequests(cfg *config.Cache, path []byte, num int) []*model.Request {
	i := 0
	list := make([]*model.Request, 0, num)

	// Iterate over all possible language and project ID combinations until num requests are created
	for {
		//for _, lng := range localesandlanguages.LanguageCodeList() {
		//for projectID := 1; projectID < 1000; projectID++ {
		if i >= num {
			return list
		}
		req := model.NewRequest(
			cfg,
			path,
			[][2][]byte{
				{[]byte("project[id]"), []byte("285")},
				{[]byte("domain"), []byte("1x001.com")},
				{[]byte("language"), []byte("en")},
				{[]byte("choice[name]"), []byte("betting")},
				{[]byte("choice[choice][name]"), []byte("betting_live")},
				{[]byte("choice[choice][choice][name]"), []byte("betting_live_null")},
				{[]byte("choice[choice][choice][choice][name]"), []byte("betting_live_null_" + strconv.Itoa(i))},
				{[]byte("choice[choice][choice][choice][choice][name]"), []byte("betting_live_null_" + strconv.Itoa(i) + "_" + strconv.Itoa(i))},
				{[]byte("choice[choice][choice][choice][choice][choice][name]"), []byte("betting_live_null_" + strconv.Itoa(i) + "_" + strconv.Itoa(i) + "_" + strconv.Itoa(i))},
				{[]byte("choice[choice][choice][choice][choice][choice][choice]"), []byte("null")},
			},
			[][2][]byte{
				{[]byte("Host"), []byte("0.0.0.0:8020")},
				{[]byte("Accept-Encoding"), []byte("gzip, deflate, br")},
				{[]byte("Accept-Language"), []byte("en-US,en;q=0.9")},
				{[]byte("Content-Type"), []byte("application/json")},
			},
		)
		list = append(list, req)
		i++
		//}
		//}
	}
}

func StreamRandomRequests(ctx context.Context, cfg *config.Cache, path []byte, num int) <-chan *model.Request {
	i := 0
	out := make(chan *model.Request)

	go func() {
		defer close(out)

		// Iterate over all possible language and project ID combinations until num requests are created
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if i >= num {
					return
				}
				req := model.NewRequest(
					cfg,
					path,
					[][2][]byte{
						{[]byte("project[id]"), []byte("285")},
						{[]byte("domain"), []byte("1x001.com")},
						{[]byte("language"), []byte("en")},
						{[]byte("choice[name]"), []byte("betting")},
						{[]byte("choice[choice][name]"), []byte("betting_live")},
						{[]byte("choice[choice][choice][name]"), []byte("betting_live_null")},
						{[]byte("choice[choice][choice][choice][name]"), []byte("betting_live_null_" + strconv.Itoa(i))},
						{[]byte("choice[choice][choice][choice][choice][name]"), []byte("betting_live_null_" + strconv.Itoa(i) + "_" + strconv.Itoa(i))},
						{[]byte("choice[choice][choice][choice][choice][choice][name]"), []byte("betting_live_null_" + strconv.Itoa(i) + "_" + strconv.Itoa(i) + "_" + strconv.Itoa(i))},
						{[]byte("choice[choice][choice][choice][choice][choice][choice]"), []byte("null")},
					},
					[][2][]byte{
						{[]byte("Host"), []byte("0.0.0.0:8020")},
						{[]byte("Accept-Encoding"), []byte("gzip, deflate, br")},
						{[]byte("Accept-Language"), []byte("en-US,en;q=0.9")},
						{[]byte("Content-Type"), []byte("application/json")},
					},
				)
				out <- req
				i++
			}
		}
	}()

	return out
}

func StreamSeqEntries(ctx context.Context, cfg *config.Cache, backend repository.Backender, path []byte, num int) <-chan *model.Entry {
	outCh := make(chan *model.Entry, runtime.GOMAXPROCS(0)*4)
	go func() {
		i := 0
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if i >= num {
					return
				}
				query := make([]byte, 0, 512)
				query = append(query, []byte("?project[id]=285")...)
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

				// releaser is unnecessary due to all entries will escape to heap
				entry, _, err := model.NewEntryManual(cfg, path, query, queryHeaders, backend.RevalidatorMaker())
				if err != nil {
					panic(err)
				}
				entry.SetPayload(path, query, queryHeaders, responseHeaders, copiedBodyBytes(i), 200)

				outCh <- entry
				i++
			}
		}
	}()
	return outCh
}

func StreamRandomResponses(ctx context.Context, cfg *config.Cache, path []byte, num int) <-chan *model.Response {
	outCh := make(chan *model.Response)

	go func() {
		defer close(outCh)

		for req := range StreamRandomRequests(ctx, cfg, path, num) {
			headers := http.Header{}
			headers.Add("Content-Type", "application/json")
			headers.Add("Vary", "Accept-Encoding, Accept-Language")

			data := model.NewData(req.Rule(), http.StatusOK, headers, copiedBodyBytes(0))
			resp, err := model.NewResponse(
				data, req, cfg,
				func(ctx context.Context) (*model.Data, error) {
					// Dummy revalidator; always returns the same data.
					return data, nil
				},
			)
			if err != nil {
				panic(err)
			}
			outCh <- resp
		}
	}()

	return outCh
}

// GenerateRandomResponses generates a list of *model.Response, each linked to a random request and containing
// random body data. Used for stress/load/benchmark testing of cache systems.
func GenerateRandomResponses(cfg *config.Cache, path []byte, num int) []*model.Response {
	list := make([]*model.Response, 0, num)
	for _, req := range GenerateRandomRequests(cfg, path, num) {
		headers := http.Header{}
		headers.Add("Content-Type", "application/json")
		headers.Add("Vary", "Accept-Encoding, Accept-Language")
		data := model.NewData(req.Rule(), http.StatusOK, headers, copiedBodyBytes(0))
		resp, err := model.NewResponse(
			data, req, cfg,
			func(ctx context.Context) (*model.Data, error) {
				// Dummy revalidator; always returns the same data.
				return data, nil
			},
		)
		if err != nil {
			panic(err)
		}
		list = append(list, resp)
	}
	return list
}

var responseBytes = []byte(`{
  "data": {
    "type": "seo/pagedata",
    "attributes": {
      "title": "1xBet[{{...}}]: It repeats some phrases multiple times. This is a long description for SEO page data.",
      "description": "1xBet[{{...}}]: his is a long description for SEO page data. This description is intentionally made verbose to increase the JSON payload size.",
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
}
`)

// copiedBodyBytes returns a random ASCII string of length between minStrLen and maxStrLen.
func copiedBodyBytes(idx int) []byte {
	return bytes.ReplaceAll(responseBytes, []byte("{{...}}"), []byte(strconv.Itoa(idx)))
}
