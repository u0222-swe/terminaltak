package cot

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
)

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
		dec := xml.NewDecoder(r)
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
			events <- ev
		}
	}()
	return events, errs
}

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
