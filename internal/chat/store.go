// Package chat is the in-memory store for GeoChat messages — both All-Chat
// broadcast and direct messages.
//
// The store is unaware of channel/team-colour state. The TUI passes a
// per-read predicate (sender UID -> allowed?) so the same channel filter
// used for the contacts table also hides chat from senders whose channels
// are disabled.
package chat

import (
	"sort"
	"sync"
	"time"

	"github.com/u0222-swe/terminaltak/internal/cot"
)

// Direction is the message direction relative to the local user.
type Direction int

const (
	DirectionIn Direction = iota
	DirectionOut
)

// Status is the delivery status of an outgoing message. Incoming messages
// always have StatusReceived.
type Status int

const (
	StatusReceived Status = iota
	StatusPending
	StatusSent
	StatusFailed
)

// AllChatRoom is the broadcast chatroom name used by TAK clients.
const AllChatRoom = "All Chat Rooms"

// Message is a single chat entry as displayed in the TUI.
type Message struct {
	ID             string
	Time           time.Time
	Sender         string // sender UID; "" for All-Chat from us
	SenderCallsign string
	RecipientUID   string // "" for All-Chat
	Chatroom       string
	Text           string
	Direction      Direction
	Status         Status
}

// DefaultCapacity is the maximum number of messages a Store keeps before
// it starts dropping the oldest. Bounded growth matters because the TUI
// re-renders the chat pane several times a second and unbounded
// accumulation eventually slows input processing to a crawl.
const DefaultCapacity = 2000

// Store is the chronological message buffer. Messages are kept in
// arrival order; reads do not sort because incoming events typically
// already arrive in time order and the cost of sorting on every render
// dominates once the store grows past a few thousand entries.
type Store struct {
	mu       sync.RWMutex
	messages []Message
	cap      int
	selfUID  string
}

// NewStore builds a chat store. selfUID lets the store recognise echoed
// messages and detect locally-sent ones for delivery-status flips. The
// store is capped at DefaultCapacity messages — the oldest are dropped
// when the cap is exceeded.
func NewStore(selfUID string) *Store {
	return &Store{selfUID: selfUID, cap: DefaultCapacity}
}

// appendMsg appends m and trims the head if the store is over capacity.
// Caller must hold s.mu (write lock).
func (s *Store) appendMsg(m Message) {
	s.messages = append(s.messages, m)
	if len(s.messages) > s.cap {
		// Copy the tail to a new slice so the underlying array can be GC'd.
		trim := make([]Message, s.cap)
		copy(trim, s.messages[len(s.messages)-s.cap:])
		s.messages = trim
	}
}

// IngestEvent recognises GeoChat events and appends one message to the
// store. Returns the appended message (or nil if the event is not chat).
func (s *Store) IngestEvent(ev cot.Event) *Message {
	if !ev.IsGeoChat() {
		return nil
	}
	chat := ev.Detail.Chat
	rem := ev.Detail.Remarks
	if chat == nil || rem == nil {
		return nil
	}

	senderUID := ""
	if ev.Detail.Link != nil {
		senderUID = ev.Detail.Link.UID
	}
	dir := DirectionIn
	if senderUID == s.selfUID {
		dir = DirectionOut
	}

	msg := Message{
		ID:             ev.UID,
		Time:           parseTime(ev.Time),
		Sender:         senderUID,
		SenderCallsign: chat.SenderCallsign,
		RecipientUID:   chat.ID,
		Chatroom:       chat.Chatroom,
		Text:           rem.Text,
		Direction:      dir,
	}
	if dir == DirectionIn {
		msg.Status = StatusReceived
	} else {
		msg.Status = StatusSent
	}

	s.mu.Lock()
	s.appendMsg(msg)
	s.mu.Unlock()
	return &msg
}

// AppendOutgoing records an outgoing message before transmission.
func (s *Store) AppendOutgoing(id, recipientUID, chatroom, text, selfCallsign string, now time.Time) Message {
	m := Message{
		ID:             id,
		Time:           now,
		Sender:         s.selfUID,
		SenderCallsign: selfCallsign,
		RecipientUID:   recipientUID,
		Chatroom:       chatroom,
		Text:           text,
		Direction:      DirectionOut,
		Status:         StatusPending,
	}
	s.mu.Lock()
	s.appendMsg(m)
	s.mu.Unlock()
	return m
}

// MarkStatus updates the delivery status of a previously-appended outgoing
// message. No-op if the ID is unknown.
func (s *Store) MarkStatus(id string, status Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.messages {
		if s.messages[i].ID == id {
			s.messages[i].Status = status
			return
		}
	}
}

// AllChat returns every All-Chat message visible under filter, in
// chronological order. filter is applied to incoming messages only —
// outgoing messages are never hidden.
func (s *Store) AllChat(filter func(senderUID string) bool) []Message {
	return s.collect(func(m Message) bool {
		return m.Chatroom == AllChatRoom || m.RecipientUID == AllChatRoom
	}, filter)
}

// Conversation returns every DM exchanged with peerUID, in chronological order.
func (s *Store) Conversation(peerUID string, filter func(senderUID string) bool) []Message {
	return s.collect(func(m Message) bool {
		if m.Direction == DirectionOut {
			return m.RecipientUID == peerUID
		}
		return m.Sender == peerUID
	}, filter)
}

// All returns every message that passes the per-room and per-sender filter.
func (s *Store) All(filter func(senderUID string) bool) []Message {
	return s.collect(func(Message) bool { return true }, filter)
}

func (s *Store) collect(roomPred func(Message) bool, senderFilter func(string) bool) []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Message, 0, len(s.messages))
	for _, m := range s.messages {
		if !roomPred(m) {
			continue
		}
		if m.Direction == DirectionIn && senderFilter != nil && !senderFilter(m.Sender) {
			continue
		}
		out = append(out, m)
	}
	// Sort defensively in case events arrived out of order. With the
	// store capped at DefaultCapacity this is bounded; the unbounded
	// growth — not the sort — was the original lag culprit.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Time.Before(out[j].Time)
	})
	return out
}

// Len returns the total number of messages stored regardless of filter.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Now().UTC()
	}
	for _, layout := range []string{
		cot.TimeFormat,
		"2006-01-02T15:04:05Z",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}
