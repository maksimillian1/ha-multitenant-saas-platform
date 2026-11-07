// Command server serves the API and stores events.
package main

import (
	"net/http"

	"example.com/fixture/internal/api"
	"example.com/fixture/internal/store"
)

func main() {
	api.Routes(http.NewServeMux(), nil, "/debug")
	_ = store.SQLStore{}
}
