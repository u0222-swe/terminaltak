package tui

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/u0222-swe/terminaltak/internal/chat"
	"github.com/u0222-swe/terminaltak/internal/cot"
)

// chatInputModel manages the textinput row at the bottom of the chat pane.
// It is "active" only when the user has pressed 'i' on the chat pane.
type chatInputModel struct {
	input  textinput.Model
	active bool
	// peerUID is "" for All-Chat or a contact UID for a DM.
	peerUID      string
	peerCallsign string
}

func newChatInputModel() chatInputModel {
	ti := textinput.New()
	ti.Placeholder = "press i to type, Enter sends, Esc cancels"
	ti.Width = 60
	return chatInputModel{input: ti}
}

func (c *chatInputModel) startAllChat() {
	c.peerUID = ""
	c.peerCallsign = ""
	c.input.Placeholder = "<All Chat Rooms>"
	c.input.SetValue("")
	c.input.Focus()
	c.active = true
}

func (c *chatInputModel) startDM(uid, callsign string) {
	c.peerUID = uid
	c.peerCallsign = callsign
	c.input.Placeholder = "DM to " + callsign
	c.input.SetValue("")
	c.input.Focus()
	c.active = true
}

func (c *chatInputModel) cancel() {
	c.active = false
	c.input.Blur()
	c.input.SetValue("")
}

// sendChatCmd builds a CoT GeoChat event, appends a pending entry to the
// chat store, sends the event via the wired-in send function, and returns a
// ChatSendResultMsg flipping the entry to Sent or Failed.
func (m Model) sendChatCmd(text string) tea.Cmd {
	cfg := m.deps.Config
	sender := cot.SelfInfo{
		UID:      cfg.SelfPos.UID,
		Callsign: cfg.SelfPos.Callsign,
		Group:    cfg.SelfPos.Group,
		Role:     cfg.SelfPos.Role,
		Lat:      cfg.SelfPos.Lat,
		Lon:      cfg.SelfPos.Lon,
		HAE:      cfg.SelfPos.HAE,
	}
	var dest cot.ChatDest
	var recipientUID, chatroom string
	if m.chatInput.peerUID == "" {
		dest = cot.AllChat{}
		recipientUID = ""
		chatroom = chat.AllChatRoom
	} else {
		dest = cot.DM{RecipientUID: m.chatInput.peerUID, RecipientCallsign: m.chatInput.peerCallsign}
		recipientUID = m.chatInput.peerUID
		chatroom = m.chatInput.peerCallsign
	}
	now := time.Now()
	ev, err := cot.BuildGeoChat(sender, dest, text, "", now)
	if err != nil {
		return func() tea.Msg { return ChatSendResultMsg{Err: err} }
	}
	if m.deps.Chat != nil {
		m.deps.Chat.AppendOutgoing(ev.UID, recipientUID, chatroom, text, sender.Callsign, now)
	}
	send := m.deps.Send
	id := ev.UID
	return func() tea.Msg {
		if err := send(ev); err != nil {
			return ChatSendResultMsg{ID: id, Err: err}
		}
		return ChatSendResultMsg{ID: id}
	}
}
