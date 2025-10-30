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

			log.Printf("Peer %s aguardando offer para adicionar tracks existentes", peer.Name)

		case "offer":
			// Processar oferta SDP
			if peer == nil {
				log.Println("Peer não inicializado")
				continue
			}

			log.Printf("Recebida offer de %s", peer.Name)

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

			// Processar ICE candidates que chegaram antes da remote description
			peer.ProcessPendingCandidates()

			// IMPORTANTE: Adicionar tracks existentes de outros peers ANTES de criar answer
			// Isso garante que a answer inclui todos os tracks
			otherPeers := room.GetPeers(peer.ID)
			log.Printf("Adicionando %d peers existentes para %s", len(otherPeers), peer.Name)

			for _, otherPeer := range otherPeers {
				broadcasters := otherPeer.GetBroadcasters()
				for _, broadcaster := range broadcasters {
					log.Printf("Adicionando track de %s para %s", otherPeer.Name, peer.Name)
					peer.AddBroadcaster(broadcaster)
				}
			}

			// Criar resposta (agora inclui todos os tracks adicionados)
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

			log.Printf("Answer criada para %s com tracks incluídos", peer.Name)

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

			// Notificar os outros peers sobre o novo participante
			notification := Message{
				Type: "peer-joined",
				Data: peer.Name,
			}
			for _, otherPeer := range otherPeers {
				otherPeer.SendMessage(notification)
			}

		case "answer":
			// Processar resposta SDP
			if peer == nil {
				log.Println("Peer não inicializado")
				continue
			}

			log.Printf("Recebida answer de %s", peer.Name)

			var answer webrtc.SessionDescription
			if err := json.Unmarshal([]byte(msg.Data), &answer); err != nil {
				log.Printf("Erro ao decodificar resposta de %s: %v", peer.Name, err)
				continue
			}

			// Definir a descrição remota
			if err := peer.PeerConnection.SetRemoteDescription(answer); err != nil {
				log.Printf("Erro ao definir descrição remota para %s: %v", peer.Name, err)
				continue
			}

			// Processar ICE candidates que chegaram antes da remote description
			peer.ProcessPendingCandidates()

			log.Printf("Answer processada com sucesso para %s (estado: %s)", peer.Name, peer.PeerConnection.ConnectionState())

		case "candidate":
			// Processar ICE candidate
			if peer == nil {
				log.Println("Peer não inicializado")
				continue
			}

			var candidate webrtc.ICECandidateInit
			if err := json.Unmarshal([]byte(msg.Data), &candidate); err != nil {
				log.Printf("Erro ao decodificar candidato de %s: %v", peer.Name, err)
				continue
			}

			// Adicionar ICE candidate (com buffer se necessário)
			if err := peer.AddICECandidate(candidate); err != nil {
				log.Printf("Erro ao adicionar ICE candidate para %s: %v", peer.Name, err)
				continue
			}

		default:
			log.Printf("Tipo de mensagem desconhecido: %s", msg.Type)
		}
	}
}
