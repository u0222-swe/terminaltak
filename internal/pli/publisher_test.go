package pli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/u0222-swe/terminaltak/internal/cot"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type collector struct {
	mu     sync.Mutex
	events []cot.Event
	fail   error
}

func (c *collector) send(ev cot.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return c.fail
	}
	c.events = append(c.events, ev)
	return nil
}

func (c *collector) snapshot() []cot.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]cot.Event, len(c.events))
	copy(out, c.events)
	return out
}

func TestRunPublishesImmediatelyAndOnTick(t *testing.T) {
	col := &collector{}
	self := cot.SelfInfo{UID: "ME", Callsign: "ALICE", Lat: 1, Lon: 2}
	p := New(self, 50*time.Millisecond, col.send, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	// Wait a few ticks then cancel.
	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)

	got := col.snapshot()
	if len(got) < 3 {
		t.Errorf("expected at least 3 publishes (immediate + 2 ticks + shutdown), got %d", len(got))
	}
	for _, ev := range got {
		if ev.UID != "ME" || ev.Type != "a-f-G-U-C" {
			t.Errorf("bad event: %+v", ev)
		}
	}
}

func TestKickPublishesEarly(t *testing.T) {
	col := &collector{}
	self := cot.SelfInfo{UID: "ME", Callsign: "ALICE"}
	p := New(self, 5*time.Second, col.send, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	// Allow the immediate publish to land.
	time.Sleep(20 * time.Millisecond)
	beforeKick := len(col.snapshot())

	// Kick — should publish well before the 5s tick.
	p.Kick()
	time.Sleep(50 * time.Millisecond)
	afterKick := len(col.snapshot())
	if afterKick <= beforeKick {
		t.Errorf("Kick did not trigger an extra publish: before=%d after=%d", beforeKick, afterKick)
	}
}

func TestSetPositionTriggersPublish(t *testing.T) {
	col := &collector{}
	self := cot.SelfInfo{UID: "ME", Lat: 0, Lon: 0}
	p := New(self, 5*time.Second, col.send, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	time.Sleep(20 * time.Millisecond)

	p.SetPosition(59.33, 18.07, 10)
	time.Sleep(50 * time.Millisecond)

	events := col.snapshot()
	last := events[len(events)-1]
	if last.Point.Lat != 59.33 || last.Point.Lon != 18.07 {
		t.Errorf("last published lat/lon = %v/%v", last.Point.Lat, last.Point.Lon)
	}
}

func TestRunPublishesShutdownEvent(t *testing.T) {
	col := &collector{}
	self := cot.SelfInfo{UID: "ME"}
	p := New(self, 1*time.Second, col.send, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go p.Run(ctx)
	time.Sleep(20 * time.Millisecond) // immediate publish lands

	cancel()
	time.Sleep(20 * time.Millisecond)

	events := col.snapshot()
	if len(events) < 2 {
		t.Fatalf("expected immediate + shutdown publishes, got %d", len(events))
	}
	final := events[len(events)-1]
	timeT, _ := time.Parse(cot.TimeFormat, final.Time)
	staleT, _ := time.Parse(cot.TimeFormat, final.Stale)
	delta := staleT.Sub(timeT)
	if delta > 10*time.Second {
		t.Errorf("shutdown stale window = %v, want ~5s", delta)
	}
}

func TestSendErrorsAreLoggedNotFatal(t *testing.T) {
	col := &collector{fail: errors.New("queue full")}
	self := cot.SelfInfo{UID: "ME"}
	p := New(self, 30*time.Millisecond, col.send, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	time.Sleep(150 * time.Millisecond)
	// Run did not panic and Kick still works after errors:
	p.Kick()
	time.Sleep(20 * time.Millisecond)
}

func TestStaleFor(t *testing.T) {
	cases := []struct {
		in, want time.Duration
	}{
		{30 * time.Second, 75 * time.Second},
		{60 * time.Second, 150 * time.Second},
		{5 * time.Second, 12500 * time.Millisecond},
	}
	for _, c := range cases {
		if got := staleFor(c.in); got != c.want {
			t.Errorf("staleFor(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
