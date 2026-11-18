package httpadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/modules/application"
	"github.com/home-atlas/homeatlas/internal/core/modules/domain"
)

type Route struct {
	Method     string
	Path       string
	Permission string
	Handler    http.HandlerFunc
}

type Transport struct {
	registry *application.Registry
	routes   map[string][]Route
	order    []string
}

func New(registry *application.Registry) *Transport {
	return &Transport{registry: registry, routes: map[string][]Route{}}
}
func ValidateRoutes(id string, capabilities []string, routes []Route) error {
	method := regexp.MustCompile(`^[A-Z]+$`)
	seen := map[string]bool{}
	for _, route := range routes {
		key := route.Method + " " + route.Path
		declared := false
		for _, cap := range capabilities {
			if cap == route.Permission && strings.HasPrefix(cap, id+".") {
				declared = true
			}
		}
		if !declared || route.Handler == nil || !method.MatchString(route.Method) || !strings.HasPrefix(route.Path, "/") || strings.ContainsAny(route.Path, " ?#\t\n\r") || seen[key] {
			return fmt.Errorf("%s: invalid route %q", id, key)
		}
		seen[key] = true
	}
	return nil
}
func (t *Transport) Register(id string, routes []Route) {
	t.routes[id] = routes
	t.order = append(t.order, id)
}
func (t *Transport) Mount(mux *http.ServeMux) {
	for _, id := range t.order {
		for _, route := range t.routes[id] {
			path := "/api/" + id + strings.TrimSuffix(route.Path, "/")
			mux.HandleFunc(route.Method+" "+path, func(w http.ResponseWriter, req *http.Request) {
				err := t.registry.WithEnabled(req.Context(), id, func() {
					if Authorize(w, req, route.Permission) {
						route.Handler(w, req)
					}
				})
				switch {
				case errors.Is(err, domain.ErrUnknownModule):
					http.NotFound(w, req)
				case errors.Is(err, domain.ErrHouseholdRequired):
					Authorize(w, req, route.Permission)
				case err != nil:
					JSON(w, 503, map[string]string{"error": "modules_unavailable"})
				}
			})
		}
	}
}
func Authorize(w http.ResponseWriter, req *http.Request, permission string) bool {
	if _, ok := access.Current(req.Context()); !ok {
		JSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication_required"})
		return false
	}
	if !access.Allows(req.Context(), permission) {
		JSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied"})
		return false
	}
	return true
}

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
