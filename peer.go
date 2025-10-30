package main

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// Peer representa um participante da sala
type Peer struct {
	ID             string
	Name           string
	Room           *Room
	PeerConnection *webrtc.PeerConnection
	WebSocketConn  *websocket.Conn
	tracksMu       sync.RWMutex
	pendingTracks  []*webrtc.TrackRemote
}

// NewPeer cria um novo peer
func NewPeer(id, name string, room *Room, ws *websocket.Conn) (*Peer, error) {
	// Configuração do WebRTC
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}

	peer := &Peer{
		ID:             id,
		Name:           name,
		Room:           room,
		PeerConnection: peerConnection,
		WebSocketConn:  ws,
		pendingTracks:  make([]*webrtc.TrackRemote, 0),
	}

	// Handler para quando recebemos um track (áudio) do peer
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("Recebido track de %s (tipo: %s)", peer.Name, track.Kind())

		// Broadcast do track para todos os outros peers na sala
		room.BroadcastTrack(peer.ID, track)

		// Ler e processar os pacotes RTP
		go func() {
			buf := make([]byte, 1500)
			for {
				_, _, err := track.Read(buf)
				if err != nil {
					log.Printf("Erro ao ler track: %v", err)
					return
				}
			}
		}()
	})

	// Handler para ICE candidates
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}

		candidateJSON, err := json.Marshal(candidate.ToJSON())
		if err != nil {
			log.Printf("Erro ao serializar ICE candidate: %v", err)
			return
		}

		msg := Message{
			Type: "candidate",
			Data: string(candidateJSON),
		}

		peer.SendMessage(msg)
	})

	// Handler para mudanças no estado da conexão
	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Estado da conexão de %s mudou para: %s", peer.Name, state.String())

		if state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateDisconnected ||
			state == webrtc.PeerConnectionStateClosed {
			peer.Close()
		}
	})

	return peer, nil
}

// AddTrack adiciona um track remoto ao peer (encaminha áudio de outro participante)
func (p *Peer) AddTrack(remoteTrack *webrtc.TrackRemote) {
	// Criar um track local para enviar ao peer
	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		remoteTrack.Codec().RTPCodecCapability,
		"audio",
		"webrtc-sfu",
	)
	if err != nil {
		log.Printf("Erro ao criar track local: %v", err)
		return
	}

	// Adicionar o track à conexão do peer
	rtpSender, err := p.PeerConnection.AddTrack(localTrack)
	if err != nil {
		log.Printf("Erro ao adicionar track ao peer: %v", err)
		return
	}

	// Processar RTCP packets
	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			_, _, err := rtpSender.Read(rtcpBuf)
			if err != nil {
				return
			}
		}
	}()

	// Encaminhar pacotes RTP do track remoto para o track local
	go func() {
		buf := make([]byte, 1500)
		for {
			n, _, err := remoteTrack.Read(buf)
			if err != nil {
				return
			}

			if _, err := localTrack.Write(buf[:n]); err != nil {
				return
			}
		}
	}()

	// Se já temos uma conexão estabelecida, precisamos renegociar
	if p.PeerConnection.ConnectionState() == webrtc.PeerConnectionStateConnected {
		p.Renegotiate()
	}
}

// Renegotiate cria uma nova oferta para renegociar a conexão
func (p *Peer) Renegotiate() {
	offer, err := p.PeerConnection.CreateOffer(nil)
	if err != nil {
		log.Printf("Erro ao criar oferta para renegociação: %v", err)
		return
	}

	if err := p.PeerConnection.SetLocalDescription(offer); err != nil {
		log.Printf("Erro ao definir descrição local: %v", err)
		return
	}

	offerJSON, err := json.Marshal(offer)
	if err != nil {
		log.Printf("Erro ao serializar oferta: %v", err)
		return
	}

	msg := Message{
		Type: "offer",
		Data: string(offerJSON),
	}

	p.SendMessage(msg)
}

// SendMessage envia uma mensagem pelo WebSocket
func (p *Peer) SendMessage(msg Message) {
	if p.WebSocketConn == nil {
		return
	}

	if err := p.WebSocketConn.WriteJSON(msg); err != nil {
		log.Printf("Erro ao enviar mensagem para %s: %v", p.Name, err)
	}
}

// Close fecha a conexão do peer
func (p *Peer) Close() {
	log.Printf("Fechando conexão do peer %s", p.Name)

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
