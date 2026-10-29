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
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		os.Exit(1)
	}
	key := request.Input["key"]
	var text string
	var err error
	switch request.View {
	case "save":
		err = pluginstorage.Set(key, request.Input["value"])
		text = "Nota guardada."
	case "read":
		var found bool
		text, found, err = pluginstorage.Get(key)
		if !found {
			text = "No hay una nota con esa clave."
		}
	case "list":
		var keys []string
		var next string
		keys, next, err = pluginstorage.List(request.Input["after"], 32)
		text = strings.Join(keys, "\n")
		if len(keys) == 0 {
			text = "No hay notas."
		}
		if next != "" {
			text += "\nContinuar después de: " + next
		}
	case "delete":
		err = pluginstorage.Delete(key)
		text = "Nota retirada."
	default:
		os.Exit(1)
	}
	if err != nil {
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Text string `json:"text"`
	}{text}); err != nil {
		os.Exit(1)
	}
}
