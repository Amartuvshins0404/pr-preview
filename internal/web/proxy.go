package web

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/Amartuvshins0404/pr-preview/internal/build"
	"github.com/Amartuvshins0404/pr-preview/internal/state"
)

type previewKey struct{}

func (s *Server) newProxy() *httputil.ReverseProxy {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			p, _ := pr.In.Context().Value(previewKey{}).(state.Preview)
			pr.SetURL(&url.URL{Scheme: "http", Host: build.ProjectName(p.Slug) + ":" + strconv.Itoa(p.Port)})
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Set("X-Forwarded-For", clientIP(pr.In))
			pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
			pr.Out.Header.Set("X-Forwarded-Proto", "https")
			stripSessionCookie(pr.Out.Header)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			p, _ := r.Context().Value(previewKey{}).(state.Preview)
			if r.Context().Err() == nil {
				slog.Warn("proxy error", "slug", p.Slug, "err", err)
			}
			s.status(w, r, p, true, http.StatusBadGateway)
		},
	}
}

func (s *Server) serveProxy(w http.ResponseWriter, r *http.Request, p state.Preview) {
	s.proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), previewKey{}, p)))
}
