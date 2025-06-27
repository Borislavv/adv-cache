package middleware

//import (
//	"bytes"
//	"context"
//	"errors"
//	cfg "github.com/Borislavv/advanced-cache/pkg/config"
//	"github.com/Borislavv/advanced-cache/pkg/model"
//	"github.com/Borislavv/advanced-cache/pkg/repository"
//	"github.com/Borislavv/advanced-cache/pkg/storage"
//	"github.com/Borislavv/advanced-cache/pkg/storage/cache"
//	"github.com/rs/zerolog/log"
//	"net/http"
//	"time"
//)
//
//var internalServerErrorJson = []byte(`{"error": {"message": "Internal server error."}}`)
//
//type Cache struct {
//	BackendUrl string `mapstructure:"SEO_URL"`
//	// RevalidateBeta is a value which will be used for generate
//	// random time point for refresh response (must be from 0.1 to 0.9).
//	// Don't use absolute values like 0 and 1 due it will be leading to CPU peaks usage.
//	//  - beta = 0.5 — regular, good for start value
//	//  - beta = 1.0 — aggressive refreshing
//	//  - beta = 0.0 — disables refreshing
//	RevalidateBeta            float64       `mapstructure:"REVALIDATE_BETA"`
//	RevalidateInterval        time.Duration `mapstructure:"REVALIDATE_INTERVAL"`
//	InitStorageLengthPerShard int           `mapstructure:"INIT_STORAGE_LEN_PER_SHARD"`
//	EvictionAlgo              string        `mapstructure:"EVICTION_ALGO"`
//	MemoryFillThreshold       float64       `mapstructure:"MEMORY_FILL_THRESHOLD"`
//	MemoryLimit               float64       `mapstructure:"MEMORY_LIMIT"`
//}
//
//func CreateConfig() *Cache {
//	return &Cache{
//		InitStorageLengthPerShard: 256,
//		EvictionAlgo:              string(cache.LRU),
//		MemoryFillThreshold:       0.97,
//		MemoryLimit:               1024 * 1024 * 256,
//		RevalidateBeta:            0.4,
//		RevalidateInterval:        time.Minute * 30,
//		BackendUrl:                "https://seo-master.lux.kube.xbet.lan/api/v2/pagedata",
//	}
//}
//
//type Plugin struct {
//	ctx     context.Context
//	next    http.Handler
//	name    string
//	config  *Cache
//	seoRepo repository.Seo
//	storage storage.Storage
//}
//
//func New(ctx context.Context, next http.Handler, config *Cache, name string) http.Handler {
//	cacheCfg := &cfg.Cache{
//		BackendUrl:                config.BackendUrl,
//		RevalidateBeta:            config.RevalidateBeta,
//		RevalidateInterval:        config.RevalidateInterval,
//		InitStorageLengthPerShard: config.InitStorageLengthPerShard,
//		EvictionAlgo:              config.EvictionAlgo,
//		MemoryFillThreshold:       config.MemoryFillThreshold,
//		MemoryLimit:               config.MemoryLimit,
//	}
//
//	return &Plugin{
//		ctx:     ctx,
//		next:    next,
//		name:    name,
//		config:  config,
//		storage: storage.New(ctx, cacheCfg),
//		seoRepo: repository.NewSeo(cacheCfg),
//	}
//}
//
//func (p *Plugin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
//	ctx, cancel := context.WithTimeout(p.ctx, time.Second)
//	defer cancel()
//
//	req, err := p.extractRequest(r)
//	if err != nil {
//		w.WriteHeader(http.StatusBadRequest)
//		if _, werr := w.Write(internalServerErrorJson); werr != nil {
//			log.Error().Err(err).Msg("error while writing response into http.ResponseWriter")
//		}
//		return
//	}
//
//	if resp, found := p.storage.Get(ctx, req); found {
//		w.WriteHeader(http.StatusOK)
//		if _, werr := w.Write(resp.Body()); werr != nil {
//			w.WriteHeader(http.StatusInternalServerError)
//			log.Err(werr).Msg("error while writing response into http.ResponseWriter")
//			return
//		}
//		for headerName, v := range resp.Headers() {
//			for _, headerValue := range v {
//				w.Header().Add(headerName, headerValue)
//			}
//		}
//		return
//	}
//
//	clonedWriter := newCaptureResponseWriter(w)
//
//	p.next.ServeHTTP(clonedWriter, r)
//
//	if clonedWriter.statusCode != http.StatusOK {
//		return
//	}
//
//	ctx, cancel := context.WithTimeout(p.ctx, time.Millisecond*400)
//	defer cancel()
//
//	resp, err := model.NewResponse(p.config.Response, clonedWriter.Header().Clone(), req, clonedWriter.body.Bytes(), func() ([]byte, error) {
//		return p.seoRepo.Fetch()
//	})
//	if err != nil {
//		log.Error().Err(err).Msg("failed to make response")
//		w.WriteHeader(http.StatusInternalServerError)
//		if _, werr := w.Write(internalServerErrorJson); werr != nil {
//			log.Err(werr).Msg("error while writing response into http.ResponseWriter")
//		}
//		return
//	}
//
//	p.storage.Set(ctx, resp)
//}
//
//func (p *Plugin) extractRequest(r *http.Request) (*model.Request, error) {
//	project := r.URL.Query().Get("project")
//	if project == "" {
//		return nil, errors.New("project is missing")
//	}
//	domain := r.URL.Query().Get("domain")
//	if domain == "" {
//		return nil, errors.New("domain is missing")
//	}
//	language := r.URL.Query().Get("language")
//	if language == "" {
//		return nil, errors.New("language is missing")
//	}
//	choice := r.URL.Query().Get("choice")
//	if choice == "" {
//		return nil, errors.New("choice is missing")
//	}
//	return model.NewRequest(project, domain, language, choice), nil
//}
//
//var _ http.ResponseWriter = &captureResponseWriter{}
//
//type captureResponseWriter struct {
//	wrapped     http.ResponseWriter
//	body        *bytes.Buffer
//	statusCode  int
//	headers     http.Header
//	wroteHeader bool
//}
//
//func newCaptureResponseWriter(w http.ResponseWriter) *captureResponseWriter {
//	return &captureResponseWriter{
//		wrapped:    w,
//		body:       new(bytes.Buffer),
//		statusCode: http.StatusOK,
//		headers:    make(http.Header),
//	}
//}
//
//func (w *captureResponseWriter) Header() http.Header {
//	// intercept and work with our copy of headers
//	return w.headers
//}
//
//func (w *captureResponseWriter) WriteHeader(code int) {
//	if w.wroteHeader {
//		return // prevent double WriteHeader
//	}
//	w.statusCode = code
//	w.wroteHeader = true
//
//	// copy headers to the underlying writer before writing header
//	for k, v := range w.headers {
//		for _, vv := range v {
//			w.wrapped.Header().Add(k, vv)
//		}
//	}
//	w.wrapped.WriteHeader(code)
//}
//
//func (w *captureResponseWriter) Write(b []byte) (int, error) {
//	// ensure WriteHeader is called if not already done
//	if !w.wroteHeader {
//		w.WriteHeader(http.StatusOK)
//	}
//	w.body.Write(b) // Save to buffer
//	return w.wrapped.Write(b)
//}
