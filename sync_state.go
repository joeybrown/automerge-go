package automerge

import (
	"context"
	"fmt"
)

// SyncState represents the state of syncing between a local copy of
// a doc and a peer; and lets you optimize bandwidth used to ensure
// two docs are always in sync.
type SyncState struct {
	Doc    *Doc
	peerID uint32
}

// NewSyncState returns a new sync state to sync with a peer
func NewSyncState(d *Doc) *SyncState {
	b, unlock := d.lock()
	defer unlock()

	ctx := context.Background()
	peerID, err := b.SyncInit(ctx)
	if err != nil {
		panic(fmt.Errorf("automerge.NewSyncState: %w", err))
	}
	return &SyncState{Doc: d, peerID: peerID}
}

// LoadSyncState lets you resume syncing with a peer from where you left off.
func LoadSyncState(d *Doc, raw []byte) (*SyncState, error) {
	b, unlock := d.lock()
	defer unlock()

	ctx := context.Background()
	peerID, err := b.SyncLoad(ctx, raw)
	if err != nil {
		return nil, err
	}
	return &SyncState{Doc: d, peerID: peerID}, nil
}

// ReceiveMessage should be called with every message created by GenerateMessage
// on the peer side.
func (ss *SyncState) ReceiveMessage(msg []byte) (*SyncMessage, error) {
	b, unlock := ss.Doc.lock()
	defer unlock()

	ctx := context.Background()
	if err := b.SyncReceiveMessage(ctx, ss.peerID, msg); err != nil {
		return nil, err
	}

	// Decode the message for inspection
	sm, err := LoadSyncMessage(msg)
	if err != nil {
		return nil, err
	}
	return sm, nil
}

// GenerateMessage generates the next message to send to the client.
// If `valid` is false the clients are currently in sync and there are
// no more messages to send (until you either modify the underlying document)
func (ss *SyncState) GenerateMessage() (sm *SyncMessage, valid bool) {
	b, unlock := ss.Doc.lock()
	defer unlock()

	ctx := context.Background()
	msgBytes, ok, err := b.SyncGenerateMessage(ctx, ss.peerID)
	if err != nil {
		return nil, false
	}
	if !ok || len(msgBytes) == 0 {
		return nil, false
	}

	return &SyncMessage{raw: msgBytes}, true
}

// Save serializes the sync state so that you can resume it later.
func (ss *SyncState) Save() []byte {
	b, unlock := ss.Doc.lock()
	defer unlock()

	ctx := context.Background()
	data, err := b.SyncSave(ctx, ss.peerID)
	if err != nil {
		panic(fmt.Errorf("automerge.SyncState.Save: %w", err))
	}
	return data
}

// SyncMessage is sent between peers to keep copies of a document in sync.
type SyncMessage struct {
	raw []byte
}

// LoadSyncMessage decodes a sync message from a byte slice for inspection.
func LoadSyncMessage(msg []byte) (*SyncMessage, error) {
	if len(msg) == 0 {
		return nil, fmt.Errorf("automerge: empty sync message")
	}
	raw := make([]byte, len(msg))
	copy(raw, msg)
	return &SyncMessage{raw: raw}, nil
}

// Changes returns any changes included in this SyncMessage.
// Note: In the wazero backend, extracting changes from a sync message
// requires loading it in a temporary context. This returns nil for now
// as the sync protocol handles change application internally.
func (sm *SyncMessage) Changes() []*Change {
	// Sync message change extraction is handled by the WASM module internally
	// during ReceiveMessage. Individual change extraction from raw sync message
	// bytes would require additional WASM exports.
	return nil
}

// Heads gives the heads of the peer that generated the SyncMessage.
// Note: Similar to Changes(), extracting heads from raw sync message bytes
// is handled internally by the sync protocol.
func (sm *SyncMessage) Heads() []ChangeHash {
	return nil
}

// Bytes returns a representation for sending over the network.
func (sm *SyncMessage) Bytes() []byte {
	if sm == nil {
		return nil
	}
	out := make([]byte, len(sm.raw))
	copy(out, sm.raw)
	return out
}
