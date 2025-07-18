package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/andybalholm/brotli"
)

const (
	baseURL           = "http://0.0.0.0:8021"
	maxI              = 10_000_000
	startI            = 1
	delay             = 0 * time.Millisecond // можно увеличить, чтобы не перегружать
	httpClientTimeout = 5 * time.Second
)

var expectedTemplate = `{
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

func main() {
	client := &http.Client{Timeout: httpClientTimeout}

	for i := startI; i <= maxI; i++ {
		path := "/api/v2/pagedata" +
			"?project[id]=285" +
			"&domain=1x001.com" +
			"&language=en" +
			"&choice[name]=betting" +
			"&choice[choice][name]=betting_live" +
			"&choice[choice][choice][name]=betting_live_null" +
			"&choice[choice][choice][choice][name]=betting_live_null_" + strconv.Itoa(i) +
			"&choice[choice][choice][choice][choice][name]=betting_live_null_" + strconv.Itoa(i) + "_" + strconv.Itoa(i) +
			"&choice[choice][choice][choice][choice][choice][name]=betting_live_null_" + strconv.Itoa(i) + "_" + strconv.Itoa(i) + "_" + strconv.Itoa(i) +
			"&choice[choice][choice][choice][choice][choice][choice]=null"

		req, err := http.NewRequest("GET", baseURL+path, nil)
		if err != nil {
			fmt.Printf("❌ Request creation failed at i=%d: %v\n", i, err)
			continue
		}

		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("❌ Request failed at i=%d: %v\n", i, err)
			continue
		}

		var body []byte
		if resp.Header.Get("Content-Encoding") == "br" {
			body, err = io.ReadAll(brotli.NewReader(resp.Body))
		} else {
			body, err = io.ReadAll(resp.Body)
		}
		resp.Body.Close()

		if err != nil {
			fmt.Printf("❌ Failed to read response at i=%d: %v\n", i, err)
			continue
		}

		expected := fmt.Sprintf(expectedTemplate, i, i)

		if !bytes.Equal([]byte(expected), bytes.TrimSpace(body)) {
			fmt.Printf("❌ MISMATCH at i=%d\nRequest:\n%s\nExpected:\n%s\nActual:\n%s\n\n", i, path, expected, string(body))
		} else {
			fmt.Printf("✅ MATCH at i=%d\n", i)
		}

		time.Sleep(delay)
	}
}
