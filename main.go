package main

import (
	"log"
	"net/http"
)

func main() {
	roomManager := NewRoomManager()

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		HandleWebSocket(w, r, roomManager)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})

	port := ":8080"
	log.Printf("SFU server started on port %s", port)
	log.Fatal(http.ListenAndServe(port, nil))
}
