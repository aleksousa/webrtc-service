package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// Message representa uma mensagem de sinalização
type Message struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// JoinRequest representa uma solicitação para entrar em uma sala
type JoinRequest struct {
	Name   string `json:"name"`
	RoomID string `json:"roomId"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Em produção, validar a origem adequadamente
	},
}

// HandleWebSocket gerencia as conexões WebSocket
func HandleWebSocket(w http.ResponseWriter, r *http.Request, roomManager *RoomManager) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Erro ao fazer upgrade para WebSocket: %v", err)
		return
	}
	defer ws.Close()

	var peer *Peer
	var room *Room

	// Loop para processar mensagens
	for {
		var msg Message
		err := ws.ReadJSON(&msg)
		if err != nil {
			log.Printf("Erro ao ler mensagem: %v", err)
			if peer != nil {
				peer.Close()
			}
			break
		}

		switch msg.Type {
		case "join":
			// Processar solicitação de entrada na sala
			var joinReq JoinRequest
			if err := json.Unmarshal([]byte(msg.Data), &joinReq); err != nil {
				log.Printf("Erro ao decodificar join request: %v", err)
				continue
			}

			// Obter ou criar a sala
			room = roomManager.GetOrCreateRoom(joinReq.RoomID)

			// Criar o peer
			peerID := uuid.New().String()
			peer, err = NewPeer(peerID, joinReq.Name, room, ws)
			if err != nil {
				log.Printf("Erro ao criar peer: %v", err)
				continue
			}

			// Adicionar peer à sala
			room.AddPeer(peer)

			log.Printf("Peer %s (%s) entrou na sala %s", peer.Name, peer.ID, room.ID)

			// Enviar confirmação de entrada
			response := Message{
				Type: "joined",
				Data: peerID,
			}
			peer.SendMessage(response)

			// Obter todos os outros peers na sala
			otherPeers := room.GetPeers(peer.ID)

			// Enviar todos os broadcasters existentes dos outros peers para o novo peer
			for _, otherPeer := range otherPeers {
				broadcasters := otherPeer.GetBroadcasters()
				for _, broadcaster := range broadcasters {
					log.Printf("Enviando track de %s para novo peer %s", otherPeer.Name, peer.Name)
					peer.AddBroadcaster(broadcaster)
				}
			}

			// Notificar os outros peers sobre o novo participante
			notification := Message{
				Type: "peer-joined",
				Data: peer.Name,
			}
			for _, otherPeer := range otherPeers {
				otherPeer.SendMessage(notification)
			}

		case "offer":
			// Processar oferta SDP
			if peer == nil {
				log.Println("Peer não inicializado")
				continue
			}

			var offer webrtc.SessionDescription
			if err := json.Unmarshal([]byte(msg.Data), &offer); err != nil {
				log.Printf("Erro ao decodificar oferta: %v", err)
				continue
			}

			// Definir a descrição remota
			if err := peer.PeerConnection.SetRemoteDescription(offer); err != nil {
				log.Printf("Erro ao definir descrição remota: %v", err)
				continue
			}

			// Criar resposta
			answer, err := peer.PeerConnection.CreateAnswer(nil)
			if err != nil {
				log.Printf("Erro ao criar resposta: %v", err)
				continue
			}

			// Definir a descrição local
			if err := peer.PeerConnection.SetLocalDescription(answer); err != nil {
				log.Printf("Erro ao definir descrição local: %v", err)
				continue
			}

			// Enviar resposta ao cliente
			answerJSON, err := json.Marshal(answer)
			if err != nil {
				log.Printf("Erro ao serializar resposta: %v", err)
				continue
			}

			response := Message{
				Type: "answer",
				Data: string(answerJSON),
			}
			peer.SendMessage(response)

		case "answer":
			// Processar resposta SDP
			if peer == nil {
				log.Println("Peer não inicializado")
				continue
			}

			var answer webrtc.SessionDescription
			if err := json.Unmarshal([]byte(msg.Data), &answer); err != nil {
				log.Printf("Erro ao decodificar resposta: %v", err)
				continue
			}

			// Definir a descrição remota
			if err := peer.PeerConnection.SetRemoteDescription(answer); err != nil {
				log.Printf("Erro ao definir descrição remota: %v", err)
				continue
			}

		case "candidate":
			// Processar ICE candidate
			if peer == nil {
				log.Println("Peer não inicializado")
				continue
			}

			var candidate webrtc.ICECandidateInit
			if err := json.Unmarshal([]byte(msg.Data), &candidate); err != nil {
				log.Printf("Erro ao decodificar candidato: %v", err)
				continue
			}

			// Adicionar ICE candidate
			if err := peer.PeerConnection.AddICECandidate(candidate); err != nil {
				log.Printf("Erro ao adicionar ICE candidate: %v", err)
				continue
			}

		default:
			log.Printf("Tipo de mensagem desconhecido: %s", msg.Type)
		}
	}
}
