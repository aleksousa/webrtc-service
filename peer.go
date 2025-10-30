package main

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

type Peer struct {
	ID                  string
	Name                string
	Room                *Room
	PeerConnection      *webrtc.PeerConnection
	WebSocketConn       *websocket.Conn
	tracksMu            sync.RWMutex
	broadcasters        []*TrackBroadcaster
	isNegotiating       bool
	negotiatingMu       sync.Mutex
	pendingCandidates   []webrtc.ICECandidateInit
	pendingCandidatesMu sync.Mutex
}

func NewPeer(id, name string, room *Room, ws *websocket.Conn) (*Peer, error) {
	mediaEngine := &webrtc.MediaEngine{}

	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: "minptime=10;useinbandfec=1;stereo=1;sprop-stereo=1",
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithSettingEngine(webrtc.SettingEngine{}),
	)

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}

	peer := &Peer{
		ID:                id,
		Name:              name,
		Room:              room,
		PeerConnection:    peerConnection,
		WebSocketConn:     ws,
		broadcasters:      make([]*TrackBroadcaster, 0),
		isNegotiating:     false,
		pendingCandidates: make([]webrtc.ICECandidateInit, 0),
	}

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("Track received from %s (type: %s)", peer.Name, track.Kind())

		broadcaster := NewTrackBroadcaster(track)

		peer.tracksMu.Lock()
		peer.broadcasters = append(peer.broadcasters, broadcaster)
		peer.tracksMu.Unlock()

		room.BroadcastTrack(peer.ID, broadcaster)
	})

	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}

		candidateJSON, err := json.Marshal(candidate.ToJSON())
		if err != nil {
			log.Printf("Error marshaling ICE candidate: %v", err)
			return
		}

		peer.SendMessage(Message{
			Type: "candidate",
			Data: string(candidateJSON),
		})
	})

	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Peer %s connection state: %s", peer.Name, state.String())

		if state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateDisconnected ||
			state == webrtc.PeerConnectionStateClosed {
			peer.Close()
		}
	})

	return peer, nil
}

func (p *Peer) AddBroadcaster(broadcaster *TrackBroadcaster) {
	uniqueTrackID := uuid.New().String()
	uniqueStreamID := uuid.New().String()

	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		broadcaster.remoteTrack.Codec().RTPCodecCapability,
		uniqueTrackID,
		uniqueStreamID,
	)
	if err != nil {
		log.Printf("Error creating local track: %v", err)
		return
	}

	rtpSender, err := p.PeerConnection.AddTrack(localTrack)
	if err != nil {
		log.Printf("Error adding track to peer %s: %v", p.Name, err)
		return
	}

	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, err := rtpSender.Read(rtcpBuf); err != nil {
				return
			}
		}
	}()

	broadcaster.AddTrackLocal(localTrack)

	if p.PeerConnection.ConnectionState() == webrtc.PeerConnectionStateConnected {
		log.Printf("Starting renegotiation for peer %s", p.Name)
		go p.Renegotiate()
	}
}

func (p *Peer) Renegotiate() {
	p.negotiatingMu.Lock()
	if p.isNegotiating {
		p.negotiatingMu.Unlock()
		return
	}
	p.isNegotiating = true
	p.negotiatingMu.Unlock()

	defer func() {
		p.negotiatingMu.Lock()
		p.isNegotiating = false
		p.negotiatingMu.Unlock()
	}()

	offer, err := p.PeerConnection.CreateOffer(nil)
	if err != nil {
		log.Printf("Error creating renegotiation offer for %s: %v", p.Name, err)
		return
	}

	if err := p.PeerConnection.SetLocalDescription(offer); err != nil {
		log.Printf("Error setting local description for %s: %v", p.Name, err)
		return
	}

	offerJSON, err := json.Marshal(offer)
	if err != nil {
		log.Printf("Error marshaling offer for %s: %v", p.Name, err)
		return
	}

	p.SendMessage(Message{
		Type: "offer",
		Data: string(offerJSON),
	})
}

func (p *Peer) GetBroadcasters() []*TrackBroadcaster {
	p.tracksMu.RLock()
	defer p.tracksMu.RUnlock()

	broadcasters := make([]*TrackBroadcaster, len(p.broadcasters))
	copy(broadcasters, p.broadcasters)
	return broadcasters
}

func (p *Peer) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	if p.PeerConnection.RemoteDescription() == nil {
		p.pendingCandidatesMu.Lock()
		p.pendingCandidates = append(p.pendingCandidates, candidate)
		p.pendingCandidatesMu.Unlock()
		return nil
	}

	return p.PeerConnection.AddICECandidate(candidate)
}

func (p *Peer) ProcessPendingCandidates() {
	p.pendingCandidatesMu.Lock()
	defer p.pendingCandidatesMu.Unlock()

	if len(p.pendingCandidates) == 0 {
		return
	}

	log.Printf("Processing %d pending ICE candidates for %s", len(p.pendingCandidates), p.Name)

	for _, candidate := range p.pendingCandidates {
		if err := p.PeerConnection.AddICECandidate(candidate); err != nil {
			log.Printf("Error adding pending ICE candidate for %s: %v", p.Name, err)
		}
	}

	p.pendingCandidates = make([]webrtc.ICECandidateInit, 0)
}

func (p *Peer) SendMessage(msg Message) {
	if p.WebSocketConn == nil {
		return
	}

	if err := p.WebSocketConn.WriteJSON(msg); err != nil {
		log.Printf("Error sending message to %s: %v", p.Name, err)
	}
}

func (p *Peer) Close() {
	log.Printf("Closing peer %s", p.Name)

	p.tracksMu.Lock()
	for _, broadcaster := range p.broadcasters {
		broadcaster.Close()
	}
	p.broadcasters = nil
	p.tracksMu.Unlock()

	if p.PeerConnection != nil {
		p.PeerConnection.Close()
	}

	if p.WebSocketConn != nil {
		p.WebSocketConn.Close()
	}

	if p.Room != nil {
		p.Room.RemovePeer(p.ID)
	}
}
