// Command mockrecord is a capturing reverse proxy for NEV-59 review fix §2.
//
// It sits between paylessforai-app and a REAL provider, forwards every
// inference request, and auto-saves each upstream response as a mockprovider
// fixture file in --out-dir. Run the full live scenarios (parent ticket:
// basic + tool-use + streaming per cell) with RECORD=1 and every response
// lands on disk; curate the recorded files into test/e2e/mocks/matrix/.
//
// Non-streaming JSON responses are saved directly as fixtures:
// {"status":200,"headers":{...},"body":{...}}.
// SSE streams are saved raw as .sse.txt (needs manual conversion to a
// fixture) because a byte stream is not replayable as a single JSON body.
//
// Auth material is NEVER written: Authorization / x-api-key / api-key
// headers are stripped from saved files. Bodies are stored verbatim —
// review them before committing (they may echo your prompt text).
//
// Usage (one proxy per provider):
// mockrecord -listen 127.0.0.1:19574 -target https://openrouter.ai/api/v1 \
// -strip-prefix /openrouter/api/v1 -out-dir test/e2e/mocks/matrix/recorded \
// -prefix openrouter
//
// Control endpoints (not forwarded): GET /__record/list, POST /__record/reset.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

var sensitiveHeaders = []string{"authorization", "x-api-key", "x-goog-api-key", "api-key"}

func main() {
	listen := flag.String("listen", "127.0.0.1:19574", "capture proxy listen address")
	target := flag.String("target", "", "real upstream base URL (e.g. https://openrouter.ai/api/v1)")
	stripPrefix := flag.String("strip-prefix", "", "URL path prefix to strip before forwarding (the fake mount path)")
	outDir := flag.String("out-dir", "", "directory for recorded fixture files")
	prefix := flag.String("prefix", "record", "filename prefix for recorded fixtures")
	flag.Parse()
	if *target == "" || *outDir == "" {
		log.Fatal("mockrecord: -target and -out-dir are required")
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("mockrecord: mkdir %s: %v", *outDir, err)
	}
	rec := &recorder{
		target:      strings.TrimSuffix(*target, "/"),
		stripPrefix: *stripPrefix,
		outDir:      *outDir,
		prefix:      *prefix,
		client:      &http.Client{},
	}
	log.Printf("mockrecord: %s -> %s (fixtures in %s)", *listen, *target, *outDir)
	if err := http.ListenAndServe(*listen, rec); err != nil {
		log.Fatal(err)
	}
}

type recorder struct {
	target      string
	stripPrefix string
	outDir      string
	prefix      string
	client      *http.Client
	mu          sync.Mutex
	files       []string
	counter     atomic.Int64
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/__record/list":
		r.mu.Lock()
		files := append([]string(nil), r.files...)
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
		return
	case "/__record/reset":
		r.mu.Lock()
		r.files = nil
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"reset":true}`)
		return
	case "/healthz":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
		return
	}

	upstreamPath := req.URL.Path
	if r.stripPrefix != "" {
		upstreamPath = strings.TrimPrefix(upstreamPath, r.stripPrefix)
		if upstreamPath == "" {
			upstreamPath = "/"
		}
	}
	upstreamURL := r.target + upstreamPath
	if req.URL.RawQuery != "" {
		upstreamURL += "?" + req.URL.RawQuery
	}

	body, _ := io.ReadAll(io.LimitReader(req.Body, 32<<20))
	upstream, err := http.NewRequest(req.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for key, values := range req.Header {
		for _, value := range values {
			upstream.Header.Add(key, value)
		}
	}
	resp, err := r.client.Do(upstream)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))

	r.save(req, resp, respBody)

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

func (r *recorder) save(req *http.Request, resp *http.Response, body []byte) {
	n := r.counter.Add(1)
	leaf := strings.Trim(strings.ReplaceAll(strings.Trim(req.URL.Path, "/"), "/", "-"), "-")
	if leaf == "" {
		leaf = "root"
	}
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		name := fmt.Sprintf("%s-%03d-%s.sse.txt", r.prefix, n, leaf)
		if err := os.WriteFile(filepath.Join(r.outDir, name), body, 0o644); err != nil {
			log.Printf("mockrecord: write %s: %v", name, err)
			return
		}
		r.record(name)
		log.Printf("mockrecord: %s %s -> %d (stream, raw %d bytes in %s)", req.Method, req.URL.Path, resp.StatusCode, len(body), name)
		return
	}
	var raw json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		name := fmt.Sprintf("%s-%03d-%s.raw.txt", r.prefix, n, leaf)
		if err := os.WriteFile(filepath.Join(r.outDir, name), body, 0o644); err != nil {
			log.Printf("mockrecord: write %s: %v", name, err)
			return
		}
		r.record(name)
		log.Printf("mockrecord: %s %s -> %d (non-JSON, raw %d bytes in %s)", req.Method, req.URL.Path, resp.StatusCode, len(body), name)
		return
	}
	headers := map[string]string{}
	for key, values := range resp.Header {
		lower := strings.ToLower(key)
		sensitive := false
		for _, s := range sensitiveHeaders {
			if lower == s {
				sensitive = true
				break
			}
		}
		if sensitive || len(values) == 0 {
			continue
		}
		headers[key] = values[0]
	}
	fixture, _ := json.MarshalIndent(map[string]any{
		"status":  resp.StatusCode,
		"headers": headers,
		"body":    raw,
	}, "", "  ")
	name := fmt.Sprintf("%s-%03d-%s.json", r.prefix, n, leaf)
	if err := os.WriteFile(filepath.Join(r.outDir, name), append(fixture, '\n'), 0o644); err != nil {
		log.Printf("mockrecord: write %s: %v", name, err)
		return
	}
	r.record(name)
	log.Printf("mockrecord: %s %s -> %d (fixture %s)", req.Method, req.URL.Path, resp.StatusCode, name)
}

func (r *recorder) record(name string) {
	r.mu.Lock()
	r.files = append(r.files, name)
	r.mu.Unlock()
}
