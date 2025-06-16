package mock

import (
	"context"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/config"
	localesandlanguages "github.com/Borislavv/traefik-http-cache-plugin/pkg/locale"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
)

const (
	minStrLen = 8    // Minimum random string length for GenerateRandomString
	maxStrLen = 1024 // Maximum random string length for GenerateRandomString
)

// GenerateRandomRequests produces a slice of *model.Request for use in tests and benchmarks.
// Each request gets a unique combination of project, domain, language, and tags.
func GenerateRandomRequests(num int) []*model.Request {
	i := 0
	list := make([]*model.Request, 0, num)

	// Iterate over all possible language and project ID combinations until num requests are created
	for {
		for _, lng := range localesandlanguages.LanguageCodeList() {
			for projectID := 1; projectID < 1000; projectID++ {
				if i >= num {
					return list
				}
				req, err := model.NewManualRequest(
					[]byte(strconv.Itoa(projectID)), // Project ID as []byte
					[]byte("1x001.com"),             // Fixed domain for testing
					[]byte(lng),                     // Language
					[][]byte{ // Tags (variation for entropy)
						[]byte(`betting`),
						[]byte(`betting_null`),
						[]byte(`betting_null_sport`),
						[]byte(`betting_null_sport_` + strconv.Itoa(projectID)),
						[]byte(`betting_null_sport_` + strconv.Itoa(projectID) + `_` + strconv.Itoa(i)),
						[]byte(`betting_null_sport_` + strconv.Itoa(projectID) + `_` + strconv.Itoa(i) + `_` + strconv.Itoa(i)),
					},
				)
				if err != nil {
					panic(err)
				}
				list = append(list, req)
				i++
			}
		}
	}
}

// GenerateRandomResponses generates a list of *model.Response, each linked to a random request and containing
// random body data. Used for stress/load/benchmark testing of cache systems.
func GenerateRandomResponses(cfg *config.Cache, num int) []*model.Response {
	headers := http.Header{}
	headers.Add("Accept", "application/json")
	headers.Add("Content-Type", "application/json")

	list := make([]*model.Response, 0, num)
	for _, req := range GenerateRandomRequests(num) {
		data := model.NewData(200, headers, []byte(GenerateRandomString()))
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

// GenerateRandomString returns a random ASCII string of length between minStrLen and maxStrLen.
func GenerateRandomString() string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	length := rand.Intn(maxStrLen-minStrLen+1) + minStrLen

	var sb strings.Builder
	sb.Grow(length)

	for i := 0; i < length; i++ {
		sb.WriteByte(letters[rand.Intn(len(letters))])
	}

	return sb.String()
}
