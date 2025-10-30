package main

import (
	"log"
	"sync"
)

type Room struct {
	ID    string
	peers map[string]*Peer
	mu    sync.RWMutex
}

type RoomManager struct {
	rooms map[string]*Room
	mu    sync.RWMutex
}

func NewRoomManager() *RoomManager {
	return &RoomManager{
		rooms: make(map[string]*Room),
	}
}

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

func (r *Room) AddPeer(peer *Peer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.peers[peer.ID] = peer
}

func (r *Room) RemovePeer(peerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.peers, peerID)
}

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

func (r *Room) BroadcastTrack(senderID string, broadcaster *TrackBroadcaster) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for id, peer := range r.peers {
		if id != senderID {
			log.Printf("Broadcasting track to peer %s (state: %s)", peer.Name, peer.PeerConnection.ConnectionState())
			peer.AddBroadcaster(broadcaster)
		}
	}
}
