package httpx

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/observability"
)

// Middleware is the standard decorator shape used across the API.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first argument is the outermost layer,
// which is the order they appear to read in at the call site.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// statusRecorder captures what was actually written so the access log reports
// the real status and size rather than assuming 200.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.status = code
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer for
// flushing and deadline control.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// RequestID assigns a correlation id, honouring a trusted inbound header so a
// trace survives the hop from the CDN or load balancer.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
			if id == "" || len(id) > 128 {
				id = uuid.NewString()
			}
			ctx := observability.WithRequestID(r.Context(), id)
			w.Header().Set("X-Request-ID", id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Recover turns a panic into a 500 so one bad handler cannot kill the process
// and take every in-flight request with it.
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// A client that hung up mid-write surfaces as a panic in
					// some stacks; it is not a server fault worth alerting on.
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					observability.LoggerFrom(r.Context(), logger).Error("panic recovered",
						slog.Any("panic", rec),
						slog.String("path", r.URL.Path),
						slog.String("stack", string(debug.Stack())))
					Respond(w, r, Internal("Внутренняя ошибка сервера"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog records one line per request after it completes.
func AccessLog(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			level := slog.LevelInfo
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case rec.status >= 400:
				level = slog.LevelWarn
			}
			observability.LoggerFrom(r.Context(), logger).Log(r.Context(), level, "http",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Duration("took", time.Since(start)))
		})
	}
}

// Timeout bounds handler execution so a slow query cannot pin a connection
// forever. Handlers must honour ctx for this to do anything useful.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SecurityHeaders sets the defaults appropriate for a JSON API.
func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cross-Origin-Resource-Policy", "same-site")
			// The API returns data, never markup; deny framing outright.
			h.Set("X-Frame-Options", "DENY")
			next.ServeHTTP(w, r)
		})
	}
}

// CORS allows the configured browser origins. Native clients ignore this, but
// the future web storefront will not.
func CORS(allowed []string) Middleware {
	allowSet := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		allowSet[strings.TrimSpace(o)] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				_, ok := allowSet[origin]
				if !ok {
					_, ok = allowSet["*"]
				}
				if ok {
					h := w.Header()
					h.Set("Access-Control-Allow-Origin", origin)
					h.Set("Access-Control-Allow-Credentials", "true")
					h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-None-Match, X-Request-ID, Idempotency-Key")
					h.Set("Access-Control-Expose-Headers", "ETag, X-Request-ID, Retry-After")
					h.Set("Access-Control-Max-Age", "600")
					h.Add("Vary", "Origin")
				}
			}
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ipRateLimiter is a per-client token bucket with idle eviction.
//
// This is deliberately in-process: it is the cheap first line that survives
// Redis being down. Cross-replica limits belong at the edge (CDN/WAF), where
// they can reject before the request costs us a goroutine.
type ipRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucketEntry
	rps     rate.Limit
	burst   int
	idleFor time.Duration
}

type bucketEntry struct {
	limiter *rate.Limiter
	seen    time.Time
}

// RateLimitByIP limits sustained request rate per client address.
func RateLimitByIP(rps, burst int, idleFor time.Duration) Middleware {
	rl := &ipRateLimiter{
		buckets: make(map[string]*bucketEntry),
		rps:     rate.Limit(rps),
		burst:   burst,
		idleFor: idleFor,
	}
	go rl.evictLoop()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !rl.allow(clientIP(r)) {
				w.Header().Set("Retry-After", "1")
				Respond(w, r, RateLimited("Слишком много запросов, попробуйте через секунду"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (rl *ipRateLimiter) allow(key string) bool {
	rl.mu.Lock()
	entry, ok := rl.buckets[key]
	if !ok {
		entry = &bucketEntry{limiter: rate.NewLimiter(rl.rps, rl.burst)}
		rl.buckets[key] = entry
	}
	entry.seen = time.Now()
	limiter := entry.limiter
	rl.mu.Unlock()
	return limiter.Allow()
}

func (rl *ipRateLimiter) evictLoop() {
	ticker := time.NewTicker(rl.idleFor)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-rl.idleFor)
		rl.mu.Lock()
		for k, v := range rl.buckets {
			if v.seen.Before(cutoff) {
				delete(rl.buckets, k)
			}
		}
		rl.mu.Unlock()
	}
}

// clientIP resolves the caller address. X-Forwarded-For is only meaningful
// behind a proxy that overwrites it; the leftmost entry is used and the result
// is always a bare IP so a spoofed port cannot fragment the bucket.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
		return real
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
