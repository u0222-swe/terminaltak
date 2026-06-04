package tui

import (
	"crypto/x509"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/u0222-swe/terminaltak/internal/chat"
	"github.com/u0222-swe/terminaltak/internal/config"
	"github.com/u0222-swe/terminaltak/internal/contacts"
	"github.com/u0222-swe/terminaltak/internal/cot"
	"github.com/u0222-swe/terminaltak/internal/eventlog"
	"github.com/u0222-swe/terminaltak/internal/markers"
	"github.com/u0222-swe/terminaltak/internal/pli"
	"github.com/u0222-swe/terminaltak/internal/takclient"
)

// Mode is the top-level TUI state.
type Mode int

const (
	ModeEnroll Mode = iota
	ModePosition
	ModeMain
)

// Pane is the currently focused element in ModeMain.
type Pane int

const (
	PaneGroups Pane = iota
	PaneContacts
	PaneChat
)

// EnrollFunc runs the chosen enrollment / import path and returns nil on
// success after persisting cert.pem / key.pem / ca.pem to disk. The TUI
// invokes it as a tea.Cmd so the form stays responsive while the network
// call is in flight.
type EnrollFunc func(EnrollRequest) error

// SendFunc transmits a single CoT event. Typically takclient.Client.Send.
type SendFunc func(cot.Event) error

// SetActiveChannelsFunc applies the desired channel-active state to the
// server. The map is name -> active; the implementation in main.go
// translates this into a PUT /Marti/api/groups/active call. nil means
// "this server doesn't support remote channel toggle, just keep the
// local display filter".
type SetActiveChannelsFunc func(state map[string]bool) error

// Deps bundles the long-lived collaborators a Model needs. It is supplied
// once at construction time.
type Deps struct {
	Config            *config.Config
	Contacts          *contacts.Store
	Chat              *chat.Store
	EventLog          *eventlog.Store
	Markers           *markers.Store
	Publisher         *pli.Publisher
	Client            *takclient.Client
	Send              SendFunc
	Enroll            EnrollFunc
	ConfigSave        func(*config.Config) error
	ClientCert        *x509.Certificate // may be nil before enrollment
	SetActiveChannels SetActiveChannelsFunc
}

// Model is the root Bubble Tea model. Its concrete shape encodes which mode
// the program is in and which sub-form (enroll, position editor) is
// currently active.
type Model struct {
	deps Deps

	mode Mode
	pane Pane

	width, height int

	// Sub-models for each top-level mode. Only the current one is visible,
	// but all are kept alive so transitioning back preserves form state.
	enrollForm   enrollModel
	positionForm positionModel
	chatInput    chatInputModel

	// Connection status surfaced from takclient.
	connState takclient.State
	connErr   error
	since     time.Time

	// Counters and rolling stats.
	eventsTotal int
	eventsTick  int // events seen since last tick
	eventsRate  float64

	// Pane cursors — clamped to row count at use-time.
	groupCursor   int
	contactCursor int

	// CoT log overlay state. Hidden by default; toggle with "l".
	showLog   bool
	logCursor int

	// Point-dropper state. Inert unless dropper.active; see marker.go.
	dropper dropperState

	// Markers manager overlay state. Hidden by default; toggle with "M".
	showMarkers  bool
	markerCursor int

// Channels — sourced from /Marti/api/groups/all (the user's authorised
	// access groups, displayed as "channels" in the UI). channelGroups
	// retains direction/type so we can replay the full Group list back to
	// the server when toggling. channelEnabled mirrors the active state and
	// is the source of truth between server polls.
	channelGroups  []ChannelGroup
	channelEnabled map[string]bool

	// uidToChannels maps a connected client's UID to the channels they are
	// authorised under (from /Marti/api/clientEndPoints). Used to (a) filter
	// inbound events by enabled channels and (b) label the CoT log overlay
	// with the sender's channels rather than their team-colour.
	uidToChannels map[string][]string

	// Derived from cert.
	certCN     string
	certExpiry time.Time

	// Transient one-line message shown in the status bar in place of the
	// hint until flashUntil. Set via setFlash; cleared lazily by render.
	flash      string
	flashUntil time.Time

	// mapZoom scales the auto-fit viewport around its centre. 1.0 is the
	// default (AutoFit as computed); <1 zooms in, >1 zooms out. Clamped at
	// the keystroke handler so it never reaches degenerate spans.
	mapZoom float64
}

// setFlash queues a transient status-bar message visible for d.
func (m *Model) setFlash(msg string, d time.Duration) {
	m.flash = msg
	m.flashUntil = time.Now().Add(d)
}

// New constructs a Model in the appropriate starting Mode.
func New(deps Deps) Model {
	m := Model{
		deps:           deps,
		channelEnabled: map[string]bool{},
		uidToChannels:  map[string][]string{},
		mapZoom:        1.0,
	}
	m.enrollForm = newEnrollModel(deps.Config)
	m.positionForm = newPositionModel(deps.Config)
	m.chatInput = newChatInputModel()

	if deps.ClientCert != nil {
		m.certCN = deps.ClientCert.Subject.CommonName
		m.certExpiry = deps.ClientCert.NotAfter
	}

	switch {
	case deps.ClientCert == nil || !deps.ClientCert.NotAfter.After(time.Now()):
		m.mode = ModeEnroll
	case deps.Config.SelfPos.Lat == 0 && deps.Config.SelfPos.Lon == 0:
		m.mode = ModePosition
	default:
		m.mode = ModeMain
	}
	return m
}

// Init implements tea.Model. It returns commands to start pumping events,
// status notifications, and the periodic tick.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tick(m.tickInterval()),
	}
	if m.deps.Client != nil {
		cmds = append(cmds,
			waitForEvent(m.deps.Client.Events()),
			waitForStatus(m.deps.Client.Status()),
		)
	}
	return tea.Batch(cmds...)
}

func (m Model) tickInterval() time.Duration {
	hz := m.deps.Config.UI.RefreshHz
	if hz <= 0 {
		hz = 4
	}
	return time.Second / time.Duration(hz)
}

// Update implements tea.Model. It dispatches to a sub-handler based on the
// current Mode after first handling messages that are relevant in any mode.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		return m, nil

	case tea.KeyMsg:
		// Global escape hatch. tea.Interrupt (rather than tea.Quit) so the
		// program returns ErrInterrupted — the setup-phase loop in main.go
		// distinguishes "user aborted" from "step completed" and exits
		// instead of re-entering the setup TUI.
		if v.Type == tea.KeyCtrlC {
			return m, tea.Interrupt
		}

	case TickMsg:
		// Recalculate rolling event-rate, prune stale contacts, re-arm.
		m.eventsRate = float64(m.eventsTick) * float64(time.Second) / float64(m.tickInterval())
		m.eventsTick = 0
		if m.deps.Contacts != nil {
			m.deps.Contacts.PruneStale(time.Time(v))
		}
		return m, tick(m.tickInterval())

	case EventMsg:
		m.eventsTotal++
		m.eventsTick++
		if m.deps.Contacts != nil {
			m.deps.Contacts.Apply(v.Event)
		}
		if m.deps.Chat != nil {
			m.deps.Chat.IngestEvent(v.Event)
		}
		if m.deps.EventLog != nil {
			m.deps.EventLog.Apply(v.Event)
		}
		// Pump the next event.
		return m, waitForEvent(m.deps.Client.Events())

	case StatusMsg:
		m.connState = v.Status.State
		m.connErr = v.Status.Err
		m.since = v.Status.Since
		return m, waitForStatus(m.deps.Client.Status())

	case ChatSendResultMsg:
		if m.deps.Chat != nil {
			if v.Err != nil {
				m.deps.Chat.MarkStatus(v.ID, chat.StatusFailed)
			} else {
				m.deps.Chat.MarkStatus(v.ID, chat.StatusSent)
			}
		}
		return m, nil

	case ChannelsMsg:
		if v.Err == nil {
			m.channelGroups = append(m.channelGroups[:0], v.Groups...)
			// Server's active flag is authoritative — mirror it. The user's
			// optimistic toggle has already been PUT back to the server, so
			// this poll's result reflects that state already.
			for _, g := range v.Groups {
				m.channelEnabled[g.Name] = g.Active
			}
			// Push the active set to the PLI publisher so the next
			// emitted PLI is tagged with the right <marti><dest group/>
			// entries. Without this the publisher keeps using whatever
			// (possibly nil) ActiveChannels list it was constructed with.
			if m.deps.Publisher != nil {
				enabled := make([]string, 0, len(v.Groups))
				for _, g := range v.Groups {
					if m.channelEnabled[g.Name] {
						enabled = append(enabled, g.Name)
					}
				}
				m.deps.Publisher.SetActiveChannels(enabled)
			}
		}
		return m, nil

	case DirectoryMsg:
		if v.Err == nil {
			m.uidToChannels = v.UIDToChannels
		}
		return m, nil

	case EnrollDoneMsg:
		if v.Err != nil {
			m.enrollForm.lastErr = v.Err
			return m, nil
		}
		m.deps.Config.Server.Host = v.Host
		m.deps.Config.Server.EnrollPort = v.Port
		_ = m.deps.ConfigSave(m.deps.Config)
		// Switch to position editor next.
		m.mode = ModePosition
		m.positionForm.focus(0)
		return m, nil

	case MarkerSendResultMsg:
		if v.Err != nil {
			m.setFlash("marker send failed: "+v.Err.Error(), 5*time.Second)
		}
		return m, nil
	}

	switch m.mode {
	case ModeEnroll:
		return m.updateEnroll(msg)
	case ModePosition:
		return m.updatePosition(msg)
	case ModeMain:
		return m.updateMain(msg)
	}
	return m, nil
}

// View implements tea.Model.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initialising…"
	}
	switch m.mode {
	case ModeEnroll:
		return m.viewEnroll()
	case ModePosition:
		return m.viewPosition()
	case ModeMain:
		return m.viewMain()
	}
	return fmt.Sprintf("unknown mode %d", m.mode)
}

// senderAllowed reports whether events / messages from the given sender UID
// should be visible under the current channel filter. Rules:
//
//   - Empty UID → allow (own outgoing, server control messages, etc.).
//   - Once we know the user's channel list, "no channels enabled" means
//     hide everything — the user has explicitly muted all channels.
//   - Sender in our directory: allow if any of their channels is enabled.
//   - Sender NOT in our directory (federated tracks etc.): treated as if
//     they belong to every enabled channel — we cannot know their actual
//     channel without per-event attribution from the server, so we err on
//     the side of showing them when the user has any channel enabled, and
//     hiding them when the user has muted everything.
func (m Model) senderAllowed(uid string) bool {
	if uid == "" {
		return true
	}
	hasAnyEnabled := false
	if len(m.channelGroups) > 0 {
		for _, g := range m.channelGroups {
			if m.channelEnabled[g.Name] {
				hasAnyEnabled = true
				break
			}
		}
		if !hasAnyEnabled {
			return false // explicit mute-all
		}
	} else {
		// We have no channel list yet (server API not yet reachable). Show
		// everything — better than hiding while the panel boots.
		return true
	}

	chans, known := m.uidToChannels[uid]
	if !known || len(chans) == 0 {
		// Unknown sender (federation, data feed, late-joining client). At
		// least one of our channels is enabled per the check above; allow.
		return true
	}
	for _, c := range chans {
		if m.channelEnabled[c] {
			return true
		}
	}
	return false
}

// channelsForUID returns the sender's channels (or empty if unknown).
func (m Model) channelsForUID(uid string) []string {
	return m.uidToChannels[uid]
}

// connStateLabel returns a short human-readable label for the status bar.
func (m Model) connStateLabel() string {
	switch m.connState {
	case takclient.StateConnected:
		return "connected"
	case takclient.StateConnecting:
		return "connecting"
	case takclient.StateReconnecting:
		return "reconnecting"
	default:
		return "disconnected"
	}
}
