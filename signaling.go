package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

type Message struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type JoinRequest struct {
	Name   string `json:"name"`
	RoomID string `json:"roomId"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func HandleWebSocket(w http.ResponseWriter, r *http.Request, roomManager *RoomManager) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer ws.Close()

	var peer *Peer
	var room *Room

	for {
		var msg Message
		err := ws.ReadJSON(&msg)
		if err != nil {
			log.Printf("Error reading message: %v", err)
			if peer != nil {
				peer.Close()
			}
			break
		}

		switch msg.Type {
		case "join":
			var joinReq JoinRequest
			if err := json.Unmarshal([]byte(msg.Data), &joinReq); err != nil {
				log.Printf("Error decoding join request: %v", err)
				continue
			}

			room = roomManager.GetOrCreateRoom(joinReq.RoomID)

			peerID := uuid.New().String()
			peer, err = NewPeer(peerID, joinReq.Name, room, ws)
			if err != nil {
				log.Printf("Error creating peer: %v", err)
				continue
			}

			room.AddPeer(peer)

			log.Printf("Peer %s (%s) joined room %s", peer.Name, peer.ID, room.ID)

			peer.SendMessage(Message{
				Type: "joined",
				Data: peerID,
			})

		case "offer":
			if peer == nil {
				log.Println("Peer not initialized")
				continue
			}

			log.Printf("Received offer from %s", peer.Name)

			var offer webrtc.SessionDescription
			if err := json.Unmarshal([]byte(msg.Data), &offer); err != nil {
				log.Printf("Error decoding offer: %v", err)
				continue
			}

			if err := peer.PeerConnection.SetRemoteDescription(offer); err != nil {
				log.Printf("Error setting remote description: %v", err)
				continue
			}

			peer.ProcessPendingCandidates()

			otherPeers := room.GetPeers(peer.ID)
			log.Printf("Adding existing tracks to %s from %d peers", peer.Name, len(otherPeers))

			for _, otherPeer := range otherPeers {
				broadcasters := otherPeer.GetBroadcasters()
				for _, broadcaster := range broadcasters {
					peer.AddBroadcaster(broadcaster)
				}
			}

			answer, err := peer.PeerConnection.CreateAnswer(nil)
			if err != nil {
				log.Printf("Error creating answer: %v", err)
				continue
			}

			if err := peer.PeerConnection.SetLocalDescription(answer); err != nil {
				log.Printf("Error setting local description: %v", err)
				continue
			}

			log.Printf("Answer created for %s", peer.Name)

			answerJSON, err := json.Marshal(answer)
			if err != nil {
				log.Printf("Error marshaling answer: %v", err)
				continue
			}

			peer.SendMessage(Message{
				Type: "answer",
				Data: string(answerJSON),
			})

			for _, otherPeer := range otherPeers {
				otherPeer.SendMessage(Message{
					Type: "peer-joined",
					Data: peer.Name,
				})
			}

		case "answer":
			if peer == nil {
				log.Println("Peer not initialized")
				continue
			}

			log.Printf("Received answer from %s", peer.Name)

			var answer webrtc.SessionDescription
			if err := json.Unmarshal([]byte(msg.Data), &answer); err != nil {
				log.Printf("Error decoding answer from %s: %v", peer.Name, err)
				continue
			}

			if err := peer.PeerConnection.SetRemoteDescription(answer); err != nil {
				log.Printf("Error setting remote description for %s: %v", peer.Name, err)
				continue
			}

			peer.ProcessPendingCandidates()

			log.Printf("Answer processed successfully for %s (state: %s)", peer.Name, peer.PeerConnection.ConnectionState())

		case "candidate":
			if peer == nil {
				log.Println("Peer not initialized")
				continue
			}

			var candidate webrtc.ICECandidateInit
			if err := json.Unmarshal([]byte(msg.Data), &candidate); err != nil {
				log.Printf("Error decoding candidate from %s: %v", peer.Name, err)
				continue
			}

			if err := peer.AddICECandidate(candidate); err != nil {
				log.Printf("Error adding ICE candidate for %s: %v", peer.Name, err)
				continue
			}

		default:
			log.Printf("Unknown message type: %s", msg.Type)
		}
	}
}
