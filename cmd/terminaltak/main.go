// Command terminaltak is a terminal-native TAK client.
//
// Run flow:
//
//  1. Load config (creating a skeleton on first run) and ensure a stable
//     SelfPos UID is generated.
//  2. If no usable client cert exists OR SelfPos lat/lon are still zero,
//     launch the "setup" TUI which collects credentials, runs cert
//     enrollment (or imports a .p12), and prompts for the user's position.
//     The setup TUI exits when both prerequisites are satisfied.
//  3. Build the TLS config from the on-disk cert/key/CA, start the
//     takclient (bidirectional CoT stream) and the PLI publisher, then
//     launch the live TUI which forwards inbound events into the contacts
//     and chat stores and renders the world-map / panes / status bar.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/u0222-swe/terminaltak/internal/branding"
	"github.com/u0222-swe/terminaltak/internal/chat"
	"github.com/u0222-swe/terminaltak/internal/config"
	"github.com/u0222-swe/terminaltak/internal/contacts"
	"github.com/u0222-swe/terminaltak/internal/cot"
	"github.com/u0222-swe/terminaltak/internal/enroll"
	"github.com/u0222-swe/terminaltak/internal/eventlog"
	"github.com/u0222-swe/terminaltak/internal/martiapi"
	"github.com/u0222-swe/terminaltak/internal/pli"
	"github.com/u0222-swe/terminaltak/internal/takclient"
	"github.com/u0222-swe/terminaltak/internal/tui"
)

func main() {
	branding.PrintSplash(os.Stdout, int(os.Stdout.Fd()))
	if err := run(); err != nil {
		if errors.Is(err, tea.ErrInterrupted) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "terminaltak:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		reset    = flag.Bool("reset", false, "wipe ~/.config/terminaltak/ (cert, key, ca, config) and start fresh")
		debugCoT = flag.Bool("debug-cot", false, "append every recv/send CoT event (raw XML) to ~/.config/terminaltak/cot-trace.log")
	)
	flag.Parse()
	if *reset {
		if err := wipeConfigDir(); err != nil {
			return fmt.Errorf("reset: %w", err)
		}
		fmt.Println("terminaltak: config dir wiped — re-run to enrol fresh")
		return nil
	}

	var cotTrace *os.File
	if *debugCoT {
		if err := config.EnsureDir(); err != nil {
			return fmt.Errorf("debug-cot: %w", err)
		}
		path := filepath.Join(config.Dir(), "cot-trace.log")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("debug-cot: open trace file: %w", err)
		}
		defer f.Close()
		cotTrace = f
		fmt.Fprintf(os.Stderr, "terminaltak: tracing CoT to %s\n", path)
	}

	logFile, err := openLogFile()
	if err == nil {
		defer logFile.Close()
		slog.SetDefault(slog.New(slog.NewTextHandler(logFile, nil)))
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if _, err := config.EnsureSelfUID(cfg); err != nil {
		return fmt.Errorf("ensure self uid: %w", err)
	}
	_ = config.Save(cfg)

	// Setup phase: keep running the setup TUI until we have a cert and a
	// valid position. The setup TUI returns tea.Quit after each successful
	// step; this loop re-checks state and either starts the live TUI or
	// re-enters the setup TUI for the next prerequisite.
	for {
		if !config.HasClientCert(cfg) {
			if err := runSetupTUI(cfg, nil); err != nil {
				return err
			}
			// Re-load to pick up any changes the setup wrote.
			cfg, err = config.Load()
			if err != nil {
				return err
			}
			continue
		}
		if cfg.SelfPos.Lat == 0 && cfg.SelfPos.Lon == 0 {
			if err := runSetupTUI(cfg, nil); err != nil {
				return err
			}
			cfg, err = config.Load()
			if err != nil {
				return err
			}
			continue
		}
		break
	}

	// Build TLS config from the on-disk PEMs.
	tlsCfg, leafCert, err := buildTLSConfig(cfg)
	if err != nil {
		return fmt.Errorf("build tls config: %w", err)
	}

	// Start the streaming client + PLI publisher in the background.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client := takclient.New(takclient.Config{
		Host:      cfg.Server.Host,
		Port:      cfg.Server.StreamPort,
		TLSConfig: tlsCfg,
		Logger:    slog.Default(),
		CoTTrace:  cotTrace,
	})
	go func() {
		if err := client.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("takclient: run exited", "err", err)
		}
	}()

	contactStore := contacts.NewStore(cfg.SelfPos.UID)
	chatStore := chat.NewStore(cfg.SelfPos.UID)
	logStore := eventlog.NewStore(eventlog.DefaultCapacity, cfg.SelfPos.UID)

	publisher := pli.New(selfInfoFromConfig(cfg), time.Duration(cfg.SelfPos.IntervalSeconds)*time.Second, client.Send, slog.Default())
	go func() {
		if err := publisher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("pli: run exited", "err", err)
		}
	}()

	// Launch the live TUI.
	apiURL := fmt.Sprintf("https://%s:%d", cfg.Server.Host, 8443)
	api := martiapi.New(apiURL, tlsCfg)

	// channelGroupsRef is updated by the periodic groups poll so the
	// setActive closure always has the latest BitPos / Type / DN values
	// to replay. PUT /Marti/api/groups/active with partial Group shapes
	// (e.g. just Name + Active) silently corrupted state in takserver during
	// earlier testing; sending the full shape — both IN and OUT — has
	// proven necessary.
	var (
		groupRefMu       sync.Mutex
		channelGroupsRef []martiapi.Group
	)
	updateGroupRef := func(gs []martiapi.Group) {
		groupRefMu.Lock()
		channelGroupsRef = append(channelGroupsRef[:0], gs...)
		groupRefMu.Unlock()
	}

	setActive := func(state map[string]bool) error {
		groupRefMu.Lock()
		ref := append([]martiapi.Group(nil), channelGroupsRef...)
		groupRefMu.Unlock()

		// Use /groups/activebits — pass an array of bit positions for
		// channels the user wants enabled. The server reconstructs the
		// full Group[] by intersecting these bits with the channels the
		// user is authorised under, which sidesteps the JSON-shape
		// mismatches that /groups/active is sensitive to.
		seen := map[int]struct{}{}
		bits := make([]int, 0, len(ref))
		for _, g := range ref {
			if !state[g.Name] {
				continue
			}
			if _, dup := seen[g.BitPos]; dup {
				continue
			}
			seen[g.BitPos] = struct{}{}
			bits = append(bits, g.BitPos)
		}
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := api.SetActiveBits(callCtx, cfg.SelfPos.UID, bits); err != nil {
			slog.Warn("martiapi: setActiveBits failed", "err", err, "bits", bits)
			return err
		}
		slog.Info("martiapi: setActiveBits applied", "active_count", countTrue(state), "bits", bits)
		// TAK Server caches bit vectors per connection ID; PUT updates
		// the user's active-group cache but not the connection's
		// inbound/outbound vectors. Force a redial so the new connection
		// picks up the fresh vectors from authenticateCoreUsers.
		client.Reconnect()
		return nil
	}

	deps := tui.Deps{
		Config:            cfg,
		Contacts:          contactStore,
		Chat:              chatStore,
		EventLog:          logStore,
		Publisher:         publisher,
		Client:            client,
		Send:              client.Send,
		Enroll:            makeEnrollFunc(cfg),
		ConfigSave:        config.Save,
		ClientCert:        leafCert,
		SetActiveChannels: setActive,
	}
	model := tui.New(deps)
	prog := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))

	// Start the channels / directory poller. It feeds into the TUI via
	// tea.Program.Send so the channels panel and CoT log overlay reflect
	// real server-side state without the TUI having to know how to dial
	// the API. Best-effort: if /Marti/api isn't reachable on this server
	// the panel just stays empty.
	startMartiPoller(ctx, api, prog, updateGroupRef)

	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("run tui: %w", err)
	}
	return nil
}

// startMartiPoller runs two background pollers — one for the user's
// authorised channels (every 2 minutes), one for the directory of
// connected clients (every 30s). Each tick sends a tea.Msg into the
// running program so the model picks it up via Update.
func startMartiPoller(ctx context.Context, api *martiapi.Client, prog *tea.Program, updateGroupRef func([]martiapi.Group)) {
	// Channels — slower cadence; channel membership rarely changes.
	bootstrapDone := false
	go runPoller(ctx, 2*time.Minute, func() {
		groups, err := api.Groups(ctx)
		gs := make([]tui.ChannelGroup, 0, len(groups))
		anyActive := false
		for _, g := range groups {
			if g.Name == "" {
				continue
			}
			if g.Active {
				anyActive = true
			}
			gs = append(gs, tui.ChannelGroup{
				Name:      g.Name,
				Direction: g.Direction,
				Type:      g.Type,
				BitPos:    g.BitPos,
				Active:    g.Active,
			})
		}
		if err != nil {
			slog.Warn("martiapi: groups poll failed", "err", err)
		} else {
			slog.Info("martiapi: groups poll", "count", len(gs), "anyActive", anyActive)
		}
		// First time we see channels and they are all inactive, the
		// server-side cache has just been bootstrapped from LDAP with
		// X509UseGroupCacheDefaultUpdatesActive=false.
		// Without an explicit setActiveBits the user has zero effective
		// groups and sees nothing. Enable everything once so the user
		// has a working baseline; subsequent toggles are honoured.
		if !bootstrapDone && err == nil && len(gs) > 0 && !anyActive {
			bits := make([]int, 0, len(gs))
			seen := map[int]struct{}{}
			for _, g := range gs {
				if _, dup := seen[g.BitPos]; dup {
					continue
				}
				seen[g.BitPos] = struct{}{}
				bits = append(bits, g.BitPos)
			}
			callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := api.SetActiveBits(callCtx, "", bits)
			cancel()
			if err != nil {
				slog.Warn("martiapi: bootstrap activebits failed", "err", err)
			} else {
				slog.Info("martiapi: bootstrap activebits applied", "bits", bits)
				bootstrapDone = true
			}
		}
		// Stash the raw martiapi.Group list so setActive can replay full
		// shapes without re-fetching.
		raw := make([]martiapi.Group, 0, len(groups))
		for _, g := range groups {
			if g.Name != "" {
				raw = append(raw, g)
			}
		}
		updateGroupRef(raw)
		prog.Send(tui.ChannelsMsg{Groups: gs, Err: err})
	})

	// Directory — UID -> channels mapping. Sourced from /Marti/api/
	// subscriptions/all, the only endpoint that includes the full Groups
	// array per connected client. /Marti/api/contacts/all returns
	// filterGroups=null in takserver and /Marti/api/clientEndPoints lacks a
	// groups field entirely.
	go runPoller(ctx, 30*time.Second, func() {
		subs, err := api.Subscriptions(ctx)
		mapping := make(map[string][]string, len(subs))
		for _, s := range subs {
			if s.ClientUID == "" {
				continue
			}
			seen := make(map[string]struct{}, len(s.Groups))
			channels := make([]string, 0, len(s.Groups))
			for _, g := range s.Groups {
				if g.Name == "" || !g.Active {
					continue
				}
				if _, ok := seen[g.Name]; ok {
					continue
				}
				seen[g.Name] = struct{}{}
				channels = append(channels, g.Name)
			}
			mapping[s.ClientUID] = channels
		}
		if err != nil {
			slog.Warn("martiapi: subscriptions poll failed", "err", err)
		} else {
			slog.Info("martiapi: subscriptions poll", "count", len(mapping), "with_groups", countNonEmpty(mapping))
		}
		prog.Send(tui.DirectoryMsg{UIDToChannels: mapping, Err: err})
	})
}

func countNonEmpty(m map[string][]string) int {
	n := 0
	for _, v := range m {
		if len(v) > 0 {
			n++
		}
	}
	return n
}

func countTrue(m map[string]bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}

// runPoller fires fn immediately, then every interval until ctx is cancelled.
func runPoller(ctx context.Context, interval time.Duration, fn func()) {
	fn()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

// runSetupTUI launches a Bubble Tea program with no streaming Client; the
// model exits via tea.Quit once the user finishes the current setup step
// (enrolment or position editor).
func runSetupTUI(cfg *config.Config, _ *takclient.Client) error {
	var leaf *x509.Certificate
	if config.HasClientCert(cfg) {
		c, err := config.LoadClientCert(cfg)
		if err == nil {
			leaf = c
		}
	}
	deps := tui.Deps{
		Config:     cfg,
		Enroll:     makeEnrollFunc(cfg),
		ConfigSave: config.Save,
		ClientCert: leaf,
	}
	model := tui.New(deps)
	prog := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("setup tui: %w", err)
	}
	return nil
}

// makeEnrollFunc returns the EnrollFunc the TUI invokes when the user
// submits the enroll form. It dispatches between the new-cert and
// import-p12 paths.
func makeEnrollFunc(cfg *config.Config) tui.EnrollFunc {
	return func(req tui.EnrollRequest) error {
		// Persist the connection target up-front so the live TUI knows
		// where to connect even if the user's Insecure flag was just
		// flipped.
		cfg.Server.InsecureSkipVerify = req.Insecure
		if req.Method == tui.MethodEnrollNew {
			cfg.Server.Host = req.Host
			cfg.Server.EnrollPort = req.Port
			if err := config.Save(cfg); err != nil {
				return err
			}
			e := enroll.NewEnroller(req.Host, req.Port, req.Username, req.Password, req.Insecure)
			outDir := config.Dir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tcfg, err := e.FetchTLSConfig(ctx)
			if err != nil {
				return err
			}
			csrPEM, keyPEM, err := enroll.GenerateCSR(req.Username, tcfg.NameEntries.Entries)
			if err != nil {
				return err
			}
			leafPEM, caPEM, err := e.SubmitCSR(ctx, csrPEM, cfg.SelfPos.UID)
			if err != nil {
				return err
			}
			if err := writePEM(outDir, "cert.pem", leafPEM, 0o600); err != nil {
				return err
			}
			if err := writePEM(outDir, "key.pem", keyPEM, 0o600); err != nil {
				return err
			}
			return writePEM(outDir, "ca.pem", caPEM, 0o644)
		}
		// Import .p12 path. The TUI does not collect host/port for this
		// branch — the user should set them in config.yaml after import,
		// or we keep whatever was already there.
		if err := config.Save(cfg); err != nil {
			return err
		}
		return enroll.ImportP12(req.P12Path, req.P12Pass, config.Dir())
	}
}

func writePEM(dir, name string, data []byte, mode os.FileMode) error {
	tmp := filepath.Join(dir, name+".tmp")
	final := filepath.Join(dir, name)
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

func buildTLSConfig(cfg *config.Config) (*tls.Config, *x509.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(cfg.Identity.CertPath, cfg.Identity.KeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load keypair: %w", err)
	}
	leaf, err := config.LoadClientCert(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("parse leaf: %w", err)
	}
	roots := x509.NewCertPool()
	caData, err := os.ReadFile(cfg.Identity.CAPath)
	if err == nil && len(caData) > 0 {
		roots.AppendCertsFromPEM(caData)
	}
	// GetClientCertificate forces our cert to be presented regardless of
	// whether the server's CertificateRequest names a CA we recognise.
	// Without this, Go's TLS stack tries to match the server's acceptable
	// CA list against our cert chain — TAK Server is known to send no
	// CA list (so the match is empty and Go sends an empty Certificate
	// message, which the server then rejects with "certificate required").
	pinned := cert
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &pinned, nil
		},
		RootCAs:            roots,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.Server.InsecureSkipVerify,
	}, leaf, nil
}

func selfInfoFromConfig(cfg *config.Config) cot.SelfInfo {
	return cot.SelfInfo{
		UID:              cfg.SelfPos.UID,
		Callsign:         cfg.SelfPos.Callsign,
		Group:            cfg.SelfPos.Group,
		Role:             cfg.SelfPos.Role,
		Lat:              cfg.SelfPos.Lat,
		Lon:              cfg.SelfPos.Lon,
		HAE:              cfg.SelfPos.HAE,
		RandomWalkSweden: cfg.SelfPos.RandomWalkSweden,
	}
}

// openLogFile opens (or creates) ~/.config/terminaltak/terminaltak.log for
// append. The TUI owns the screen so all logging routes here. Returns an
// error if the directory could not be created or the file could not be
// opened — main run() falls back to slog.Default in that case.
func openLogFile() (*os.File, error) {
	if err := config.EnsureDir(); err != nil {
		return nil, err
	}
	path := filepath.Join(config.Dir(), "terminaltak.log")
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}

// wipeConfigDir removes the entire config directory (cert/key/ca/config/log).
// The next run will create a fresh skeleton and drop into the enrolment TUI.
// We deliberately use os.RemoveAll on Dir() rather than removing individual
// files so any future state we add (cached groups, message history, ...) is
// also cleared by --reset.
func wipeConfigDir() error {
	dir := config.Dir()
	if dir == "" || dir == "/" {
		return fmt.Errorf("refusing to wipe %q", dir)
	}
	return os.RemoveAll(dir)
}
