// Package tui implements the Bubble Tea program that ties together the cert
// enrollment flow, the TAK streaming client, the PLI publisher, the
// contacts/chat stores, and the world-map renderer.
//
// The TUI runs in three top-level modes:
//
//  1. ModeEnroll   — interactive form collecting host/port/username/password
//                    and running cert enrollment. Entered when no client
//                    cert exists or it has expired.
//  2. ModePosition — interactive form collecting lat/lon/callsign/group/
//                    role/PLI-interval. Entered after a fresh enrollment or
//                    when SelfPos lat/lon are still zero.
//  3. ModeMain     — the live situation picture: map pane, groups pane,
//                    contacts table, chat pane, status bar. Sub-modes for
//                    chat-input editing and pane focus.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/u0222-swe/terminaltak/internal/cot"
	"github.com/u0222-swe/terminaltak/internal/takclient"
)

// EventMsg wraps an inbound CoT event delivered by the takclient.
type EventMsg struct{ Event cot.Event }

// StatusMsg wraps a connection-state notification from the takclient.
type StatusMsg struct{ Status takclient.Status }

// TickMsg fires at the configured refresh rate; the model uses it to redraw
// derived state (cert expiry countdown, contact staleness) and prune stale
// contacts.
type TickMsg time.Time

// EnrollDoneMsg signals the result of an enrollment attempt. On success Err
// is nil and the writer should reload config from disk; on failure Err
// describes why so the form can re-prompt.
type EnrollDoneMsg struct {
	Host string
	Port int
	Err  error
}

// ChatSendResultMsg conveys the success/failure of a single outbound chat
// message so the chat pane can flip the per-message status badge.
type ChatSendResultMsg struct {
	ID  string
	Err error
}

// ChannelsMsg carries the user's authorised channels (the toggle-able list
// in the groups panel). Sourced from /Marti/api/groups/all and refreshed
// periodically by main.go. We keep the full Group objects (with direction
// + active state) rather than just names so the TUI can replay them back
// to the server when toggling.
type ChannelsMsg struct {
	Groups []ChannelGroup
	Err    error
}

// ChannelGroup mirrors martiapi.Group but is declared here so the TUI does
// not have to import martiapi just to render. Fields are kept aligned by
// the wiring layer in cmd/terminaltak/main.go.
type ChannelGroup struct {
	Name      string
	Direction string
	Type      string
	BitPos    int
	Active    bool
}

// ActiveChannelsResultMsg conveys success/failure of a PUT
// /Marti/api/groups/active call. The TUI uses it to surface server
// rejection in the status bar; on success no UI update is needed since
// the optimistic local toggle already reflects the new state.
type ActiveChannelsResultMsg struct {
	Err error
}

// DirectoryMsg carries a UID→channels mapping derived from polling
// /Marti/api/clientEndPoints. Used both to filter incoming events by the
// sender's channels and to label rows in the CoT log overlay.
type DirectoryMsg struct {
	UIDToChannels map[string][]string
	Err           error
}

// MarkerSendResultMsg conveys the success/failure of transmitting a dropped
// marker or a marker-delete event. The marker is already stored/removed
// locally; this only surfaces a wire error in the status bar.
type MarkerSendResultMsg struct {
	UID string
	Err error
}

// waitForEvent returns a tea.Cmd that blocks on the next inbound event and
// converts it into an EventMsg. After Update processes the message it must
// re-issue waitForEvent so the pump keeps running for the lifetime of the
// program.
func waitForEvent(ch <-chan cot.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return EventMsg{Event: ev}
	}
}

// waitForStatus mirrors waitForEvent for the takclient status channel.
func waitForStatus(ch <-chan takclient.Status) tea.Cmd {
	return func() tea.Msg {
		st, ok := <-ch
		if !ok {
			return nil
		}
		return StatusMsg{Status: st}
	}
}

// tick re-arms the periodic redraw timer.
func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return TickMsg(t) })
}
