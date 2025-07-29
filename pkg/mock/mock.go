package mock

import (
	"bytes"
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"math/rand"
	"runtime"
	"strconv"
)

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

func GenerateRandomEntryPointer(cfg *config.Cache, backend repository.Backender, path []byte) *model.VersionPointer {
	i := rand.Intn(10_000_000)

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
	entry, err := model.NewEntryManual(cfg, path, query, &queryHeaders, backend.RevalidatorMaker())
	if err != nil {
		panic(err)
	}
	entry.SetPayload(path, query, &queryHeaders, &responseHeaders, copiedBodyBytes(i), 200)

	return model.NewVersionPointer(entry)
}

func GenerateEntryPointersConsecutive(cfg *config.Cache, backend repository.Backender, path []byte, num int) []*model.VersionPointer {
	res := make([]*model.VersionPointer, 0, num)

	i := 0
	for {
		if i >= num {
			break
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
		entry, err := model.NewEntryManual(cfg, path, query, &queryHeaders, backend.RevalidatorMaker())
		if err != nil {
			panic(err)
		}
		entry.SetPayload(path, query, &queryHeaders, &responseHeaders, copiedBodyBytes(i), 200)

		res = append(res, model.NewVersionPointer(entry))

		i++
	}

	return res
}

func StreamEntryPointersConsecutive(ctx context.Context, cfg *config.Cache, backend repository.Backender, path []byte, num int) <-chan *model.VersionPointer {
	outCh := make(chan *model.VersionPointer, runtime.GOMAXPROCS(0)*4)
	go func() {
		defer close(outCh)

		realDefer := func() {
			for entry := range outCh {
				entry.Remove()
			}
		}

		i := 0
		for {
			select {
			case <-ctx.Done():
				go realDefer()
				return
			default:
				if i >= num {
					go realDefer()
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

				// releaser is unnecessary due to all entries will escape to heap
				entry, err := model.NewEntryManual(cfg, path, query, &queryHeaders, backend.RevalidatorMaker())
				if err != nil {
					panic(err)
				}
				entry.SetPayload(path, query, &queryHeaders, &responseHeaders, copiedBodyBytes(i), 200)

				outCh <- model.NewVersionPointer(entry)
				i++
			}
		}
	}()
	return outCh
}

func StreamEntriesConsecutive(ctx context.Context, cfg *config.Cache, backend repository.Backender, path []byte, num int) <-chan *model.VersionPointer {
	outCh := make(chan *model.VersionPointer, runtime.GOMAXPROCS(0)*4*10000)
	go func() {
		defer close(outCh)
		i := 0
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if i >= num {
					i = 0
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

				// releaser is unnecessary due to all entries will escape to heap
				entry, err := model.NewEntryManual(cfg, path, query, &queryHeaders, backend.RevalidatorMaker())
				if err != nil {
					panic(err)
				}
				entry.SetPayload(path, query, &queryHeaders, &responseHeaders, copiedBodyBytes(i), 200)

				outCh <- model.NewVersionPointer(entry)
				i++
			}
		}
	}()
	return outCh
}

// copiedBodyBytes returns a random ASCII string of length between minStrLen and maxStrLen.
func copiedBodyBytes(idx int) []byte {
	return bytes.ReplaceAll(responseBytes, []byte("{{...}}"), []byte(strconv.Itoa(idx)))
}
