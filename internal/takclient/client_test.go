package takclient

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/u0222-swe/terminaltak/internal/cot"
)

// silentLogger discards log output during tests.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const fixturePLI = `<event version="2.0" uid="ANDROID-test" type="a-f-G-U-C" how="m-g"` +
	` time="2026-04-29T10:00:00.000Z" start="2026-04-29T10:00:00.000Z" stale="2026-04-29T10:01:00.000Z">` +
	`<point lat="59.33" lon="18.07" hae="10" ce="9999999" le="9999999"/>` +
	`<detail><contact callsign="ALICE"/></detail>` +
	`</event>` + "\n"

// pairDialer hands out one in-process connection and lets the test drive the
// "server" side via the matching net.Pipe end. After the first dial it
// returns errClosed so we don't get into infinite reconnect loops in tests.
type pairDialer struct {
	mu     sync.Mutex
	dials  int
	server net.Conn
	closed bool
}

func newPairDialer() (Dialer, *pairDialer) {
	pd := &pairDialer{}
	dialer := func(ctx context.Context) (io.ReadWriteCloser, error) {
		pd.mu.Lock()
		defer pd.mu.Unlock()
		if pd.closed {
			return nil, errors.New("dialer closed")
		}
		pd.dials++
		client, server := net.Pipe()
		pd.server = server
		return client, nil
	}
	return dialer, pd
}

func (p *pairDialer) Server() net.Conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.server
}

func (p *pairDialer) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.server != nil {
		_ = p.server.Close()
	}
}

func TestReceivesEventsFromServer(t *testing.T) {
	dialer, pd := newPairDialer()
	defer pd.close()

	c := New(Config{
		Host:           "fake",
		Port:           8089,
		Dialer:         dialer,
		Logger:         silentLogger(),
		BackoffInitial: 10 * time.Millisecond,
		BackoffMax:     10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan struct{})
	go func() {
		_ = c.Run(ctx)
		close(runDone)
	}()

	// Wait until we're connected before writing — emitStatus is non-blocking
	// so we must consume it.
	waitState(t, c, StateConnected)

	// Push a fixture event from the "server" side.
	go func() {
		_, _ = pd.Server().Write([]byte(fixturePLI))
	}()

	select {
	case ev := <-c.Events():
		if ev.UID != "ANDROID-test" {
			t.Errorf("UID = %q", ev.UID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event")
	}

	cancel()
	<-runDone
}

func TestSendWritesToServer(t *testing.T) {
	dialer, pd := newPairDialer()
	defer pd.close()

	c := New(Config{
		Host:           "fake",
		Port:           8089,
		Dialer:         dialer,
		Logger:         silentLogger(),
		BackoffInitial: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()
	waitState(t, c, StateConnected)

	// Build and send a PLI.
	self := cot.SelfInfo{UID: "ME", Callsign: "ALICE", Lat: 59, Lon: 18}
	ev := cot.BuildPLI(self, 75*time.Second, time.Now())
	if err := c.Send(ev); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Read what the server receives — net.Pipe is unbuffered so we read
	// directly from pd.Server().
	got := make([]byte, 4096)
	_ = pd.Server().SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := pd.Server().Read(got)
	if err != nil {
		t.Fatalf("server read: %v", err)
	}
	body := string(got[:n])
	if !strings.Contains(body, `uid="ME"`) {
		t.Errorf("server didn't see our uid: %s", body)
	}
	if !strings.Contains(body, `<contact callsign="ALICE"`) {
		t.Errorf("server didn't see our callsign: %s", body)
	}
}

func TestSendFullQueueReturnsError(t *testing.T) {
	dialer, _ := newPairDialer()
	c := New(Config{
		Host:           "fake",
		Port:           8089,
		Dialer:         dialer,
		Logger:         silentLogger(),
		OutBufSize:     1,
		BackoffInitial: 10 * time.Millisecond,
	})
	// Don't run — so the writer goroutine isn't draining.
	ev := cot.Event{UID: "x", Type: "a-f-G-U-C"}
	if err := c.Send(ev); err != nil {
		t.Fatalf("first send unexpectedly failed: %v", err)
	}
	if err := c.Send(ev); !errors.Is(err, ErrSendQueueFull) {
		t.Errorf("second send err = %v, want ErrSendQueueFull", err)
	}
}

func TestStatusFlowsConnectingThenConnected(t *testing.T) {
	dialer, pd := newPairDialer()
	defer pd.close()
	c := New(Config{
		Host:           "fake",
		Port:           8089,
		Dialer:         dialer,
		Logger:         silentLogger(),
		BackoffInitial: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	sawConnecting, sawConnected := false, false
	for time.Now().Before(deadline) && !(sawConnecting && sawConnected) {
		select {
		case st := <-c.Status():
			if st.State == StateConnecting {
				sawConnecting = true
			}
			if st.State == StateConnected {
				sawConnected = true
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	if !sawConnecting {
		t.Error("never saw StateConnecting")
	}
	if !sawConnected {
		t.Error("never saw StateConnected")
	}
}

func TestNextBackoff(t *testing.T) {
	cases := []struct {
		cur, max, want time.Duration
	}{
		{0, 30 * time.Second, time.Second},
		{time.Second, 30 * time.Second, 2 * time.Second},
		{16 * time.Second, 30 * time.Second, 30 * time.Second},
		{30 * time.Second, 30 * time.Second, 30 * time.Second},
	}
	for _, c := range cases {
		if got := nextBackoff(c.cur, c.max); got != c.want {
			t.Errorf("nextBackoff(%v,%v) = %v, want %v", c.cur, c.max, got, c.want)
		}
	}
}

// waitState consumes status messages until the desired state is observed or
// a timeout fires.
func waitState(t *testing.T, c *Client, want State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case st := <-c.Status():
			if st.State == want {
				return
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for state %v", want)
}
