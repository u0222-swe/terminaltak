// Package pli publishes the user's own position (Position Location
// Information) to a TAK server on a configurable interval.
//
// The publisher does not own the TLS connection; it calls a send function
// supplied by the caller (typically takclient.Client.Send). If send returns
// an error — most often ErrSendQueueFull during a reconnect — the publisher
// logs and keeps ticking. The next tick will succeed once the connection
// recovers.
package pli

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/u0222-swe/terminaltak/internal/cot"
)

// SendFunc transmits a single CoT event. It must be safe to call from the
// publisher's goroutine.
type SendFunc func(cot.Event) error

// Publisher periodically transmits the user's PLI. It is safe for
// concurrent updates from any goroutine via SetSelfInfo / SetPosition /
// SetInterval — the next publish picks up the new state.
type Publisher struct {
	mu       sync.RWMutex
	self     cot.SelfInfo
	interval time.Duration

	send   SendFunc
	log    *slog.Logger
	notify chan struct{}
}

// New constructs a Publisher with the initial self info and tick interval.
// log may be nil — slog.Default is used when so.
func New(self cot.SelfInfo, interval time.Duration, send SendFunc, log *slog.Logger) *Publisher {
	if log == nil {
		log = slog.Default()
	}
	return &Publisher{
		self:     self,
		interval: interval,
		send:     send,
		log:      log,
		notify:   make(chan struct{}, 1),
	}
}

// SetSelfInfo replaces the published identity in full. The next tick (or an
// explicit Kick) will use the new value.
func (p *Publisher) SetSelfInfo(self cot.SelfInfo) {
	p.mu.Lock()
	p.self = self
	p.mu.Unlock()
	p.Kick()
}

// SetPosition is a convenience wrapper for changing only the lat/lon/hae.
func (p *Publisher) SetPosition(lat, lon, hae float64) {
	p.mu.Lock()
	p.self.Lat = lat
	p.self.Lon = lon
	p.self.HAE = hae
	p.mu.Unlock()
	p.Kick()
}

// SetActiveChannels replaces the set of channels each outgoing PLI is
// addressed to via the <marti><dest group="X"/> mechanism. Empty slice
// means "do not include marti dests" — the server then falls back to
// the user's full LDAP group set.
func (p *Publisher) SetActiveChannels(channels []string) {
	p.mu.Lock()
	p.self.ActiveChannels = append(p.self.ActiveChannels[:0], channels...)
	p.mu.Unlock()
	p.Kick()
}

// SetInterval changes the publish cadence. The current timer is cancelled
// and re-armed at the new interval.
func (p *Publisher) SetInterval(d time.Duration) {
	if d <= 0 {
		return
	}
	p.mu.Lock()
	p.interval = d
	p.mu.Unlock()
	p.Kick()
}

// Kick triggers an immediate publish, replacing the next scheduled tick.
// Safe to call concurrently and from any goroutine.
func (p *Publisher) Kick() {
	select {
	case p.notify <- struct{}{}:
	default:
		// already pending
	}
}

// Run drives the publish loop until ctx is cancelled. On cancellation it
// performs one best-effort "going stale" publish so peers drop our track
// promptly. Returns ctx.Err().
func (p *Publisher) Run(ctx context.Context) error {
	p.publish(false)
	for {
		_, interval := p.snapshot()
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			p.publish(true)
			return ctx.Err()
		case <-timer.C:
			p.publish(false)
		case <-p.notify:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			p.publish(false)
		}
	}
}

// publish builds a fresh PLI event and calls send. shutdown=true sets a
// short stale window so peers drop the track within seconds rather than
// minutes. When RandomWalkSweden is set we also persist the rolled point
// back into p.self so other readers (the map view) see the same position
// the publisher just sent on the wire.
func (p *Publisher) publish(shutdown bool) {
	self, interval := p.snapshot()
	staleAfter := staleFor(interval)
	if shutdown {
		staleAfter = 5 * time.Second
	}
	if self.RandomWalkSweden && !shutdown {
		self.Lat, self.Lon = randomSwedenPoint()
		p.mu.Lock()
		p.self.Lat = self.Lat
		p.self.Lon = self.Lon
		p.mu.Unlock()
	}
	ev := cot.BuildPLI(self, staleAfter, time.Now())
	if err := p.send(ev); err != nil {
		p.log.Warn("pli: send failed", "err", err, "uid", self.UID)
	}
}

// Position returns the latest position the publisher has emitted (or is
// configured to emit on the next tick). When RandomWalkSweden is enabled
// this advances every interval; otherwise it equals whatever was passed
// in via SetSelfInfo / SetPosition.
func (p *Publisher) Position() (lat, lon, hae float64) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.self.Lat, p.self.Lon, p.self.HAE
}

// randomSwedenPoint returns a uniformly random latitude/longitude inside
// Sweden using rejection sampling against a hand-traced simplified
// outline. The polygon is intentionally inset from the actual border so
// rolled points stay clear of Norwegian / Finnish / Baltic territory.
//
// Up to 200 candidates are drawn from the bbox; if none land inside the
// polygon (extremely unlikely — the polygon covers most of the bbox) we
// return the last candidate to guarantee progress.
func randomSwedenPoint() (lat, lon float64) {
	const (
		minLat = 55.3
		maxLat = 69.0
		minLon = 11.0
		maxLon = 24.2
	)
	for i := 0; i < 200; i++ {
		lat = minLat + rand.Float64()*(maxLat-minLat)
		lon = minLon + rand.Float64()*(maxLon-minLon)
		if pointInSweden(lat, lon) {
			return lat, lon
		}
	}
	return lat, lon
}

// swedenPolygon is a simplified outline of Sweden in (lon, lat) order,
// traced from south (Smygehuk) clockwise around the coast. The vertices
// hug the actual border on the western and northern sides (well clear
// of Norway and Finland) but follow the seaward coast on the east — so
// rolled points may land in the Bothnian Bay / Baltic, but never in
// Norway, Finland, the Baltic states, Denmark or Poland.
var swedenPolygon = [...][2]float64{
	// south coast, west → east
	{12.65, 55.45}, // Falsterbo / SW Skåne
	{13.00, 55.38},
	{13.36, 55.34}, // Smygehuk
	{14.20, 55.42},
	{14.75, 55.55},
	// east coast, south → north
	{15.55, 56.10}, // Karlshamn
	{16.20, 56.20},
	{16.65, 56.70}, // east of Kalmar; Öland is included
	{17.10, 57.40},
	{17.20, 58.30},
	{17.55, 58.85},
	{18.30, 59.45}, // E of Stockholm
	{19.00, 60.15},
	{18.70, 61.10},
	{18.10, 62.30},
	{18.50, 63.00},
	{19.50, 63.70}, // E of Umeå (which sits at 20.26 — well inside)
	{20.85, 64.10},
	{21.30, 64.85},
	{22.10, 65.45},
	{23.20, 65.80}, // inland of Haparanda (border on Torne älv ≈ 24.1)
	// north — inland of Finnish border (Torne river, Muonio river)
	{23.65, 66.30},
	{23.55, 67.00},
	{23.20, 67.50},
	{22.85, 68.00},
	{20.50, 68.85}, // Treriksröset corner
	// west — inland of Norwegian border
	{18.30, 68.40},
	{17.40, 67.80},
	{16.30, 67.10},
	{15.40, 66.20},
	{14.30, 65.20},
	{13.50, 64.40},
	{12.40, 63.85}, // Storsjön / Norway border area
	{12.10, 63.00},
	{12.30, 62.20},
	{12.20, 61.40},
	{12.55, 60.50},
	{12.05, 59.50},
	{11.40, 58.90}, // Strömstad
	// west coast, north → south
	{11.55, 58.30},
	{11.70, 57.70}, // Göteborg
	{12.05, 57.20},
	{12.45, 56.65}, // Halmstad
	{12.55, 56.30},
	{12.65, 56.05}, // Helsingborg / Öresund
	{12.80, 55.80},
	{12.65, 55.45}, // close
}

// pointInSweden runs a ray-cast point-in-polygon test against
// swedenPolygon. Crossings are counted on the half-open edge so a point
// exactly on a horizontal edge does not get counted twice.
func pointInSweden(lat, lon float64) bool {
	inside := false
	n := len(swedenPolygon)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		xi, yi := swedenPolygon[i][0], swedenPolygon[i][1]
		xj, yj := swedenPolygon[j][0], swedenPolygon[j][1]
		if (yi > lat) != (yj > lat) {
			xCross := (xj-xi)*(lat-yi)/(yj-yi) + xi
			if lon < xCross {
				inside = !inside
			}
		}
	}
	return inside
}

func (p *Publisher) snapshot() (cot.SelfInfo, time.Duration) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.self, p.interval
}

// staleFor returns 2.5× the publish interval — peers drop the track once
// they miss roughly 2 publishes.
func staleFor(interval time.Duration) time.Duration {
	return interval*2 + interval/2
}
