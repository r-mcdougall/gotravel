package httpserver

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/r-mcdougall/gotravel/internal/platform/health"
	"github.com/r-mcdougall/gotravel/internal/platform/metrics"
)

func NewRouter(log *slog.Logger, m *metrics.Metrics, routes func(router chi.Router)) http.Handler {
	stack := chi.Chain(RequestID, m.Middleware, AccessLog(log), Recoverer(log))

	root := chi.NewRouter()
	root.NotFound(stack.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "route not found")
	}).ServeHTTP)
	root.Get("/healthz", health.Handler)
	root.Handle("/metrics", m.Handler())
	root.Group(func(r chi.Router) {
		r.Use(stack...)
		routes(r)
	})

	return root
}
