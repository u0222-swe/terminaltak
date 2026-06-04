package cot

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
)

// MaxEventBytes caps how many bytes a single CoT event may consume from the
// wire. The TAK server is mutually authenticated so this is hardening rather
// than a trust boundary, but a compromised or malicious server could otherwise
// stream one unbounded <event> element and exhaust client memory, since
// xml.Decoder buffers a whole element before DecodeElement returns. Real CoT
// events are a few KB; 1 MiB is comfortably above any legitimate event.
const MaxEventBytes = 1 << 20

// Decode consumes a stream of CoT XML events from r and emits parsed Events
// on the returned channel. The TAK wire format is a sequence of <event/>
// elements concatenated without any wrapping root element; xml.Decoder
// handles this naturally as long as we drive it with Token() / DecodeElement.
//
// On a clean EOF the events channel is closed and no error is sent. On any
// other reader or parse failure the error is sent on errs and both channels
// are then closed. The caller stops the goroutine by closing r.
func Decode(r io.Reader) (<-chan Event, <-chan error) {
	events := make(chan Event, 256)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		capped := &byteCapReader{r: r, limit: MaxEventBytes}
		dec := xml.NewDecoder(capped)
		for {
			tok, err := dec.Token()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				errs <- fmt.Errorf("cot: decode token: %w", err)
				return
			}
			start, ok := tok.(xml.StartElement)
			if !ok || start.Name.Local != "event" {
				continue
			}
			var ev Event
			if err := dec.DecodeElement(&ev, &start); err != nil {
				errs <- fmt.Errorf("cot: decode event: %w", err)
				return
			}
			// A complete event was decoded; reset the per-event byte budget
			// for the next one.
			capped.reset()
			events <- ev
		}
	}()
	return events, errs
}

// byteCapReader fails a read once more than limit bytes have been pulled from
// the underlying reader since the last reset(). Decode resets it after each
// successfully decoded event, bounding the size of any single event. Because
// xml.Decoder reads ahead, the counter may include the leading bytes of the
// next event, making the cap approximate — but it reliably stops a single
// oversized element from consuming unbounded memory.
type byteCapReader struct {
	r     io.Reader
	n     int
	limit int
}

func (c *byteCapReader) Read(p []byte) (int, error) {
	if c.n > c.limit {
		return 0, fmt.Errorf("cot: event exceeds %d bytes", c.limit)
	}
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func (c *byteCapReader) reset() { c.n = 0 }

// Encode marshals a single event to XML and writes it followed by a newline.
// The newline is not required by TAK but eases tcpdump / log inspection.
// Version is filled in if empty so callers can omit it.
func Encode(w io.Writer, ev Event) error {
	if ev.Version == "" {
		ev.Version = Version
	}
	data, err := xml.Marshal(&ev)
	if err != nil {
		return fmt.Errorf("cot: marshal event: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("cot: write event: %w", err)
	}
	if _, err := w.Write([]byte{'\n'}); err != nil {
		return fmt.Errorf("cot: write newline: %w", err)
	}
	return nil
}
