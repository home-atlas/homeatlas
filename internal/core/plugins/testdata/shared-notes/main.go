//go:build wasip1

package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/home-atlas/homeatlas/sdk/pluginstorage"
)

func main() {
	var request struct {
		View  string            `json:"view"`
		Input map[string]string `json:"input"`
	}
	if json.NewDecoder(os.Stdin).Decode(&request) != nil {
		os.Exit(1)
	}
	key := request.Input["key"]
	var text string
	var err error
	switch request.View {
	case "private_save":
		err = pluginstorage.Set(key, request.Input["value"])
		text = "Nota privada guardada."
	case "private_read", "read":
		var found bool
		if request.View == "private_read" {
			text, found, err = pluginstorage.Get(key)
		} else {
			text, found, err = pluginstorage.GetShared(key)
		}
		if !found {
			text = "No hay una nota accesible con esa clave."
		} else if strings.TrimSpace(text) == "" {
			text = "La nota está vacía."
		}
	case "save":
		err = pluginstorage.SetShared(key, request.Input["value"])
		text = "Nota guardada."
	case "list":
		var keys []string
		var next string
		keys, next, err = pluginstorage.ListShared(request.Input["after"], 32)
		text = strings.Join(keys, "\n")
		if len(keys) == 0 {
			text = "No hay notas accesibles."
		}
		if next != "" {
			text += "\nContinuar después de: " + next
		}
	case "grant":
		err = pluginstorage.GrantShared(key, request.Input["user"], request.Input["permission"])
		text = "Permiso actualizado."
	case "revoke":
		err = pluginstorage.RevokeShared(key, request.Input["user"])
		text = "Acceso revocado."
	case "grants":
		var grants []pluginstorage.Grant
		var next string
		grants, next, err = pluginstorage.ListSharedGrants(key, request.Input["after"], 32)
		var lines []string
		for _, g := range grants {
			permission := "lectura"
			if g.Permission == "write" {
				permission = "edición"
			}
			lines = append(lines, g.User+": "+permission)
		}
		text = strings.Join(lines, "\n")
		if len(grants) == 0 {
			text = "No has concedido acceso a otras personas."
		}
		if next != "" {
			text += "\nContinuar después de: " + next
		}
	default:
		os.Exit(1)
	}
	if err != nil {
		os.Exit(1)
	}

	if json.NewEncoder(os.Stdout).Encode(struct {
		Text string `json:"text"`
	}{text}) != nil {
		os.Exit(1)
	}
}
