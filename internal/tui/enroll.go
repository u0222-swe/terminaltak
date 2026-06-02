package tui

import (
	"strconv"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/u0222-swe/terminaltak/internal/config"
)

// EnrollMethod selects between minting a fresh client cert via TAK Server's
// CSR endpoint and importing one the user already has on disk as a .p12.
type EnrollMethod int

const (
	MethodEnrollNew EnrollMethod = iota
	MethodImportP12
)

// EnrollRequest is the combined input the wired-in EnrollFunc receives. It
// carries fields for both methods so a single function pointer can drive
// both flows. Fields irrelevant to the chosen method are ignored.
type EnrollRequest struct {
	Method   EnrollMethod
	Host     string
	Port     int
	Username string
	Password string
	P12Path  string
	P12Pass  string
	Insecure bool
}

// EnrollFunc runs the chosen enrollment/import path and returns nil on
// success after persisting cert.pem / key.pem / ca.pem to disk.
type EnrollFuncV2 = func(EnrollRequest) error

// enrollModel collects the fields needed for either enrollment method. The
// focusIdx walks through the method selector, the method-specific fields,
// and the insecure-skip-verify toggle in order.
type enrollModel struct {
	method EnrollMethod

	// methodEnrollNew fields.
	host     textinput.Model
	port     textinput.Model
	username textinput.Model
	password textinput.Model

	// methodImportP12 fields.
	p12Path textinput.Model
	p12Pass textinput.Model

	insecure bool

	focusIdx int
	lastErr  error
	busy     bool
}

// focus index layout:
//
//	0: method selector (← / →)
//	1..N: visible fields (4 for new, 2 for p12)
//	N+1: insecure toggle
const (
	enrollIdxMethod = 0
)

func (e *enrollModel) fieldCount() int {
	if e.method == MethodEnrollNew {
		return 4
	}
	return 2
}

func (e *enrollModel) totalFocusable() int {
	return 1 + e.fieldCount() + 2 // method + fields + insecure toggle + exit
}

func (e *enrollModel) insecureIdx() int {
	return 1 + e.fieldCount()
}

func (e *enrollModel) exitIdx() int {
	return 1 + e.fieldCount() + 1
}

func newEnrollModel(cfg *config.Config) enrollModel {
	mk := func(prompt, value string, secret bool) textinput.Model {
		ti := textinput.New()
		ti.Prompt = "  " + prompt + ": "
		ti.SetValue(value)
		ti.Width = 40
		if secret {
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '•'
		}
		return ti
	}
	host := mk("server host    ", cfg.Server.Host, false)
	port := mk("enrol port     ", strconv.Itoa(orDefault(cfg.Server.EnrollPort, 8446)), false)
	user := mk("username       ", "", false)
	pass := mk("password       ", "", true)
	p12Path := mk(".p12 path      ", "", false)
	p12Pass := mk(".p12 password  ", "", true)
	host.Focus()
	return enrollModel{
		method:   MethodEnrollNew,
		host:     host,
		port:     port,
		username: user,
		password: pass,
		p12Path:  p12Path,
		p12Pass:  p12Pass,
		insecure: cfg.Server.InsecureSkipVerify,
		focusIdx: enrollIdxMethod,
	}
}

func orDefault(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

func (e *enrollModel) blurAll() {
	for _, ti := range []*textinput.Model{&e.host, &e.port, &e.username, &e.password, &e.p12Path, &e.p12Pass} {
		ti.Blur()
	}
}

// activeFields returns the field pointers visible for the current method,
// in display order (focusIdx 1 = activeFields[0], etc.).
func (e *enrollModel) activeFields() []*textinput.Model {
	if e.method == MethodEnrollNew {
		return []*textinput.Model{&e.host, &e.port, &e.username, &e.password}
	}
	return []*textinput.Model{&e.p12Path, &e.p12Pass}
}

func (e *enrollModel) focus(i int) {
	if i < 0 {
		i = 0
	}
	if i >= e.totalFocusable() {
		i = e.totalFocusable() - 1
	}
	e.focusIdx = i
	e.blurAll()
	if i >= 1 && i <= e.fieldCount() {
		e.activeFields()[i-1].Focus()
	}
}

func (m Model) updateEnroll(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.Type {
		case tea.KeyTab, tea.KeyDown:
			m.enrollForm.focus((m.enrollForm.focusIdx + 1) % m.enrollForm.totalFocusable())
			return m, nil
		case tea.KeyShiftTab, tea.KeyUp:
			m.enrollForm.focus((m.enrollForm.focusIdx - 1 + m.enrollForm.totalFocusable()) % m.enrollForm.totalFocusable())
			return m, nil
		case tea.KeyLeft:
			if m.enrollForm.focusIdx == enrollIdxMethod {
				m.enrollForm.method = MethodEnrollNew
				m.enrollForm.focus(enrollIdxMethod)
				return m, nil
			}
		case tea.KeyRight:
			if m.enrollForm.focusIdx == enrollIdxMethod {
				m.enrollForm.method = MethodImportP12
				m.enrollForm.focus(enrollIdxMethod)
				return m, nil
			}
		case tea.KeySpace:
			if m.enrollForm.focusIdx == m.enrollForm.insecureIdx() {
				m.enrollForm.insecure = !m.enrollForm.insecure
				return m, nil
			}
		case tea.KeyEnter:
			if m.enrollForm.focusIdx == enrollIdxMethod {
				// On the method row, Enter just advances; toggle is via ←/→.
				m.enrollForm.focus(1)
				return m, nil
			}
			if m.enrollForm.focusIdx == m.enrollForm.insecureIdx() {
				m.enrollForm.insecure = !m.enrollForm.insecure
				return m, nil
			}
			if m.enrollForm.focusIdx == m.enrollForm.exitIdx() {
				return m, tea.Interrupt
			}
			return m, m.submitEnroll()
		case tea.KeyEsc:
			return m, tea.Interrupt
		}
	}

	// Forward typing to the focused text input.
	if m.enrollForm.focusIdx >= 1 && m.enrollForm.focusIdx <= m.enrollForm.fieldCount() {
		fields := m.enrollForm.activeFields()
		idx := m.enrollForm.focusIdx - 1
		var cmd tea.Cmd
		*fields[idx], cmd = fields[idx].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) submitEnroll() tea.Cmd {
	req := EnrollRequest{
		Method:   m.enrollForm.method,
		Insecure: m.enrollForm.insecure,
	}
	hostForReturn := ""
	portForReturn := 0
	if req.Method == MethodEnrollNew {
		host := m.enrollForm.host.Value()
		portS := m.enrollForm.port.Value()
		port, err := strconv.Atoi(portS)
		if err != nil {
			return func() tea.Msg {
				return EnrollDoneMsg{Host: host, Err: errInvalidPort(portS)}
			}
		}
		req.Host = host
		req.Port = port
		req.Username = m.enrollForm.username.Value()
		req.Password = m.enrollForm.password.Value()
		hostForReturn = host
		portForReturn = port
	} else {
		req.P12Path = m.enrollForm.p12Path.Value()
		req.P12Pass = m.enrollForm.p12Pass.Value()
	}
	enrollFn := m.deps.Enroll
	return func() tea.Msg {
		if err := enrollFn(req); err != nil {
			return EnrollDoneMsg{Host: hostForReturn, Port: portForReturn, Err: err}
		}
		return EnrollDoneMsg{Host: hostForReturn, Port: portForReturn}
	}
}

// errInvalidPort wraps a parse error with a friendly message.
type errInvalidPort string

func (e errInvalidPort) Error() string { return "invalid port: " + string(e) }

func (m Model) viewEnroll() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("12"))

	// Method selector — radio-style.
	methodRow := func() string {
		newLbl := "( ) Enrol new cert"
		impLbl := "( ) Import .p12"
		if m.enrollForm.method == MethodEnrollNew {
			newLbl = "(•) Enrol new cert"
		} else {
			impLbl = "(•) Import .p12"
		}
		row := newLbl + "    " + impLbl
		if m.enrollForm.focusIdx == enrollIdxMethod {
			row = cursorStyle.Render("▸ ") + row + "   " + hintStyle.Render("← →")
		} else {
			row = "  " + row
		}
		return row
	}()

	insecureLabel := "[ ] skip TLS verify (self-signed CA — applies to enroll AND streaming)"
	if m.enrollForm.insecure {
		insecureLabel = "[x] skip TLS verify (self-signed CA — applies to enroll AND streaming)"
	}
	if m.enrollForm.focusIdx == m.enrollForm.insecureIdx() {
		insecureLabel = cursorStyle.Render("▸ ") + insecureLabel
	} else {
		insecureLabel = "  " + insecureLabel
	}

	exitLabel := "[ Exit ]"
	if m.enrollForm.focusIdx == m.enrollForm.exitIdx() {
		exitLabel = cursorStyle.Render("▸ ") + exitLabel
	} else {
		exitLabel = "  " + exitLabel
	}

	rows := []string{
		titleStyle.Render("TerminalTAK — first-run setup"),
		"",
		hintStyle.Render("  ←/→ choose method · Tab/↑↓ move · Enter submit · Space toggle · Esc / Ctrl+C quit"),
		"",
		methodRow,
		"",
	}
	if m.enrollForm.method == MethodEnrollNew {
		rows = append(rows,
			m.enrollForm.host.View(),
			m.enrollForm.port.View(),
			m.enrollForm.username.View(),
			m.enrollForm.password.View(),
		)
	} else {
		rows = append(rows,
			m.enrollForm.p12Path.View(),
			m.enrollForm.p12Pass.View(),
			hintStyle.Render("  Server host/port for the streaming connection are taken from"),
			hintStyle.Render("  the .p12 cert's CN and config.yaml after the import."),
		)
	}
	rows = append(rows, "", insecureLabel, "", exitLabel, "")

	body := lipgloss.JoinVertical(lipgloss.Left, rows...)
	if m.enrollForm.lastErr != nil {
		body = lipgloss.JoinVertical(lipgloss.Left, body,
			errStyle.Render("  enrolment failed: "+m.enrollForm.lastErr.Error()))
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
}
