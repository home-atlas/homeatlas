package collections

import (
	"net/http"

	"github.com/home-atlas/homeatlas/internal/core/modules"
)

func (m *Module) shares(w http.ResponseWriter, r *http.Request) {
	out, err := m.Shares(r.Context(), r.PathValue("id"))
	if err != nil {
		collectionError(w, err)
		return
	}
	modules.JSON(w, 200, out)
}
func (m *Module) changeShare(w http.ResponseWriter, r *http.Request, remove bool) {
	err := m.ChangeShare(r.Context(), r.PathValue("id"), r.PathValue("user"), remove)
	if err != nil {
		collectionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}
func (m *Module) share(w http.ResponseWriter, r *http.Request)   { m.changeShare(w, r, false) }
func (m *Module) unshare(w http.ResponseWriter, r *http.Request) { m.changeShare(w, r, true) }
