package tui

import (
	"math/rand"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/u0222-swe/terminaltak/internal/config"
	"github.com/u0222-swe/terminaltak/internal/cot"
	"github.com/u0222-swe/terminaltak/internal/mgrs"
)

// PositionInputMode selects how the user enters their coordinates.
type PositionInputMode int

const (
	InputDecimal PositionInputMode = iota
	InputMGRS
)

// Default ATAK role / team-color values used when the user leaves those
// fields blank. They are display-only — the server does not consult them
// for access control. ATAK's drop-down defaults are "Cyan" and
// "Team Member".
const (
	DefaultRole      = "Team Member"
	DefaultTeamColor = "Cyan"
)

// Sweden bounding box used by the "r" (random Sweden) shortcut. The box is
// generous on the western side — some random points will fall in
// Norwegian/Finnish territory or in water; that is fine for testing.
const (
	swedenMinLat = 55.3
	swedenMaxLat = 69.0
	swedenMinLon = 11.0
	swedenMaxLon = 24.2
)

type positionModel struct {
	inputMode PositionInputMode

	// decimal mode fields
	lat textinput.Model
	lon textinput.Model

	// MGRS mode field
	mgrs textinput.Model

	// shared fields
	hae       textinput.Model
	callsign  textinput.Model
	teamColor textinput.Model
	role      textinput.Model
	interval  textinput.Model

	// randomWalk, when true, makes the publisher pick a fresh random
	// Swedish point every PLI tick. Useful for end-to-end tests where
	// the user wants a moving track without nudging the position by
	// hand. Toggled with Space when focused on the random-walk row.
	randomWalk bool

	focusIdx int
	err      string
}

// position focus layout:
//   0: input mode toggle
//   1..N: visible coord field(s) — 2 in decimal, 1 in MGRS
//   N+1..end: hae, callsign, teamColor, role, interval

func newPositionModel(cfg *config.Config) positionModel {
	mk := func(prompt, value string) textinput.Model {
		ti := textinput.New()
		ti.Prompt = "  " + prompt + ": "
		ti.SetValue(value)
		ti.Width = 40
		return ti
	}
	roleVal := cfg.SelfPos.Role
	if roleVal == "" {
		roleVal = DefaultRole
	}
	teamVal := cfg.SelfPos.Group
	if teamVal == "" {
		teamVal = DefaultTeamColor
	}
	pm := positionModel{
		inputMode:  InputDecimal,
		lat:        mk("latitude        ", floatToStr(cfg.SelfPos.Lat)),
		lon:        mk("longitude       ", floatToStr(cfg.SelfPos.Lon)),
		mgrs:       mk("MGRS grid       ", ""),
		hae:        mk("altitude (m)    ", floatToStr(cfg.SelfPos.HAE)),
		callsign:   mk("callsign        ", cfg.SelfPos.Callsign),
		teamColor:  mk("team color      ", teamVal),
		role:       mk("role            ", roleVal),
		interval:   mk("PLI interval (s)", strconv.Itoa(orDefault(cfg.SelfPos.IntervalSeconds, 30))),
		randomWalk: cfg.SelfPos.RandomWalkSweden,
	}
	pm.focus(0)
	return pm
}

// randomWalkIdx returns the focusable index of the random-walk checkbox
// row — one past the last visible text input field.
func (p *positionModel) randomWalkIdx() int {
	return 1 + len(p.visibleFields())
}

func floatToStr(f float64) string {
	if f == 0 {
		return ""
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func (p *positionModel) coordFields() []*textinput.Model {
	if p.inputMode == InputDecimal {
		return []*textinput.Model{&p.lat, &p.lon}
	}
	return []*textinput.Model{&p.mgrs}
}

func (p *positionModel) sharedFields() []*textinput.Model {
	return []*textinput.Model{&p.hae, &p.callsign, &p.teamColor, &p.role, &p.interval}
}

func (p *positionModel) visibleFields() []*textinput.Model {
	return append(p.coordFields(), p.sharedFields()...)
}

func (p *positionModel) totalFocusable() int {
	// mode toggle (0) + visible fields + random-walk checkbox + exit
	return 1 + len(p.visibleFields()) + 2
}

func (p *positionModel) exitIdx() int {
	return 1 + len(p.visibleFields()) + 1
}

func (p *positionModel) blurAll() {
	for _, ti := range []*textinput.Model{
		&p.lat, &p.lon, &p.mgrs, &p.hae, &p.callsign, &p.teamColor, &p.role, &p.interval,
	} {
		ti.Blur()
	}
}

func (p *positionModel) focus(i int) {
	if i < 0 {
		i = 0
	}
	if i >= p.totalFocusable() {
		i = p.totalFocusable() - 1
	}
	p.focusIdx = i
	p.blurAll()
	// Index 0 is the mode toggle and the last index is the random-walk
	// checkbox — both are non-textinput rows.
	if i >= 1 && i < p.randomWalkIdx() {
		p.visibleFields()[i-1].Focus()
	}
}

func (m Model) updatePosition(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.Type {
		case tea.KeyTab, tea.KeyDown:
			m.positionForm.focus((m.positionForm.focusIdx + 1) % m.positionForm.totalFocusable())
			return m, nil
		case tea.KeyShiftTab, tea.KeyUp:
			m.positionForm.focus((m.positionForm.focusIdx - 1 + m.positionForm.totalFocusable()) % m.positionForm.totalFocusable())
			return m, nil
		case tea.KeyLeft:
			if m.positionForm.focusIdx == 0 {
				m.positionForm.inputMode = InputDecimal
				m.positionForm.focus(0)
				return m, nil
			}
		case tea.KeyRight:
			if m.positionForm.focusIdx == 0 {
				m.positionForm.inputMode = InputMGRS
				m.positionForm.focus(0)
				return m, nil
			}
		case tea.KeyEnter:
			if m.positionForm.focusIdx == 0 {
				// Mode toggle row — Enter just steps into the first field.
				m.positionForm.focus(1)
				return m, nil
			}
			if m.positionForm.focusIdx == m.positionForm.exitIdx() {
				return m, tea.Interrupt
			}
			return m.submitPosition()
		case tea.KeySpace:
			if m.positionForm.focusIdx == m.positionForm.randomWalkIdx() {
				m.positionForm.randomWalk = !m.positionForm.randomWalk
				return m, nil
			}
		case tea.KeyEsc:
			return m, tea.Interrupt
		case tea.KeyF2:
			// F2 fills the active coord field(s) with a random point in
			// Sweden. We do NOT use a plain letter shortcut because it
			// would collide with text typed into callsign / role fields.
			m = m.fillRandomSweden()
			return m, nil
		}
	}

	if m.positionForm.focusIdx >= 1 && m.positionForm.focusIdx < m.positionForm.randomWalkIdx() {
		fields := m.positionForm.visibleFields()
		idx := m.positionForm.focusIdx - 1
		var cmd tea.Cmd
		*fields[idx], cmd = fields[idx].Update(msg)
		return m, cmd
	}
	return m, nil
}

// fillRandomSweden writes a random Swedish point into the currently-active
// coord field(s).
func (m Model) fillRandomSweden() Model {
	lat := swedenMinLat + rand.Float64()*(swedenMaxLat-swedenMinLat)
	lon := swedenMinLon + rand.Float64()*(swedenMaxLon-swedenMinLon)
	if m.positionForm.inputMode == InputDecimal {
		m.positionForm.lat.SetValue(strconv.FormatFloat(lat, 'f', 5, 64))
		m.positionForm.lon.SetValue(strconv.FormatFloat(lon, 'f', 5, 64))
		return m
	}
	if grid, err := mgrs.Format(lat, lon); err == nil {
		m.positionForm.mgrs.SetValue(grid)
	}
	return m
}

func (m Model) submitPosition() (tea.Model, tea.Cmd) {
	pm := &m.positionForm

	var lat, lon float64
	var err error
	if pm.inputMode == InputDecimal {
		lat, err = parseCoord(pm.lat.Value())
		if err != nil {
			pm.err = "latitude must be a decimal number (e.g. 59.33). Got: " + pm.lat.Value()
			return m, nil
		}
		lon, err = parseCoord(pm.lon.Value())
		if err != nil {
			pm.err = "longitude must be a decimal number (e.g. 18.07). Got: " + pm.lon.Value()
			return m, nil
		}
	} else {
		lat, lon, err = mgrs.Parse(pm.mgrs.Value())
		if err != nil {
			pm.err = "MGRS parse failed: " + err.Error()
			return m, nil
		}
	}

	hae := 0.0
	if v := strings.TrimSpace(pm.hae.Value()); v != "" {
		if h, perr := parseCoord(v); perr == nil {
			hae = h
		} else {
			pm.err = "altitude must be a number or empty"
			return m, nil
		}
	}
	callsign := strings.TrimSpace(pm.callsign.Value())
	if callsign == "" {
		pm.err = "callsign is required"
		return m, nil
	}
	interval, err := strconv.Atoi(strings.TrimSpace(pm.interval.Value()))
	if err != nil || interval < 5 || interval > 300 {
		pm.err = "PLI interval must be 5–300 seconds"
		return m, nil
	}

	cfg := m.deps.Config
	cfg.SelfPos.Lat = lat
	cfg.SelfPos.Lon = lon
	cfg.SelfPos.HAE = hae
	cfg.SelfPos.Callsign = callsign
	cfg.SelfPos.Group = strings.TrimSpace(pm.teamColor.Value())
	cfg.SelfPos.Role = strings.TrimSpace(pm.role.Value())
	cfg.SelfPos.IntervalSeconds = interval
	cfg.SelfPos.RandomWalkSweden = pm.randomWalk
	if err := m.deps.ConfigSave(cfg); err != nil {
		pm.err = "save config: " + err.Error()
		return m, nil
	}
	if m.deps.Publisher != nil {
		m.deps.Publisher.SetSelfInfo(cot.SelfInfo{
			UID:              cfg.SelfPos.UID,
			Callsign:         callsign,
			Group:            cfg.SelfPos.Group,
			Role:             cfg.SelfPos.Role,
			Lat:              lat,
			Lon:              lon,
			HAE:              hae,
			RandomWalkSweden: pm.randomWalk,
		})
	}
	pm.err = ""
	if m.deps.Client == nil {
		return m, tea.Quit
	}
	m.mode = ModeMain
	return m, nil
}

// parseCoord accepts both "59.33" and the Swedish keyboard "59,33".
func parseCoord(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", ".")
	return strconv.ParseFloat(s, 64)
}

func (m Model) viewPosition() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("12"))

	pm := &m.positionForm

	// Mode selector row.
	modeRow := func() string {
		decLbl := "( ) Decimal lat/lon"
		mgLbl := "( ) MGRS"
		if pm.inputMode == InputDecimal {
			decLbl = "(•) Decimal lat/lon"
		} else {
			mgLbl = "(•) MGRS"
		}
		row := decLbl + "    " + mgLbl
		if pm.focusIdx == 0 {
			row = cursorStyle.Render("▸ ") + row + "   " + hintStyle.Render("← →")
		} else {
			row = "  " + row
		}
		return row
	}()

	lines := []string{
		titleStyle.Render("TerminalTAK — your position"),
		"",
		hintStyle.Render("  Tab/↑↓ move · Enter saves · Space toggles · F2 random Sweden · Esc / Ctrl+C quit"),
		hintStyle.Render("  ←/→ on the format row toggles between decimal and MGRS."),
		"",
		modeRow,
		"",
	}
	if pm.inputMode == InputDecimal {
		lines = append(lines, pm.lat.View(), pm.lon.View())
	} else {
		lines = append(lines, pm.mgrs.View(),
			hintStyle.Render("  e.g. 33V XK 8200 1500    or    33VXK82001500"))
	}
	lines = append(lines,
		pm.hae.View(),
		pm.callsign.View(),
		pm.teamColor.View(),
		pm.role.View(),
		pm.interval.View(),
	)
	// Random-walk checkbox row.
	rwLabel := "[ ] random walk in Sweden — re-randomize position each PLI tick"
	if pm.randomWalk {
		rwLabel = "[x] random walk in Sweden — re-randomize position each PLI tick"
	}
	if pm.focusIdx == pm.randomWalkIdx() {
		lines = append(lines, cursorStyle.Render("▸ ")+rwLabel+"   "+hintStyle.Render("Space toggles"))
	} else {
		lines = append(lines, "  "+rwLabel)
	}
	exitLabel := "[ Exit ]"
	if pm.focusIdx == pm.exitIdx() {
		exitLabel = cursorStyle.Render("▸ ") + exitLabel
	} else {
		exitLabel = "  " + exitLabel
	}
	lines = append(lines,
		"",
		exitLabel,
		"",
		hintStyle.Render("  team color and role are ATAK display attributes — what other clients"),
		hintStyle.Render("  show next to your callsign. They are NOT access groups; the TAK server"),
		hintStyle.Render("  decides those from your client cert via LDAP. Leave blank for none."),
	)
	if pm.err != "" {
		lines = append(lines, "", errStyle.Render("  "+pm.err))
	}
	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
}
