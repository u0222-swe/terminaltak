// Package takclient maintains a single bidirectional connection to a TAK
// Server's TLS streaming port (8089 by default), forwarding incoming CoT
// events to its caller and serialising outbound CoT writes from arbitrary
// goroutines.
//
// The connection is managed by Run: dial → reader → handle errors →
// exponential backoff → redial. A single writer goroutine drains the
// outbound queue for the lifetime of the Client; if no connection is
// currently active, queued events are dropped (PLI is periodic so this is
// fine, and the chat package surfaces send failures as a per-message
// status). Send is non-blocking and returns ErrSendQueueFull if the buffered
// out channel cannot accept another event immediately.
package takclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/u0222-swe/terminaltak/internal/cot"
)

// State enumerates the connection states surfaced via the Status channel.
type State int

const (
	StateDisconnected State = iota
	StateConnecting
	StateConnected
	StateReconnecting
)

func (s State) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateReconnecting:
		return "reconnecting"
	default:
		return "unknown"
	}
}

// Status is a connection-state notification.
type Status struct {
	State State
	Err   error
	Since time.Time
}

// ErrSendQueueFull is returned by Send when the buffered outbound queue is
// full — typically because the connection is down and the writer goroutine
// is not draining.
var ErrSendQueueFull = errors.New("takclient: send queue full")

// Dialer establishes a TAK Server connection. Real usage uses tlsDial; tests
// inject an in-process pipe.
type Dialer func(ctx context.Context) (io.ReadWriteCloser, error)

// Config configures a Client.
type Config struct {
	Host          string
	Port          int
	TLSConfig     *tls.Config
	Dialer        Dialer
	Logger        *slog.Logger
	OutBufSize    int
	EventsBufSize int
	StatusBufSize int

	// Backoff parameters for redial. Zero values get sane defaults.
	BackoffInitial time.Duration
	BackoffMax     time.Duration
}

// Client is the long-lived TAK streaming connection holder.
type Client struct {
	cfg Config

	events chan cot.Event
	status chan Status
	out    chan cot.Event

	connMu sync.RWMutex
	conn   io.ReadWriteCloser

	writeMu sync.Mutex // serialises writes to conn
	log     *slog.Logger
}

// New constructs a Client. If cfg.Dialer is nil, a default tls.Dial-based
// dialer is built from cfg.Host, cfg.Port, and cfg.TLSConfig.
func New(cfg Config) *Client {
	if cfg.OutBufSize == 0 {
		cfg.OutBufSize = 32
	}
	if cfg.EventsBufSize == 0 {
		cfg.EventsBufSize = 256
	}
	if cfg.StatusBufSize == 0 {
		cfg.StatusBufSize = 8
	}
	if cfg.BackoffInitial == 0 {
		cfg.BackoffInitial = time.Second
	}
	if cfg.BackoffMax == 0 {
		cfg.BackoffMax = 30 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Dialer == nil {
		cfg.Dialer = tlsDial(cfg.Host, cfg.Port, cfg.TLSConfig)
	}
	return &Client{
		cfg:    cfg,
		events: make(chan cot.Event, cfg.EventsBufSize),
		status: make(chan Status, cfg.StatusBufSize),
		out:    make(chan cot.Event, cfg.OutBufSize),
		log:    cfg.Logger,
	}
}

// Events returns the channel of CoT events received from the server. The
// channel is closed when Run returns.
func (c *Client) Events() <-chan cot.Event { return c.events }

// Status returns the channel of connection-state notifications.
func (c *Client) Status() <-chan Status { return c.status }

// Reconnect closes the current connection so Run's loop redials. Used by
// callers that have changed server-side state (active channel bits,
// permissions) and need the new connection to pick up the change — TAK
// Server's per-connection cached group vectors are not refreshed by API
// calls, so a fresh dial is the only way to apply the toggle.
func (c *Client) Reconnect() {
	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()
	if conn != nil {
		c.log.Info("takclient: forcing reconnect")
		_ = conn.Close()
	}
}

// Send queues ev for transmission on the current connection. It is
// non-blocking: if the outbound buffer is full (typically because the
// connection is down), Send returns ErrSendQueueFull and the caller decides
// whether to retry, surface a UI message, or drop.
func (c *Client) Send(ev cot.Event) error {
	select {
	case c.out <- ev:
		return nil
	default:
		return ErrSendQueueFull
	}
}

// Run drives the connection lifecycle until ctx is cancelled. It returns
// ctx.Err() on cancellation; other errors are reported via Status and
// trigger backoff + redial. After Run returns, Events and Status are closed.
func (c *Client) Run(ctx context.Context) error {
	defer close(c.events)
	defer close(c.status)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		c.writeLoop(ctx)
	}()
	defer func() {
		<-writerDone
	}()

	backoff := c.cfg.BackoffInitial
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.emitStatus(StateConnecting, nil)
		conn, err := c.cfg.Dialer(ctx)
		if err != nil {
			c.log.Warn("takclient: dial failed", "err", err, "backoff", backoff)
			c.emitStatus(StateReconnecting, err)
			if !sleepCtx(ctx, backoff) {
				return ctx.Err()
			}
			backoff = nextBackoff(backoff, c.cfg.BackoffMax)
			continue
		}
		backoff = c.cfg.BackoffInitial
		c.setConn(conn)
		c.emitStatus(StateConnected, nil)
		c.log.Info("takclient: connected", "host", c.cfg.Host, "port", c.cfg.Port)

		err = c.handle(ctx, conn)
		c.setConn(nil)
		_ = conn.Close()

		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.log.Warn("takclient: disconnected", "err", err)
		c.emitStatus(StateReconnecting, err)
		if !sleepCtx(ctx, backoff) {
			return ctx.Err()
		}
		backoff = nextBackoff(backoff, c.cfg.BackoffMax)
	}
}

// handle runs the read loop for a single connection. It returns when the
// context is cancelled, the conn closes cleanly (io.EOF), or a parse/IO
// error occurs.
func (c *Client) handle(ctx context.Context, conn io.ReadWriteCloser) error {
	events, errs := cot.Decode(conn)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				// Decoder finished; check for a parse error.
				if err, errsOpen := <-errs; errsOpen && err != nil {
					return err
				}
				return io.EOF
			}
			c.maybeLogChat("recv", ev)
			c.maybeAckProtocol(ev)
			select {
			case c.events <- ev:
			default:
				c.log.Warn("takclient: dropped event - downstream slow", "uid", ev.UID)
			}
		case err, ok := <-errs:
			if ok && err != nil {
				return err
			}
			return io.EOF
		}
	}
}

// maybeAckProtocol responds to TAK Server's t-x-takp-v probe with a
// TakRequest version="0" so the server registers our connection as a
// live, protocol-aware client that prefers XML. Without this ack TAK
// Server forwards broadcast chat but appears to silently drop inbound
// DMs (observed in takserver during manual testing).
func (c *Client) maybeAckProtocol(ev cot.Event) {
	if ev.Type != "t-x-takp-v" {
		return
	}
	ack, err := cot.BuildTakProtocolAck("", 0, time.Now())
	if err != nil {
		c.log.Warn("takclient: build proto ack failed", "err", err)
		return
	}
	if err := c.Send(ack); err != nil {
		c.log.Warn("takclient: proto ack send failed", "err", err)
		return
	}
	c.log.Info("takclient: protocol ack sent", "version", 0)
}

// maybeLogChat dumps the raw XML of any non-position event so we can
// inspect chat, receipts, and routing metadata while debugging. Position
// events (type "a-*") are noisy and skipped to keep the log readable. The
// raw XML cuts off at 2KB per event.
func (c *Client) maybeLogChat(direction string, ev cot.Event) {
	if strings.HasPrefix(ev.Type, "a-") {
		return // skip position events
	}
	var buf bytes.Buffer
	if err := cot.Encode(&buf, ev); err != nil {
		c.log.Warn("takclient: event re-encode failed", "err", err, "dir", direction, "uid", ev.UID)
		return
	}
	xml := buf.String()
	if len(xml) > 2048 {
		xml = xml[:2048] + "…"
	}
	c.log.Info("takclient: cot",
		"dir", direction,
		"type", ev.Type,
		"uid", ev.UID,
		"chatPresent", ev.Detail.Chat != nil,
		"remarksPresent", ev.Detail.Remarks != nil,
		"xml", xml,
	)
}

// writeLoop runs for the lifetime of the Client, draining the outbound
// queue. If no conn is currently set, events are dropped silently (the
// caller already received nil from Send and will see "connecting" /
// "reconnecting" on the Status channel).
func (c *Client) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-c.out:
			c.connMu.RLock()
			conn := c.conn
			c.connMu.RUnlock()
			if conn == nil {
				c.log.Debug("takclient: dropping send - no connection", "uid", ev.UID)
				continue
			}
			c.maybeLogChat("send", ev)
			c.writeMu.Lock()
			err := cot.Encode(conn, ev)
			c.writeMu.Unlock()
			if err != nil {
				c.log.Warn("takclient: send failed", "err", err, "uid", ev.UID)
				// Force the read loop to fail and trigger reconnect by
				// closing the conn we just failed to write to.
				_ = conn.Close()
			}
		}
	}
}

func (c *Client) setConn(conn io.ReadWriteCloser) {
	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()
}

func (c *Client) emitStatus(s State, err error) {
	st := Status{State: s, Err: err, Since: time.Now()}
	select {
	case c.status <- st:
	default:
		// Drop status if nobody's listening — never block.
	}
}

func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		next = max
	}
	if next < time.Second {
		next = time.Second
	}
	return next
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// tlsDial builds a Dialer that performs an mTLS handshake against
// host:port using cfg. It is the default Dialer for production use.
//
// We use tls.Client (rather than tls.Dial) so the caller can swap the
// underlying transport for tests. tls.Client does not infer the SNI / cert
// verification name from the dial address, so we clone cfg and set
// ServerName explicitly. Without this set the handshake fails with
// "either ServerName or InsecureSkipVerify must be specified".
//
// The dialer enables a short TCP keepalive (10s) so that if the network
// path silently dies — VPN drop, NAT timeout, server crash — we notice
// within a few keepalive cycles instead of the kernel default of 2 hours.
// The streaming connection has no application-level heartbeat to fall
// back on.
func tlsDial(host string, port int, cfg *tls.Config) Dialer {
	return func(ctx context.Context) (io.ReadWriteCloser, error) {
		dialer := &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 10 * time.Second,
		}
		raw, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return nil, fmt.Errorf("takclient: tcp dial: %w", err)
		}
		// Belt-and-braces: also call SetKeepAlive in case the system
		// resolved to an IP family where the dialer's hint is ignored.
		if tcp, ok := raw.(*net.TCPConn); ok {
			_ = tcp.SetKeepAlive(true)
			_ = tcp.SetKeepAlivePeriod(10 * time.Second)
		}
		tlsCfg := cfg.Clone()
		if tlsCfg.ServerName == "" {
			tlsCfg.ServerName = host
		}
		conn := tls.Client(raw, tlsCfg)
		if d, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(d)
		}
		if err := conn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, fmt.Errorf("takclient: tls handshake: %w", err)
		}
		_ = conn.SetDeadline(time.Time{})
		return conn, nil
	}
}
