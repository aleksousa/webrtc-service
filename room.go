package main

import (
	"log"
	"sync"
)

// Room representa uma sala de conferência
type Room struct {
	ID    string
	peers map[string]*Peer
	mu    sync.RWMutex
}

// RoomManager gerencia todas as salas
type RoomManager struct {
	rooms map[string]*Room
	mu    sync.RWMutex
}

// NewRoomManager cria um novo gerenciador de salas
func NewRoomManager() *RoomManager {
	return &RoomManager{
		rooms: make(map[string]*Room),
	}
}

// GetOrCreateRoom obtém uma sala existente ou cria uma nova
func (rm *RoomManager) GetOrCreateRoom(roomID string) *Room {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	room, exists := rm.rooms[roomID]
	if !exists {
		room = &Room{
			ID:    roomID,
			peers: make(map[string]*Peer),
		}
		rm.rooms[roomID] = room
	}

	return room
}

// AddPeer adiciona um peer à sala
func (r *Room) AddPeer(peer *Peer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.peers[peer.ID] = peer
}

// RemovePeer remove um peer da sala
func (r *Room) RemovePeer(peerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.peers, peerID)
}

// GetPeers retorna todos os peers da sala (exceto o especificado)
func (r *Room) GetPeers(excludeID string) []*Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	peers := make([]*Peer, 0, len(r.peers)-1)
	for id, peer := range r.peers {
		if id != excludeID {
			peers = append(peers, peer)
		}
	}
	return peers
}

// BroadcastTrack envia um broadcaster para todos os peers da sala (exceto o remetente)
func (r *Room) BroadcastTrack(senderID string, broadcaster *TrackBroadcaster) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	recipients := 0
	for id, peer := range r.peers {
		if id != senderID {
			log.Printf("Enviando broadcaster para peer %s (estado: %s)", peer.Name, peer.PeerConnection.ConnectionState())
			peer.AddBroadcaster(broadcaster)
			recipients++
		}
	}
	log.Printf("Broadcaster enviado para %d peer(s)", recipients)
}
