package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/u0222-swe/terminaltak/internal/cot"
	"github.com/u0222-swe/terminaltak/internal/markers"
)

// markerStaleAfter is how long peers retain a dropped marker before ageing it
// out. Markers are "sticky" relative to PLI, so this is generous.
const markerStaleAfter = 24 * time.Hour

// dropperStage is the two-step point-dropper flow: first aim a crosshair on
// the map, then fill in the marker's affiliation / label / remarks.
type dropperStage int

const (
	dropAim dropperStage = iota
	dropForm
)

// dropperAffiliations are the four point-dropper affiliations in cycle order.
var dropperAffiliations = []cot.Affiliation{
	cot.AffiliationFriendly,
	cot.AffiliationHostile,
	cot.AffiliationNeutral,
	cot.AffiliationUnknown,
}

func affiliationLabel(a cot.Affiliation) string {
	switch a {
	case cot.AffiliationFriendly:
		return "Friendly"
	case cot.AffiliationHostile:
		return "Hostile"
	case cot.AffiliationNeutral:
		return "Neutral"
	default:
		return "Unknown"
	}
}

// dropperState holds the live point-dropper. It is inert unless active.
type dropperState struct {
	active bool
	stage  dropperStage

	// lat/lon is the crosshair / chosen coordinate.
	lat, lon float64

	// form fields (stage == dropForm)
	affIdx   int
	lat_     textinput.Model
	lon_     textinput.Model
	label    textinput.Model
	remarks  textinput.Model
	focusIdx int
	err      string
}

// dropper form focus layout:
//
//	0: affiliation (←/→ cycles)
//	1: latitude    2: longitude
//	3: label       4: remarks
//	5: [ Drop ]    6: [ Cancel ]
const dropperFocusCount = 7

func (d *dropperState) dropIdx() int   { return 5 }
func (d *dropperState) cancelIdx() int { return 6 }

func (d *dropperState) formFields() []*textinput.Model {
	return []*textinput.Model{&d.lat_, &d.lon_, &d.label, &d.remarks}
}

func (d *dropperState) blurAll() {
	for _, ti := range d.formFields() {
		ti.Blur()
	}
}

func (d *dropperState) focus(i int) {
	if i < 0 {
		i = 0
	}
	if i >= dropperFocusCount {
		i = dropperFocusCount - 1
	}
	d.focusIdx = i
	d.blurAll()
	// Indices 1..4 map to the four text inputs.
	if i >= 1 && i <= 4 {
		d.formFields()[i-1].Focus()
	}
}

// startDropper enters aim mode with the crosshair at the user's own position
// (or the current map centre when no self position is set).
func (m Model) startDropper() Model {
	iw, ih := m.mapInnerDims()
	view, selfLat, selfLon := m.mapView(iw, ih)
	lat := (view.MinLat + view.MaxLat) / 2
	lon := (view.MinLon + view.MaxLon) / 2
	if selfLat != 0 || selfLon != 0 {
		lat, lon = selfLat, selfLon
	}
	m.dropper = dropperState{active: true, stage: dropAim, lat: lat, lon: lon}
	return m
}

// dropperStep returns one cell's worth of lat/lon at the current zoom, so the
// crosshair moves a visible step per keypress regardless of how zoomed-in the
// map is.
func (m Model) dropperStep() (latStep, lonStep float64) {
	iw, ih := m.mapInnerDims()
	view, _, _ := m.mapView(iw, ih)
	if ih < 1 {
		ih = 1
	}
	if iw < 1 {
		iw = 1
	}
	return (view.MaxLat - view.MinLat) / float64(ih), (view.MaxLon - view.MinLon) / float64(iw)
}

// updateDropper handles all keystrokes while the point dropper is active.
func (m Model) updateDropper(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.dropper.stage == dropAim {
		return m.updateDropperAim(k)
	}
	return m.updateDropperForm(msg, k)
}

func (m Model) updateDropperAim(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	latStep, lonStep := m.dropperStep()
	switch k.String() {
	case "esc":
		m.dropper = dropperState{}
		return m, nil
	case "up", "k":
		m.dropper.lat = clampLat(m.dropper.lat + latStep)
	case "down", "j":
		m.dropper.lat = clampLat(m.dropper.lat - latStep)
	case "left", "h":
		m.dropper.lon = clampLon(m.dropper.lon - lonStep)
	case "right", "l":
		m.dropper.lon = clampLon(m.dropper.lon + lonStep)
	case "+", "=":
		m.mapZoom *= 0.7
		if m.mapZoom < 0.05 {
			m.mapZoom = 0.05
		}
	case "-", "_":
		m.mapZoom *= 1.4
		if m.mapZoom > 5.0 {
			m.mapZoom = 5.0
		}
	case "enter":
		m = m.enterDropperForm()
	}
	return m, nil
}

// enterDropperForm transitions from aim to the detail form, seeding the coord
// inputs from the crosshair position.
func (m Model) enterDropperForm() Model {
	mk := func(prompt, val string) textinput.Model {
		ti := textinput.New()
		ti.Prompt = "  " + prompt + ": "
		ti.SetValue(val)
		ti.Width = 36
		return ti
	}
	d := &m.dropper
	d.stage = dropForm
	d.affIdx = 0
	d.err = ""
	d.lat_ = mk("latitude ", strconv.FormatFloat(d.lat, 'f', 5, 64))
	d.lon_ = mk("longitude", strconv.FormatFloat(d.lon, 'f', 5, 64))
	d.label = mk("label    ", "")
	d.remarks = mk("remarks  ", "")
	d.focus(0)
	return m
}

func (m Model) updateDropperForm(msg tea.Msg, k tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &m.dropper
	switch k.Type {
	case tea.KeyEsc:
		m.dropper = dropperState{}
		return m, nil
	case tea.KeyTab, tea.KeyDown:
		d.focus((d.focusIdx + 1) % dropperFocusCount)
		return m, nil
	case tea.KeyShiftTab, tea.KeyUp:
		d.focus((d.focusIdx - 1 + dropperFocusCount) % dropperFocusCount)
		return m, nil
	case tea.KeyLeft:
		if d.focusIdx == 0 {
			d.affIdx = (d.affIdx - 1 + len(dropperAffiliations)) % len(dropperAffiliations)
			return m, nil
		}
	case tea.KeyRight:
		if d.focusIdx == 0 {
			d.affIdx = (d.affIdx + 1) % len(dropperAffiliations)
			return m, nil
		}
	case tea.KeyEnter:
		switch d.focusIdx {
		case d.cancelIdx():
			m.dropper = dropperState{}
			return m, nil
		case 0:
			// affiliation row — step into the first field
			d.focus(1)
			return m, nil
		default:
			return m.submitMarker()
		}
	}
	// Route typing to the focused text input.
	if d.focusIdx >= 1 && d.focusIdx <= 4 {
		fields := d.formFields()
		var cmd tea.Cmd
		*fields[d.focusIdx-1], cmd = fields[d.focusIdx-1].Update(msg)
		return m, cmd
	}
	return m, nil
}

// submitMarker validates the form, stores the marker locally for instant
// feedback, and dispatches the CoT event.
func (m Model) submitMarker() (tea.Model, tea.Cmd) {
	d := &m.dropper
	lat, err := parseCoord(d.lat_.Value())
	if err != nil || lat < -90 || lat > 90 {
		d.err = "latitude must be a decimal between -90 and 90"
		return m, nil
	}
	lon, err := parseCoord(d.lon_.Value())
	if err != nil || lon < -180 || lon > 180 {
		d.err = "longitude must be a decimal between -180 and 180"
		return m, nil
	}
	aff := dropperAffiliations[d.affIdx]
	label := strings.TrimSpace(d.label.Value())
	if label == "" {
		label = affiliationLabel(aff) + " marker"
	}
	remarks := strings.TrimSpace(d.remarks.Value())

	cmd := m.dropMarkerCmd(aff, lat, lon, label, remarks)
	m.dropper = dropperState{}
	m.setFlash("marker dropped: "+label, 4*time.Second)
	return m, cmd
}

// dropMarkerCmd builds the marker CoT, records it in the local store, and
// returns a command that transmits it.
func (m Model) dropMarkerCmd(aff cot.Affiliation, lat, lon float64, label, remarks string) tea.Cmd {
	cfg := m.deps.Config
	cotType := cot.MarkerType(aff)
	hae := cfg.SelfPos.HAE
	now := time.Now()
	ev, uid, err := cot.BuildMarker(cfg.SelfPos.UID, "", cotType, label, remarks, lat, lon, hae, markerStaleAfter, now)
	if err != nil {
		return func() tea.Msg { return MarkerSendResultMsg{Err: err} }
	}
	if m.deps.Markers != nil {
		m.deps.Markers.Add(markers.Marker{
			UID:         uid,
			Label:       label,
			Remarks:     remarks,
			Type:        cotType,
			Affiliation: aff,
			Lat:         lat,
			Lon:         lon,
			HAE:         hae,
			Created:     now,
		})
	}
	send := m.deps.Send
	if send == nil {
		return func() tea.Msg { return MarkerSendResultMsg{UID: uid} }
	}
	return func() tea.Msg {
		return MarkerSendResultMsg{UID: uid, Err: send(ev)}
	}
}

// deleteMarkerCmd removes a marker locally and broadcasts a CoT delete.
func (m Model) deleteMarkerCmd(uid string) tea.Cmd {
	mk, ok := m.deps.Markers.Get(uid)
	if !ok {
		return nil
	}
	m.deps.Markers.Remove(uid)
	send := m.deps.Send
	if send == nil {
		return nil
	}
	now := time.Now()
	return func() tea.Msg {
		ev, err := cot.BuildMarkerDelete(uid, mk.Type, now)
		if err != nil {
			return MarkerSendResultMsg{UID: uid, Err: err}
		}
		return MarkerSendResultMsg{UID: uid, Err: send(ev)}
	}
}

// --- markers overlay (list + delete) ---------------------------------------

func (m Model) updateMarkersOverlay(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := 0
	if m.deps.Markers != nil {
		n = m.deps.Markers.Len()
	}
	switch k.String() {
	case "M", "esc", "q":
		m.showMarkers = false
		m.markerCursor = 0
		return m, nil
	case "up", "k":
		m.markerCursor = clamp(m.markerCursor-1, 0, n-1)
		return m, nil
	case "down", "j":
		m.markerCursor = clamp(m.markerCursor+1, 0, n-1)
		return m, nil
	case "d", "x", "delete", "backspace":
		if m.deps.Markers == nil {
			return m, nil
		}
		snap := m.deps.Markers.Snapshot()
		if m.markerCursor < 0 || m.markerCursor >= len(snap) {
			return m, nil
		}
		target := snap[m.markerCursor]
		cmd := m.deleteMarkerCmd(target.UID)
		m.setFlash("marker deleted: "+target.Label, 4*time.Second)
		m.markerCursor = clamp(m.markerCursor, 0, m.deps.Markers.Len()-1)
		return m, cmd
	}
	return m, nil
}

func (m Model) viewMarkersOverlay() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("237"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("12"))

	innerW := m.width - 2
	innerH := m.height - 4
	if innerW < 30 {
		innerW = 30
	}
	if innerH < 8 {
		innerH = 8
	}

	var snap []markers.Marker
	if m.deps.Markers != nil {
		snap = m.deps.Markers.Snapshot()
	}
	lines := []string{hintStyle.Render(fmt.Sprintf("%-10s %-18s %10s %10s  %s", "aff", "label", "lat", "lon", "remarks"))}
	if len(snap) == 0 {
		lines = append(lines, "", hintStyle.Render("  (no markers yet — press m on the map to drop one)"))
	}
	for i, mk := range snap {
		glyph, color := runeForAffiliation(mk.Affiliation)
		dot := lipgloss.NewStyle().Foreground(color).Render(string(glyph))
		row := fmt.Sprintf("%s %-8s %-18s %10.5f %10.5f  %s",
			dot, affiliationLabel(mk.Affiliation), truncate(mk.Label, 18), mk.Lat, mk.Lon, truncate(mk.Remarks, 24))
		if i == m.markerCursor {
			row = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("236")).Render("▸ " + row)
		} else {
			row = "  " + row
		}
		lines = append(lines, truncate(row, innerW))
	}

	titleBar := titleStyle.Width(m.width).Render(fmt.Sprintf(" markers — %d placed ", len(snap)))
	hint := hintStyle.Render("  M/Esc close · ↑↓ select · d delete (broadcasts CoT delete)")
	return lipgloss.JoinVertical(lipgloss.Left,
		titleBar,
		box.Width(m.width).Height(innerH).Render(strings.Join(lines, "\n")),
		hint,
	)
}

// viewMarkerForm renders the detail form (stage == dropForm) as a centred
// panel, mirroring the position editor's layout.
func (m Model) viewMarkerForm() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	d := &m.dropper

	aff := dropperAffiliations[d.affIdx]
	glyph, color := runeForAffiliation(aff)
	affRow := fmt.Sprintf("affiliation: %s %s", lipgloss.NewStyle().Foreground(color).Render(string(glyph)), affiliationLabel(aff))
	if d.focusIdx == 0 {
		affRow = cursorStyle.Render("▸ ") + affRow + "   " + hintStyle.Render("← →")
	} else {
		affRow = "  " + affRow
	}

	lines := []string{
		titleStyle.Render("Drop marker"),
		"",
		hintStyle.Render("  Tab/↑↓ move · ←/→ change affiliation · Enter drop · Esc cancel"),
		"",
		affRow,
		"",
		d.lat_.View(),
		d.lon_.View(),
		d.label.View(),
		d.remarks.View(),
	}

	dropLabel := "[ Drop ]"
	if d.focusIdx == d.dropIdx() {
		dropLabel = cursorStyle.Render("▸ ") + dropLabel + "   " + hintStyle.Render("broadcast marker")
	} else {
		dropLabel = "  " + dropLabel
	}
	cancelLabel := "[ Cancel ]"
	if d.focusIdx == d.cancelIdx() {
		cancelLabel = cursorStyle.Render("▸ ") + cancelLabel
	} else {
		cancelLabel = "  " + cancelLabel
	}
	lines = append(lines, "", dropLabel, cancelLabel)
	if d.err != "" {
		lines = append(lines, "", errStyle.Render("  "+d.err))
	}
	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
}

func clampLat(v float64) float64 {
	if v < -90 {
		return -90
	}
	if v > 90 {
		return 90
	}
	return v
}

func clampLon(v float64) float64 {
	if v < -180 {
		return -180
	}
	if v > 180 {
		return 180
	}
	return v
}
