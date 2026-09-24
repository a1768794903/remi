package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/zeromicro/go-zero/rest"
	"remi/server/internal/auth"
	"remi/server/internal/config"
	"remi/server/internal/health"
)

func BuildServer(cfg config.Config, audioHandler http.Handler) *rest.Server {
	server := rest.MustNewServer(rest.RestConf{Host: hostFromAddr(cfg.HTTPAddr), Port: portFromAddr(cfg.HTTPAddr), Timeout: cfg.ReadHeaderTimeout.Milliseconds()})
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/healthz", Handler: health.Handler(func() bool { return true }).ServeHTTP},
		{Method: http.MethodGet, Path: "/readyz", Handler: health.Handler(func() bool { return true }).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/audio/stream", Handler: auth.Middleware(cfg.AuthMode)(audioHandler).ServeHTTP},
		{Method: http.MethodGet, Path: "/v4/listen", Handler: auth.Middleware(cfg.AuthMode)(audioHandler).ServeHTTP},
	})
	return server
}

func hostFromAddr(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return "0.0.0.0"
	}
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			if i == 0 {
				return "0.0.0.0"
			}
			return addr[:i]
		}
	}
	return "0.0.0.0"
}

func portFromAddr(addr string) int {
	parts := strings.Split(addr, ":")
	value, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || value == 0 {
		return 8080
	}
	return value
}
