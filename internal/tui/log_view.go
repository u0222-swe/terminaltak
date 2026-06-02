package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/u0222-swe/terminaltak/internal/eventlog"
)

// updateLog handles keystrokes while the CoT log overlay is open.
func (m Model) updateLog(msg tea.Msg) (Model, tea.Cmd, bool) {
	if !m.showLog {
		return m, nil, false
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil, false
	}
	switch k.String() {
	case "l", "esc", "q":
		m.showLog = false
		m.logCursor = 0
		return m, nil, true
	case "up", "k":
		if m.logCursor > 0 {
			m.logCursor--
		}
		return m, nil, true
	case "down", "j":
		max := m.visibleLogEntries() - 1
		if max < 0 {
			max = 0
		}
		if m.logCursor < max {
			m.logCursor++
		}
		return m, nil, true
	case "pgup":
		m.logCursor -= 10
		if m.logCursor < 0 {
			m.logCursor = 0
		}
		return m, nil, true
	case "pgdown":
		m.logCursor += 10
		max := m.visibleLogEntries() - 1
		if max < 0 {
			max = 0
		}
		if m.logCursor > max {
			m.logCursor = max
		}
		return m, nil, true
	case "home":
		m.logCursor = 0
		return m, nil, true
	}
	return m, nil, true
}

func (m Model) visibleLogEntries() int {
	if m.deps.EventLog == nil {
		return 0
	}
	return len(m.recentLogEntries(0))
}

// recentLogEntries returns the entries the overlay would render, applying
// the same channel filter as the rest of the TUI.
func (m Model) recentLogEntries(limit int) []eventlog.Entry {
	if m.deps.EventLog == nil {
		return nil
	}
	return m.deps.EventLog.Recent(m.senderAllowed, limit)
}

// activeChannels returns the names of currently-enabled channels —
// used in the log overlay header.
func (m Model) activeChannels() []string {
	out := make([]string, 0, len(m.channelGroups))
	for _, g := range m.channelGroups {
		if m.channelEnabled[g.Name] {
			out = append(out, g.Name)
		}
	}
	return out
}

// viewLogOverlay renders the full-screen CoT log view with newest events on
// top. Each row shows the sender's channels (resolved live from the
// directory) rather than the team-colour from the wire — channels are what
// the user actually thinks in.
func (m Model) viewLogOverlay() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("237"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245"))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("12"))

	headerLine := headerStyle.Render(fmt.Sprintf("%-8s %-14s %-12s %-24s %s",
		"time", "callsign", "type", "channels", "lat,lon"))

	chans := m.activeChannels()
	subtitle := "all channels"
	if len(chans) > 0 {
		subtitle = "active: " + strings.Join(chans, ", ")
	} else if len(m.channelGroups) > 0 {
		subtitle = "(no channels enabled)"
	}

	innerW := m.width - 2
	innerH := m.height - 4
	if innerW < 30 {
		innerW = 30
	}
	if innerH < 10 {
		innerH = 10
	}
	rowsAvail := innerH - 2
	if rowsAvail < 1 {
		rowsAvail = 1
	}

	entries := m.recentLogEntries(m.logCursor + rowsAvail)
	if m.logCursor > len(entries) {
		m.logCursor = 0
	}
	if m.logCursor > 0 && m.logCursor < len(entries) {
		entries = entries[m.logCursor:]
	}
	if len(entries) > rowsAvail {
		entries = entries[:rowsAvail]
	}

	lines := []string{headerLine, ""}
	if len(entries) == 0 {
		lines = append(lines, hintStyle.Render("(no events captured yet — keep this open while events stream in)"))
	}
	for _, e := range entries {
		ts := e.Time.Format("15:04:05")
		cs := truncate(e.Callsign, 14)
		typ := truncate(e.Type, 12)
		channelsStr := strings.Join(m.channelsForUID(e.UID), ",")
		if channelsStr == "" {
			channelsStr = hintStyle.Render("?")
		}
		channelsStr = truncate(channelsStr, 24)
		_, color := runeForAffiliation(e.Affiliation)
		dot := lipgloss.NewStyle().Foreground(color).Render("●")
		line := fmt.Sprintf("%s %s %-14s %-12s %-24s %.2f,%.2f", ts, dot, cs, typ, channelsStr, e.Lat, e.Lon)
		lines = append(lines, truncate(line, innerW))
	}

	body := strings.Join(lines, "\n")

	titleBar := titleStyle.Width(m.width).Render(fmt.Sprintf(" CoT log — %s · %d shown · cursor %d ",
		subtitle, len(entries), m.logCursor))
	hint := hintStyle.Render("  l/Esc close · ↑↓ scroll · PgUp/PgDn page · Home top")

	overlay := lipgloss.JoinVertical(lipgloss.Left,
		titleBar,
		box.Width(m.width).Height(innerH).Render(body),
		hint,
	)
	return overlay
}
