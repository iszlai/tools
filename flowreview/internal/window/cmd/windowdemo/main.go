// Command windowdemo opens flowreview's native window against a tiny local
// HTTP server, so the window package can be exercised by hand: run it, see
// the window with "hello" loaded, quit it (⌘Q or ⌘W).
package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/iszlai/tools/flowreview/internal/window"
)

func main() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "hello")
	}))
	defer server.Close()

	err := window.Run(window.Options{
		Title:  "windowdemo",
		URL:    server.URL,
		Width:  900,
		Height: 600,
		OnPreferences: func() {
			log.Println("windowdemo: preferences selected")
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
