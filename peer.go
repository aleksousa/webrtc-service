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
	ID                  string
	Name                string
	Room                *Room
	PeerConnection      *webrtc.PeerConnection
	WebSocketConn       *websocket.Conn
	tracksMu            sync.RWMutex
	broadcasters        []*TrackBroadcaster // Broadcasters dos tracks que este peer está enviando
	isNegotiating       bool                // Flag para evitar renegociações concorrentes
	negotiatingMu       sync.Mutex
	pendingTracks       []*TrackBroadcaster       // Tracks aguardando para serem adicionados
	pendingCandidates   []webrtc.ICECandidateInit // ICE candidates aguardando remote description
	pendingCandidatesMu sync.Mutex
}

// NewPeer cria um novo peer
func NewPeer(id, name string, room *Room, ws *websocket.Conn) (*Peer, error) {
	// Criar MediaEngine customizado para melhor qualidade de áudio
	mediaEngine := &webrtc.MediaEngine{}

	// Configurar codec Opus para áudio de alta qualidade
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

	// Criar SettingEngine
	settingEngine := webrtc.SettingEngine{}

	// Criar API com configurações customizadas
	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithSettingEngine(settingEngine),
	)

	// Configuração do WebRTC
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
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
		pendingTracks:     make([]*TrackBroadcaster, 0),
		pendingCandidates: make([]webrtc.ICECandidateInit, 0),
	}

	// Handler para quando recebemos um track (áudio) do peer
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("Recebido track de %s (tipo: %s)", peer.Name, track.Kind())

		// Criar um broadcaster para este track (lê UMA vez e distribui para todos)
		broadcaster := NewTrackBroadcaster(track)

		// Armazenar o broadcaster
		peer.tracksMu.Lock()
		peer.broadcasters = append(peer.broadcasters, broadcaster)
		peer.tracksMu.Unlock()

		// Broadcast do broadcaster para todos os outros peers na sala
		room.BroadcastTrack(peer.ID, broadcaster)
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

// AddBroadcaster adiciona um broadcaster ao peer (encaminha áudio de outro participante)
func (p *Peer) AddBroadcaster(broadcaster *TrackBroadcaster) {
	// Criar um track local para enviar ao peer
	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		broadcaster.remoteTrack.Codec().RTPCodecCapability,
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
		log.Printf("Erro ao adicionar track ao peer %s: %v", p.Name, err)
		return
	}

	log.Printf("Track adicionado ao peer %s (estado: %s)", p.Name, p.PeerConnection.ConnectionState())

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

	// Adicionar o track local ao broadcaster (broadcaster distribui os pacotes)
	broadcaster.AddTrackLocal(localTrack)

	// Se já temos uma conexão estabelecida, precisamos renegociar
	state := p.PeerConnection.ConnectionState()
	if state == webrtc.PeerConnectionStateConnected {
		log.Printf("Iniciando renegociação para peer %s", p.Name)
		go p.Renegotiate() // Executar em goroutine para não bloquear
	}
}

// Renegotiate cria uma nova oferta para renegociar a conexão
func (p *Peer) Renegotiate() {
	// Evitar renegociações concorrentes
	p.negotiatingMu.Lock()
	if p.isNegotiating {
		log.Printf("Renegociação já em andamento para %s, ignorando", p.Name)
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

	log.Printf("Criando offer de renegociação para %s", p.Name)

	offer, err := p.PeerConnection.CreateOffer(nil)
	if err != nil {
		log.Printf("Erro ao criar oferta para renegociação de %s: %v", p.Name, err)
		return
	}

	if err := p.PeerConnection.SetLocalDescription(offer); err != nil {
		log.Printf("Erro ao definir descrição local para %s: %v", p.Name, err)
		return
	}

	offerJSON, err := json.Marshal(offer)
	if err != nil {
		log.Printf("Erro ao serializar oferta para %s: %v", p.Name, err)
		return
	}

	msg := Message{
		Type: "offer",
		Data: string(offerJSON),
	}

	log.Printf("Enviando offer de renegociação para %s", p.Name)
	p.SendMessage(msg)
}

// GetBroadcasters retorna os broadcasters que este peer está enviando
func (p *Peer) GetBroadcasters() []*TrackBroadcaster {
	p.tracksMu.RLock()
	defer p.tracksMu.RUnlock()

	broadcasters := make([]*TrackBroadcaster, len(p.broadcasters))
	copy(broadcasters, p.broadcasters)
	return broadcasters
}

// AddICECandidate adiciona um ICE candidate, com buffer se remote description não estiver definida
func (p *Peer) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	// Verificar se a remote description já foi definida
	if p.PeerConnection.RemoteDescription() == nil {
		// Fazer buffer do candidate
		p.pendingCandidatesMu.Lock()
		p.pendingCandidates = append(p.pendingCandidates, candidate)
		p.pendingCandidatesMu.Unlock()
		log.Printf("ICE candidate de %s adicionado ao buffer (aguardando remote description)", p.Name)
		return nil
	}

	// Remote description está definida, adicionar candidate normalmente
	return p.PeerConnection.AddICECandidate(candidate)
}

// ProcessPendingCandidates processa todos os ICE candidates que estavam em buffer
func (p *Peer) ProcessPendingCandidates() {
	p.pendingCandidatesMu.Lock()
	defer p.pendingCandidatesMu.Unlock()

	if len(p.pendingCandidates) == 0 {
		return
	}

	log.Printf("Processando %d ICE candidates pendentes para %s", len(p.pendingCandidates), p.Name)

	for _, candidate := range p.pendingCandidates {
		if err := p.PeerConnection.AddICECandidate(candidate); err != nil {
			log.Printf("Erro ao adicionar ICE candidate pendente para %s: %v", p.Name, err)
		}
	}

	// Limpar o buffer
	p.pendingCandidates = make([]webrtc.ICECandidateInit, 0)
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

	// Fechar todos os broadcasters
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
