package main

import (
	"log"
	"net/http"
)

func main() {
	// Criar gerenciador de salas
	roomManager := NewRoomManager()

	// Configurar rotas
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		HandleWebSocket(w, r, roomManager)
	})

	// Página de teste simples
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})

	// Iniciar servidor
	port := ":8080"
	log.Printf("Servidor SFU iniciado na porta %s", port)
	log.Fatal(http.ListenAndServe(port, nil))
}
