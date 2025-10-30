package main

import (
	"io"
	"log"
	"sync"

	"github.com/pion/webrtc/v4"
)

// TrackBroadcaster gerencia a leitura de um track remoto e distribui para múltiplos destinos
type TrackBroadcaster struct {
	remoteTrack *webrtc.TrackRemote
	localTracks []*webrtc.TrackLocalStaticRTP
	mu          sync.RWMutex
	closed      bool
}

// NewTrackBroadcaster cria um novo broadcaster para um track remoto
func NewTrackBroadcaster(remoteTrack *webrtc.TrackRemote) *TrackBroadcaster {
	broadcaster := &TrackBroadcaster{
		remoteTrack: remoteTrack,
		localTracks: make([]*webrtc.TrackLocalStaticRTP, 0),
	}

	// Iniciar a leitura e broadcast dos pacotes RTP
	go broadcaster.start()

	return broadcaster
}

// AddTrackLocal adiciona um track local para receber o broadcast
func (tb *TrackBroadcaster) AddTrackLocal(track *webrtc.TrackLocalStaticRTP) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	if tb.closed {
		return
	}

	tb.localTracks = append(tb.localTracks, track)
	log.Printf("Track local adicionado ao broadcaster. Total: %d", len(tb.localTracks))
}

// start lê os pacotes RTP do track remoto e distribui para todos os tracks locais
func (tb *TrackBroadcaster) start() {
	defer tb.Close()

	rtpBuf := make([]byte, 1500)

	for {
		// Ler pacote RTP do track remoto (UMA VEZ)
		n, _, readErr := tb.remoteTrack.Read(rtpBuf)

		if readErr != nil {
			if readErr != io.EOF {
				log.Printf("Erro ao ler track remoto: %v", readErr)
			}
			return
		}

		// Copiar o buffer para evitar race conditions
		packet := make([]byte, n)
		copy(packet, rtpBuf[:n])

		// Distribuir para todos os tracks locais
		tb.mu.RLock()
		for _, localTrack := range tb.localTracks {
			if _, err := localTrack.Write(packet); err != nil {
				log.Printf("Erro ao escrever no track local: %v", err)
			}
		}
		tb.mu.RUnlock()
	}
}

// Close fecha o broadcaster
func (tb *TrackBroadcaster) Close() {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.closed = true
	tb.localTracks = nil
}
