package main

import (
	"io"
	"log"
	"sync"

	"github.com/pion/webrtc/v4"
)

type TrackBroadcaster struct {
	remoteTrack *webrtc.TrackRemote
	localTracks []*webrtc.TrackLocalStaticRTP
	mu          sync.RWMutex
	closed      bool
}

func NewTrackBroadcaster(remoteTrack *webrtc.TrackRemote) *TrackBroadcaster {
	broadcaster := &TrackBroadcaster{
		remoteTrack: remoteTrack,
		localTracks: make([]*webrtc.TrackLocalStaticRTP, 0),
	}

	log.Printf("Created broadcaster: trackID=%s, streamID=%s", remoteTrack.ID(), remoteTrack.StreamID())

	go broadcaster.start()

	return broadcaster
}

func (tb *TrackBroadcaster) AddTrackLocal(track *webrtc.TrackLocalStaticRTP) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	if tb.closed {
		log.Printf("Warning: attempted to add track to closed broadcaster")
		return
	}

	tb.localTracks = append(tb.localTracks, track)
	log.Printf("Local track added to broadcaster (trackID: %s). Recipients: %d", track.ID(), len(tb.localTracks))
}

func (tb *TrackBroadcaster) start() {
	defer tb.Close()

	rtpBuf := make([]byte, 1500)

	for {
		n, _, err := tb.remoteTrack.Read(rtpBuf)
		if err != nil {
			if err != io.EOF {
				log.Printf("Broadcaster read error: %v", err)
			}
			return
		}

		packet := make([]byte, n)
		copy(packet, rtpBuf[:n])

		tb.mu.RLock()
		for _, localTrack := range tb.localTracks {
			if _, err := localTrack.Write(packet); err != nil {
				log.Printf("Error writing to local track %s: %v", localTrack.ID(), err)
			}
		}
		tb.mu.RUnlock()
	}
}

func (tb *TrackBroadcaster) Close() {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.closed = true
	tb.localTracks = nil
}
