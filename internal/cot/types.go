// Package cot models, decodes, and builds CoT (Cursor on Target) XML events
// as exchanged with TAK Server over the streaming TLS port.
//
// Only the subset of CoT used by TerminalTak is modelled: position events
// (PLI / "a-f-G-U-C") and GeoChat events ("b-t-f"). Unknown detail elements
// are preserved as raw XML so they survive a decode → encode round-trip.
package cot

import "encoding/xml"

// TimeFormat is the ISO-8601 layout TAK clients use for the time/start/stale
// attributes. Always UTC, always millisecond precision.
const TimeFormat = "2006-01-02T15:04:05.000Z"

// Version is the CoT envelope version every modern TAK client emits.
const Version = "2.0"

// Event is a single CoT message.
type Event struct {
	XMLName xml.Name `xml:"event"`
	Version string   `xml:"version,attr"`
	UID     string   `xml:"uid,attr"`
	Type    string   `xml:"type,attr"`
	How     string   `xml:"how,attr,omitempty"`
	Time    string   `xml:"time,attr"`
	Start   string   `xml:"start,attr"`
	Stale   string   `xml:"stale,attr"`

	// Optional access control attributes used by some TAK 5+ deployments.
	Access string `xml:"access,attr,omitempty"`
	Qos    string `xml:"qos,attr,omitempty"`
	Opex   string `xml:"opex,attr,omitempty"`

	Point  Point  `xml:"point"`
	Detail Detail `xml:"detail"`
}

// Point holds the geospatial coordinates of an event.
type Point struct {
	Lat float64 `xml:"lat,attr"`
	Lon float64 `xml:"lon,attr"`
	HAE float64 `xml:"hae,attr"`
	CE  float64 `xml:"ce,attr"`
	LE  float64 `xml:"le,attr"`
}

// Detail carries the typed sub-elements TerminalTak understands. Anything
// outside this whitelist lands in Other so it round-trips intact.
type Detail struct {
	Contact      *Contact      `xml:"contact,omitempty"`
	TakV         *TakV         `xml:"takv,omitempty"`
	Track        *Track        `xml:"track,omitempty"`
	Group        *Group        `xml:"__group,omitempty"`
	PrecisionLoc *PrecisionLoc `xml:"precisionlocation,omitempty"`
	Status       *Status       `xml:"status,omitempty"`
	UID          *UIDDetail    `xml:"uid,omitempty"`
	Chat         *Chat         `xml:"__chat,omitempty"`
	Link         *Link         `xml:"link,omitempty"`
	Remarks      *Remarks      `xml:"remarks,omitempty"`
	Marti        *Marti        `xml:"marti,omitempty"`
	ServerDest   *ServerDest   `xml:"__serverdestination,omitempty"`

	// Other captures everything we did not explicitly model so that
	// re-encoding does not drop server-added detail fields.
	Other []RawElement `xml:",any"`
}

// RawElement is an opaque XML element captured verbatim. We keep it as the
// raw inner XML so we don't need to model every TAK detail variant.
type RawElement struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Inner   string     `xml:",innerxml"`
}

// MarshalXML re-emits the raw element using its original name and inner
// payload so unknown details survive a round-trip.
func (r RawElement) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	start.Name = r.XMLName
	start.Attr = r.Attrs
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	if r.Inner != "" {
		if err := e.EncodeToken(xml.CharData(r.Inner)); err != nil {
			return err
		}
	}
	return e.EncodeToken(start.End())
}

// Contact identifies the device sending this event.
type Contact struct {
	Callsign string `xml:"callsign,attr,omitempty"`
	Endpoint string `xml:"endpoint,attr,omitempty"`
	Phone    string `xml:"phone,attr,omitempty"`
}

// TakV identifies the TAK client implementation.
type TakV struct {
	Device   string `xml:"device,attr,omitempty"`
	Platform string `xml:"platform,attr,omitempty"`
	OS       string `xml:"os,attr,omitempty"`
	Version  string `xml:"version,attr,omitempty"`
}

// Track is the contact's velocity vector.
type Track struct {
	Speed  float64 `xml:"speed,attr"`
	Course float64 `xml:"course,attr"`
}

// Group is the user's TAK group/role assignment ("__group" — two underscores
// in the wire format).
type Group struct {
	Name string `xml:"name,attr,omitempty"`
	Role string `xml:"role,attr,omitempty"`
}

// PrecisionLoc describes how the point was sourced.
type PrecisionLoc struct {
	AltSrc      string `xml:"altsrc,attr,omitempty"`
	GeopointSrc string `xml:"geopointsrc,attr,omitempty"`
}

// Status is mostly battery telemetry.
type Status struct {
	Battery int `xml:"battery,attr,omitempty"`
}

// UIDDetail carries Android/iOS UID labels — TAK clients put their callsign
// in the Droid attribute.
type UIDDetail struct {
	Droid string `xml:"Droid,attr,omitempty"`
}

// Chat is the GeoChat envelope ("__chat" — two underscores).
type Chat struct {
	Parent         string    `xml:"parent,attr,omitempty"`
	GroupOwner     string    `xml:"groupOwner,attr,omitempty"`
	Chatroom       string    `xml:"chatroom,attr,omitempty"`
	ID             string    `xml:"id,attr,omitempty"`
	SenderCallsign string    `xml:"senderCallsign,attr,omitempty"`
	ChatGrp        *ChatGrp  `xml:"chatgrp,omitempty"`
}

// ChatGrp identifies the chatroom participants.
type ChatGrp struct {
	UID0 string `xml:"uid0,attr,omitempty"`
	UID1 string `xml:"uid1,attr,omitempty"`
	ID   string `xml:"id,attr,omitempty"`
}

// Link relates the event to another contact (typically the chat sender).
type Link struct {
	UID      string `xml:"uid,attr,omitempty"`
	Type     string `xml:"type,attr,omitempty"`
	Relation string `xml:"relation,attr,omitempty"`
}

// Remarks holds the human-readable chat message text.
type Remarks struct {
	Source string `xml:"source,attr,omitempty"`
	To     string `xml:"to,attr,omitempty"`
	Time   string `xml:"time,attr,omitempty"`
	Text   string `xml:",chardata"`
}

// Marti carries server-side routing hints, primarily used to address chat to
// a specific callsign.
type Marti struct {
	Dests []MartiDest `xml:"dest"`
}

// MartiDest targets a single recipient by callsign, UID, channel group,
// or mission. TAK Server's StreamingEndpointRewriteFilter recognises the
// "group" attribute and rewrites the message's source-group set to just
// the named groups, which lets a client send a PLI to a specific channel
// even when server-side group caching is disabled (the route would
// otherwise default to every LDAP group the user is in).
type MartiDest struct {
	Callsign string `xml:"callsign,attr,omitempty"`
	UID      string `xml:"uid,attr,omitempty"`
	Mission  string `xml:"mission,attr,omitempty"`
	Group    string `xml:"group,attr,omitempty"`
}

// ServerDest records which servers the message was delivered through.
type ServerDest struct {
	Destinations string `xml:"destinations,attr,omitempty"`
}

// Affiliation is the friend/foe classification carried in the second segment
// of the CoT type string (e.g. "a-f-G-U-C" → Friendly).
type Affiliation int

const (
	AffiliationUnknown Affiliation = iota
	AffiliationFriendly
	AffiliationHostile
	AffiliationNeutral
	AffiliationPending
	AffiliationSuspect
	AffiliationAssumedFriend
	AffiliationJoker
	AffiliationFaker
	AffiliationNone
)

// Affiliation parses Event.Type and returns the friend/foe classification.
func (e *Event) Affiliation() Affiliation {
	if len(e.Type) < 3 || e.Type[0] != 'a' || e.Type[1] != '-' {
		return AffiliationUnknown
	}
	switch e.Type[2] {
	case 'f':
		return AffiliationFriendly
	case 'h':
		return AffiliationHostile
	case 'n':
		return AffiliationNeutral
	case 'p':
		return AffiliationPending
	case 's':
		return AffiliationSuspect
	case 'a':
		return AffiliationAssumedFriend
	case 'j':
		return AffiliationJoker
	case 'k':
		return AffiliationFaker
	case 'o':
		return AffiliationNone
	default:
		return AffiliationUnknown
	}
}

// Domain is the operating environment carried in the third segment of an
// "a-*-X" type string.
type Domain int

const (
	DomainUnknown Domain = iota
	DomainGround
	DomainAir
	DomainSea
	DomainSubsurface
	DomainSpace
)

// Domain parses Event.Type for the operating environment.
func (e *Event) Domain() Domain {
	if len(e.Type) < 5 || e.Type[0] != 'a' || e.Type[3] != '-' {
		return DomainUnknown
	}
	switch e.Type[4] {
	case 'G':
		return DomainGround
	case 'A':
		return DomainAir
	case 'S':
		return DomainSea
	case 'U':
		return DomainSubsurface
	case 'P':
		return DomainSpace
	default:
		return DomainUnknown
	}
}

// IsGeoChat reports whether the event is a GeoChat message.
func (e *Event) IsGeoChat() bool {
	return e.Type == "b-t-f" && e.Detail.Chat != nil
}

// IsPosition reports whether the event carries an "atom" (i.e. a contact
// position rather than a chat or marker).
func (e *Event) IsPosition() bool {
	return len(e.Type) >= 1 && e.Type[0] == 'a'
}
