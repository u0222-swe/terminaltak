package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/u0222-swe/terminaltak/internal/chat"
	"github.com/u0222-swe/terminaltak/internal/contacts"
	"github.com/u0222-swe/terminaltak/internal/cot"
	"github.com/u0222-swe/terminaltak/internal/takclient"
	"github.com/u0222-swe/terminaltak/internal/worldmap"
)

// isTAKMember reports whether the contact is a real TAK client (one whose
// CoT carried a <contact callsign="…"/> element). Bare CoT producers
// (sims, sensors, federated tracks) typically lack that element, so they
// have no endpoint to receive a GeoChat DM at.
func isTAKMember(c contacts.Contact) bool {
	return c.Callsign != ""
}

// displayName returns Callsign when set, otherwise a UID-derived fallback so
// the contacts list never shows a blank row. The "~" prefix marks it as a
// synthesised name.
func displayName(c contacts.Contact) string {
	if c.Callsign != "" {
		return c.Callsign
	}
	uid := c.UID
	if len(uid) > 8 {
		uid = uid[len(uid)-8:]
	}
	return "~" + uid
}

// runeForAffiliation picks a colored rune for a contact based on its CoT
// affiliation segment (a-f / a-h / a-n / a-u …).
func runeForAffiliation(a cot.Affiliation) (rune, lipgloss.Color) {
	switch a {
	case cot.AffiliationFriendly, cot.AffiliationAssumedFriend:
		return '●', lipgloss.Color("12") // blue
	case cot.AffiliationHostile:
		return '▲', lipgloss.Color("9") // red
	case cot.AffiliationNeutral:
		return '■', lipgloss.Color("10") // green
	case cot.AffiliationSuspect:
		return '◆', lipgloss.Color("13") // magenta
	default:
		return '?', lipgloss.Color("11") // yellow
	}
}

func (m Model) updateMain(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}

	if m.showLog {
		newM, cmd, _ := m.updateLog(msg)
		return newM, cmd
	}

	if m.chatInput.active {
		switch k.Type {
		case tea.KeyEsc:
			m.chatInput.cancel()
			return m, nil
		case tea.KeyEnter:
			text := strings.TrimSpace(m.chatInput.input.Value())
			if text == "" {
				m.chatInput.cancel()
				return m, nil
			}
			cmd := m.sendChatCmd(text)
			m.chatInput.cancel()
			return m, cmd
		}
		var cmd tea.Cmd
		m.chatInput.input, cmd = m.chatInput.input.Update(msg)
		return m, cmd
	}

	switch k.String() {
	case "q":
		return m, tea.Quit
	case "r":
		// Reconnect placeholder — needs takclient.Reconnect() to be useful.
		return m, nil
	case "tab":
		m.pane = (m.pane + 1) % 3
		return m, nil
	case "shift+tab":
		m.pane = (m.pane + 2) % 3
		return m, nil
	case " ":
		if m.pane == PaneGroups {
			if name, ok := m.selectedChannelName(); ok {
				m.channelEnabled[name] = !m.channelEnabled[name]
				return m, m.applyActiveChannelsCmd()
			}
		}
	case "a":
		if m.pane == PaneGroups {
			for _, g := range m.channelGroups {
				m.channelEnabled[g.Name] = true
			}
			return m, m.applyActiveChannelsCmd()
		}
	case "n":
		if m.pane == PaneGroups {
			for _, g := range m.channelGroups {
				m.channelEnabled[g.Name] = false
			}
			return m, m.applyActiveChannelsCmd()
		}
	case "i":
		switch m.pane {
		case PaneChat:
			m.chatInput.startAllChat()
			return m, nil
		case PaneContacts:
			if c, ok := m.selectedContact(); ok {
				if !isTAKMember(c) {
					m.setFlash("not a TAK client — chat disabled for "+displayName(c), 4*time.Second)
					return m, nil
				}
				m.chatInput.startDM(c.UID, c.Callsign)
				return m, nil
			}
		}
	case "p":
		m.mode = ModePosition
		m.positionForm.focus(0)
		return m, nil
	case "l":
		m.showLog = true
		m.logCursor = 0
		return m, nil
	case "up", "k":
		switch m.pane {
		case PaneGroups:
			m.groupCursor = clamp(m.groupCursor-1, 0, len(uniqueChannels(m.channelGroups))-1)
		case PaneContacts:
			m.contactCursor = clamp(m.contactCursor-1, 0, len(m.sortedContacts())-1)
		}
		return m, nil
	case "down", "j":
		switch m.pane {
		case PaneGroups:
			m.groupCursor = clamp(m.groupCursor+1, 0, len(uniqueChannels(m.channelGroups))-1)
		case PaneContacts:
			m.contactCursor = clamp(m.contactCursor+1, 0, len(m.sortedContacts())-1)
		}
		return m, nil
	}
	return m, nil
}

// applyActiveChannelsCmd dispatches the current channelEnabled map to the
// server (PUT /Marti/api/groups/activebits) and clears local caches that
// would otherwise show stale data from now-disabled channels until each
// entry's stale timer fires. takclient.Reconnect inside SetActiveChannels
// also rebuilds the server-side bit vector from the fresh cache.
func (m Model) applyActiveChannelsCmd() tea.Cmd {
	state := make(map[string]bool, len(m.channelGroups))
	enabledNames := make([]string, 0, len(m.channelGroups))
	for _, g := range m.channelGroups {
		state[g.Name] = m.channelEnabled[g.Name]
		if m.channelEnabled[g.Name] {
			enabledNames = append(enabledNames, g.Name)
		}
	}
	if m.deps.Publisher != nil {
		m.deps.Publisher.SetActiveChannels(enabledNames)
	}
	// Wipe contact cache so the panel and map redraw from active-channel
	// events only. Federation tracks (which have no entry in the UID
	// directory and therefore evade our senderAllowed filter) get
	// dropped here too — the server-side filter then prevents them from
	// reappearing if their channel is now disabled.
	if m.deps.Contacts != nil {
		m.deps.Contacts.Clear()
	}
	if m.deps.SetActiveChannels == nil {
		return nil
	}
	fn := m.deps.SetActiveChannels
	return func() tea.Msg {
		return ActiveChannelsResultMsg{Err: fn(state)}
	}
}

func clamp(x, lo, hi int) int {
	if hi < 0 {
		return 0
	}
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func (m Model) selectedChannelName() (string, bool) {
	rows := uniqueChannels(m.channelGroups)
	if m.groupCursor < 0 || m.groupCursor >= len(rows) {
		return "", false
	}
	return rows[m.groupCursor].Name, true
}

// channelRow is the deduplicated, display-ready channel row used by the
// groups pane and cursor logic. The TAK server lists each channel twice
// when both IN and OUT are authorised; we collapse those into a single
// row with a "(IN/OUT)" suffix so the user does not have to toggle two
// rows that always move together.
type channelRow struct {
	Name       string
	Directions string // "IN", "OUT", or "IN/OUT"
}

func uniqueChannels(gs []ChannelGroup) []channelRow {
	byName := make(map[string]map[string]struct{}, len(gs))
	for _, g := range gs {
		if byName[g.Name] == nil {
			byName[g.Name] = map[string]struct{}{}
		}
		byName[g.Name][g.Direction] = struct{}{}
	}
	rows := make([]channelRow, 0, len(byName))
	for name, dirs := range byName {
		hasIn, hasOut := false, false
		for d := range dirs {
			switch d {
			case "IN":
				hasIn = true
			case "OUT":
				hasOut = true
			}
		}
		var label string
		switch {
		case hasIn && hasOut:
			label = "IN/OUT"
		case hasIn:
			label = "IN"
		case hasOut:
			label = "OUT"
		}
		rows = append(rows, channelRow{Name: name, Directions: label})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func (m Model) selectedContact() (contacts.Contact, bool) {
	cs := m.sortedContacts()
	if len(cs) == 0 || m.contactCursor < 0 || m.contactCursor >= len(cs) {
		return contacts.Contact{}, false
	}
	return cs[m.contactCursor], true
}

// sortedContacts returns contacts that pass the channel filter (sender's
// channels intersect the enabled set), sorted by recency (most recent
// first). Recency matters more than name order for ops: fresh tracks should
// be visible at a glance, and the contacts panel has a fixed height that
// clips long lists. Ties break case-insensitively on displayName so two
// contacts with identical LastSeen render deterministically.
func (m Model) sortedContacts() []contacts.Contact {
	if m.deps.Contacts == nil {
		return nil
	}
	cs := m.deps.Contacts.Snapshot(func(c contacts.Contact) bool {
		return m.senderAllowed(c.UID)
	})
	sort.Slice(cs, func(i, j int) bool {
		if !cs[i].LastSeen.Equal(cs[j].LastSeen) {
			return cs[i].LastSeen.After(cs[j].LastSeen)
		}
		return strings.ToLower(displayName(cs[i])) < strings.ToLower(displayName(cs[j]))
	})
	return cs
}

// viewMain composes the whole live screen.
func (m Model) viewMain() string {
	if m.showLog {
		return m.viewLogOverlay()
	}
	cellTitle := m.titleBar()
	cellStatus := m.statusBar()

	innerH := m.height - 2
	if innerH < 10 {
		innerH = 10
	}
	mapH := innerH * 6 / 10
	bottomH := innerH - mapH

	mapStr := m.viewMap(m.width, mapH)

	bottomCols := 3
	colW := m.width / bottomCols
	groupsW := colW
	contactsW := colW
	chatW := m.width - groupsW - contactsW

	groupsView := m.viewChannels(groupsW, bottomH)
	contactsView := m.viewContacts(contactsW, bottomH)
	chatView := m.viewChat(chatW, bottomH)

	bottom := lipgloss.JoinHorizontal(lipgloss.Top, groupsView, contactsView, chatView)
	body := lipgloss.JoinVertical(lipgloss.Left, cellTitle, mapStr, bottom, cellStatus)
	return body
}

func (m Model) titleBar() string {
	style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("237")).Width(m.width)
	cfg := m.deps.Config
	left := "TerminalTAK"
	mid := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.StreamPort)
	if m.certCN != "" {
		mid += "  CN=" + m.certCN
	}
	right := cfg.SelfPos.Callsign
	if cfg.SelfPos.Group != "" {
		right += " · " + cfg.SelfPos.Group
	}
	pad := m.width - len(left) - len(mid) - len(right)
	if pad < 1 {
		pad = 1
	}
	return style.Render(left + strings.Repeat(" ", pad/2) + mid + strings.Repeat(" ", pad-pad/2) + right)
}

func (m Model) statusBar() string {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("237")).Width(m.width)
	state := m.connStateLabel()
	if m.connState != takclient.StateConnected && m.connErr != nil {
		state += " (" + truncate(m.connErr.Error(), 30) + ")"
	}
	expiry := "no cert"
	if !m.certExpiry.IsZero() {
		left := time.Until(m.certExpiry).Round(time.Hour)
		expiry = "cert expires " + m.certExpiry.Format("2006-01-02") + " (" + left.String() + ")"
	}
	tail := "q quit · Tab pane · Space toggle · a all · n none · i chat · p pos · l log"
	if m.flash != "" && time.Now().Before(m.flashUntil) {
		tail = m.flash
	}
	body := fmt.Sprintf("%s · %s · %.1f ev/s · %s", state, expiry, m.eventsRate, tail)
	return style.Render(truncate(body, m.width))
}

func (m Model) viewMap(width, height int) string {
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("237"))
	innerW := width - 2
	innerH := height - 2
	if innerW <= 0 || innerH <= 0 {
		return box.Width(width).Height(height).Render("")
	}

	cs := m.sortedContacts()
	coords := make([]worldmap.LatLon, 0, len(cs)+1)
	for _, c := range cs {
		coords = append(coords, worldmap.LatLon{Lat: c.Lat, Lon: c.Lon})
	}
	// Self position: prefer the publisher's current position so a
	// random-walk roll between PLI ticks is reflected on the map. Fall
	// back to cfg when no publisher is wired (e.g. enroll-only run).
	cfg := m.deps.Config
	selfLat, selfLon := cfg.SelfPos.Lat, cfg.SelfPos.Lon
	if m.deps.Publisher != nil {
		if lat, lon, _ := m.deps.Publisher.Position(); lat != 0 || lon != 0 {
			selfLat, selfLon = lat, lon
		}
	}
	if selfLat != 0 || selfLon != 0 {
		coords = append(coords, worldmap.LatLon{Lat: selfLat, Lon: selfLon})
	}
	// AutoFit selects a tight bbox. If the user is in Sweden we widen the
	// bbox so all of Sweden is always visible — that is the natural
	// minimum-zoom for a Swedish operator. AspectFit then expands one
	// axis so the equirectangular projection plus terminal cell aspect
	// ratio do not flatten the map horizontally at high latitudes.
	view := worldmap.AutoFit(coords)
	if worldmap.SwedenBBox.Contains(selfLat, selfLon) {
		view = view.Expand(worldmap.SwedenBBox)
	}
	view = view.AspectFit(innerW, innerH)

	// Determine which contact (if any) is currently selected so we can
	// overlay it as a distinct 'X' marker on the map. When the user is
	// actively cursoring the contacts pane, also recentre the viewport on
	// that contact (keeping the current span — no zoom change) so the
	// chosen track is easy to find on a busy map.
	selectedUID := ""
	if m.pane == PaneContacts {
		if c, ok := m.selectedContact(); ok {
			selectedUID = c.UID
			view = view.CenterOn(c.Lat, c.Lon)
		}
	}

	canvas := worldmap.Basemap(view, innerW, innerH)
	for _, c := range cs {
		col, row := view.Project(c.Lat, c.Lon, innerW, innerH)
		r, _ := runeForAffiliation(c.Affiliation)
		if c.UID == selectedUID {
			r = 'X'
		}
		canvas[row][col] = r
	}
	if selfLat != 0 || selfLon != 0 {
		col, row := view.Project(selfLat, selfLon, innerW, innerH)
		canvas[row][col] = '◎'
	}

	var b strings.Builder
	for i, row := range canvas {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(string(row))
	}
	return box.Width(width).Height(height).Render(b.String())
}

// viewChannels renders the channels pane — the user's authorised access
// groups (sourced from /Marti/api/groups/all) with toggle state. Toggling
// fires PUT /Marti/api/groups/active so the server-side filter is updated
// in both directions: disabled channels stop receiving inbound events AND
// stop seeing the user's own PLI broadcasts.
func (m Model) viewChannels(width, height int) string {
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(borderColor(m.pane == PaneGroups)).Width(width).Height(height)
	rows := []string{lipgloss.NewStyle().Bold(true).Render(" channels")}
	if len(m.channelGroups) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("  (server API not reachable yet)"))
	}
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	for i, ch := range uniqueChannels(m.channelGroups) {
		check := "[ ]"
		if m.channelEnabled[ch.Name] {
			check = "[x]"
		}
		line := fmt.Sprintf("%s %s %s", check, ch.Name, dimStyle.Render("("+ch.Directions+")"))
		if i == m.groupCursor && m.pane == PaneGroups {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("236")).Render("▸ " + line)
		} else {
			line = "  " + line
		}
		rows = append(rows, line)
	}
	return style.Render(strings.Join(rows, "\n"))
}

func (m Model) viewContacts(width, height int) string {
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(borderColor(m.pane == PaneContacts)).Width(width).Height(height)
	cs := m.sortedContacts()
	rows := []string{lipgloss.NewStyle().Bold(true).Render(" contacts")}
	if len(cs) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("  (waiting for events)"))
	}
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	for i, c := range cs {
		var marker string
		if isTAKMember(c) {
			_, color := runeForAffiliation(c.Affiliation)
			marker = lipgloss.NewStyle().Foreground(color).Render("●")
		} else {
			marker = dimStyle.Render("?")
		}
		body := fmt.Sprintf("%s %-12s %5.2f,%6.2f", marker, truncate(displayName(c), 12), c.Lat, c.Lon)
		if !isTAKMember(c) {
			body = dimStyle.Render(body)
		}
		var line string
		if i == m.contactCursor && m.pane == PaneContacts {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("236")).Render("▸ " + body)
		} else {
			line = "  " + body
		}
		rows = append(rows, line)
	}
	return style.Render(strings.Join(rows, "\n"))
}

func (m Model) viewChat(width, height int) string {
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(borderColor(m.pane == PaneChat)).Width(width).Height(height)
	if m.deps.Chat == nil {
		return style.Render(" chat\n  (disabled)")
	}
	rows := []string{lipgloss.NewStyle().Bold(true).Render(" chat")}
	// Show every message (All-Chat broadcast + DMs) so the user sees a
	// single chronological log. DMs are marked inline so they are obvious.
	msgs := m.deps.Chat.All(m.senderAllowed)
	maxLines := height - 4
	if len(msgs) > maxLines {
		msgs = msgs[len(msgs)-maxLines:]
	}
	if len(msgs) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("  (no messages yet)"))
	}
	dmStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("13")) // magenta for DM
	for _, msg := range msgs {
		marker := ""
		switch msg.Status {
		case chat.StatusPending:
			marker = "…"
		case chat.StatusFailed:
			marker = "!"
		}
		ts := msg.Time.Format("15:04")
		from := msg.SenderCallsign
		if from == "" {
			from = "me"
		}
		isDM := msg.RecipientUID != "" && msg.RecipientUID != chat.AllChatRoom &&
			msg.Chatroom != chat.AllChatRoom
		var line string
		if isDM {
			arrow := "→"
			peer := msg.Chatroom
			if msg.Direction == chat.DirectionIn {
				arrow = "←"
				peer = from
				from = "me"
			}
			line = dmStyle.Render(fmt.Sprintf("[%s] %s%s %s %s: %s",
				ts, marker, from, arrow, peer, msg.Text))
		} else {
			line = fmt.Sprintf("[%s] %s%s: %s", ts, marker, from, msg.Text)
		}
		rows = append(rows, truncate(line, width-2))
	}
	if m.chatInput.active {
		rows = append(rows, "")
		rows = append(rows, "> "+m.chatInput.input.View())
	} else {
		rows = append(rows, "")
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("  press i to send All-Chat (or i on a contact for DM)"))
	}
	return style.Render(strings.Join(rows, "\n"))
}

func borderColor(focused bool) lipgloss.Color {
	if focused {
		return lipgloss.Color("12")
	}
	return lipgloss.Color("237")
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}
