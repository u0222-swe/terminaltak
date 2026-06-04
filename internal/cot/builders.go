package cot

import (
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"time"
)

// SelfInfo is the subset of the user's state needed to build outbound CoT
// events. Decoupled from internal/config so the cot package stays
// dependency-free and re-usable.
//
// ActiveChannels holds the user's currently-enabled access channels.
// When non-empty, BuildPLI emits one <marti><dest group="X"/> per name
// — TAK Server then routes the PLI only to subscribers in those
// channels. This is how the channel toggle filters outbound traffic on
// servers that disable the active-group cache.
type SelfInfo struct {
	UID            string
	Callsign       string
	Group          string
	Role           string
	Lat            float64
	Lon            float64
	HAE            float64
	ActiveChannels []string
	// RandomWalkSweden is a flag the PLI publisher reads on every tick.
	// When set, the publisher picks a fresh random Swedish coordinate
	// before each emit. The flag itself is not encoded in the wire CoT
	// — only the resulting Lat/Lon is.
	RandomWalkSweden bool
}

// ChatDest names the recipient of a GeoChat message — either the All-Chat
// broadcast room or a single contact (DM).
type ChatDest interface {
	isChatDest()
	destID() string
	destCallsign() string
	chatroom() string
}

// AllChat is the broadcast destination ("All Chat Rooms").
type AllChat struct{}

func (AllChat) isChatDest()          {}
func (AllChat) destID() string       { return "All Chat Rooms" }
func (AllChat) destCallsign() string { return "" }
func (AllChat) chatroom() string     { return "All Chat Rooms" }

// DM is a direct message to a specific contact, identified by both UID
// (preferred) and callsign (used by the server to locate the recipient).
type DM struct {
	RecipientUID      string
	RecipientCallsign string
}

func (DM) isChatDest()            {}
func (d DM) destID() string       { return d.RecipientUID }
func (d DM) destCallsign() string { return d.RecipientCallsign }
func (d DM) chatroom() string     { return d.RecipientCallsign }

// BuildPLI constructs a position event for this client. staleAfter controls
// how far in the future the stale attribute is set — typically 2.5× the
// publish interval so other clients drop the track promptly when this client
// stops publishing.
func BuildPLI(self SelfInfo, staleAfter time.Duration, now time.Time) Event {
	t := now.UTC()
	stale := t.Add(staleAfter)
	ev := Event{
		Version: Version,
		UID:     self.UID,
		Type:    "a-f-G-U-C",
		How:     "m-g",
		Time:    t.Format(TimeFormat),
		Start:   t.Format(TimeFormat),
		Stale:   stale.Format(TimeFormat),
		Point: Point{
			Lat: self.Lat,
			Lon: self.Lon,
			HAE: self.HAE,
			CE:  9999999.0,
			LE:  9999999.0,
		},
		Detail: Detail{
			Contact: &Contact{
				Callsign: self.Callsign,
				Endpoint: "*:-1:stcp",
			},
			TakV: &TakV{
				// Identify as ATAK-CIV so TAK Server routes DMs to us.
				// During manual testing TAK Server forwarded broadcast
				// chat to a "TerminalTAK" platform but silently dropped
				// inbound DMs — strongly suggests the server keeps a
				// list of "chat-capable" client platforms. Mimicking
				// ATAK-CIV is ugly but works; once we figure out what
				// actually drives the routing decision we can switch
				// back to a truthful identifier.
				Device:   "TerminalTAK",
				Platform: "ATAK-CIV",
				OS:       "linux",
				Version:  "5.4.0.29",
			},
			UID:          &UIDDetail{Droid: self.Callsign},
			PrecisionLoc: &PrecisionLoc{AltSrc: "USER", GeopointSrc: "USER"},
			Status:       &Status{Battery: 100},
			Track:        &Track{Speed: 0, Course: 0},
		},
	}
	if self.Group != "" {
		ev.Detail.Group = &Group{Name: self.Group, Role: self.Role}
	}
	// We tried <marti><dest group="X"/> here to force per-event channel
	// scoping but TAK Server's filter validates each requested group
	// against the connection's currently-set GROUPS_KEY before allowing
	// it as a destination. On takserver (no x509UseGroupCache, LDAP auth)
	// the validation drops the PLI silently, so peers stop seeing us.
	// SelfInfo.ActiveChannels is kept around for a future server with
	// the cache enabled but is no longer baked into the wire format.
	return ev
}

// BuildGeoChat constructs a GeoChat event addressed to dest. The sender's
// position is embedded in <point/> so the message is plotted on map clients
// at the sender's last known location. msgUID is the per-message UUID; if
// empty a fresh UUIDv4 is generated.
//
// The wire shape is the consensus from FreeTAKServer + atak-civ source for
// TAK 5.x — the open question section in the plan flags that field shapes
// may need adjustment after a real-world capture.
func BuildGeoChat(sender SelfInfo, dest ChatDest, text, msgUID string, now time.Time) (Event, error) {
	if msgUID == "" {
		id, err := newUUID()
		if err != nil {
			return Event{}, err
		}
		msgUID = id
	}
	t := now.UTC()
	stale := t.Add(24 * time.Hour) // GeoChat traditionally has a long stale window
	uid := fmt.Sprintf("GeoChat.%s.%s.%s", sender.UID, dest.destID(), msgUID)

	ev := Event{
		Version: Version,
		UID:     uid,
		Type:    "b-t-f",
		How:     "h-g-i-g-o",
		Time:    t.Format(TimeFormat),
		Start:   t.Format(TimeFormat),
		Stale:   stale.Format(TimeFormat),
		Point: Point{
			Lat: sender.Lat,
			Lon: sender.Lon,
			HAE: sender.HAE,
			CE:  9999999.0,
			LE:  9999999.0,
		},
		Detail: Detail{
			Chat: &Chat{
				Parent:         "RootContactGroup",
				GroupOwner:     "false",
				Chatroom:       dest.chatroom(),
				ID:             dest.destID(),
				SenderCallsign: sender.Callsign,
				ChatGrp: &ChatGrp{
					UID0: sender.UID,
					UID1: dest.destID(),
					ID:   dest.destID(),
				},
			},
			Link: &Link{
				UID:      sender.UID,
				Type:     "a-f-G-U-C",
				Relation: "p-p",
			},
			Remarks: &Remarks{
				// Use the BAO.F.ATAK.<uid> source format. ATAK's reply
				// path parses this string to recover the original
				// sender's UID — non-ATAK prefixes can confuse the
				// parser and route the reply to the wrong place.
				Source: "BAO.F.ATAK." + sender.UID,
				To:     dest.destID(),
				Time:   t.Format(TimeFormat),
				Text:   text,
			},
		},
	}
	if dm, ok := dest.(DM); ok && dm.RecipientCallsign != "" {
		ev.Detail.Marti = &Marti{
			Dests: []MartiDest{{Callsign: dm.RecipientCallsign}},
		}
	}
	// We do NOT add <__serverdestination/> here — the previous attempt
	// caused ATAK to surface our DM under a separate "figure-icon"
	// contact entry whose Reply path tried to use our wildcard
	// destination string and dropped on the server. Letting the server
	// add its own __serverdestination on forward keeps BUCKEYE's UI
	// using the PLI-derived contact (which routes correctly via the
	// callsign map).
	return ev, nil
}

// MarkerType maps a point-dropper affiliation to its CoT type string
// (MIL-STD-2525 affiliation letter + the "G" ground battle dimension).
// Unknown / unrecognised affiliations fall back to "a-u-G".
func MarkerType(a Affiliation) string {
	switch a {
	case AffiliationHostile:
		return "a-h-G"
	case AffiliationNeutral:
		return "a-n-G"
	case AffiliationFriendly, AffiliationAssumedFriend:
		return "a-f-G"
	default:
		return "a-u-G"
	}
}

// BuildMarker constructs a user-placed map marker — the equivalent of ATAK's
// "point dropper". cotType is the full CoT type (use MarkerType); uid is the
// stable per-marker identifier, or "" to mint a fresh UUIDv4 (the value used
// is returned so the caller can store it for later edit/delete). label becomes
// the on-map <contact callsign>; remarks an optional free-text note.
// creatorUID links the marker back to this client (peers' "who dropped this"),
// and staleAfter controls how long peers retain the marker before ageing it
// out. The <archive/> detail asks TAK Server to persist the marker so
// late-joining clients still receive it.
func BuildMarker(creatorUID, uid, cotType, label, remarks string, lat, lon, hae float64, staleAfter time.Duration, now time.Time) (Event, string, error) {
	if uid == "" {
		id, err := newUUID()
		if err != nil {
			return Event{}, "", err
		}
		uid = id
	}
	if cotType == "" {
		cotType = "a-u-G"
	}
	t := now.UTC()
	ev := Event{
		Version: Version,
		UID:     uid,
		Type:    cotType,
		How:     "h-g-i-g-o",
		Time:    t.Format(TimeFormat),
		Start:   t.Format(TimeFormat),
		Stale:   t.Add(staleAfter).Format(TimeFormat),
		Point: Point{
			Lat: lat,
			Lon: lon,
			HAE: hae,
			CE:  9999999.0,
			LE:  9999999.0,
		},
		Detail: Detail{
			Contact: &Contact{Callsign: label},
			Link:    &Link{UID: creatorUID, Type: "a-f-G-U-C", Relation: "p-p"},
			Other: []RawElement{
				{XMLName: xml.Name{Local: "archive"}},
			},
		},
	}
	if remarks != "" {
		ev.Detail.Remarks = &Remarks{Text: remarks}
	}
	return ev, uid, nil
}

// BuildMarkerDelete constructs the CoT "delete" task that instructs peers and
// the server to remove the marker identified by targetUID. TAK recognises a
// "t-x-d-d" event carrying a <link> to the target plus a <__forcedelete/>
// detail. targetType should be the deleted marker's CoT type (e.g. "a-h-G").
func BuildMarkerDelete(targetUID, targetType string, now time.Time) (Event, error) {
	id, err := newUUID()
	if err != nil {
		return Event{}, err
	}
	t := now.UTC()
	return Event{
		Version: Version,
		UID:     id,
		Type:    "t-x-d-d",
		How:     "h-g-i-g-o",
		Time:    t.Format(TimeFormat),
		Start:   t.Format(TimeFormat),
		Stale:   t.Add(time.Minute).Format(TimeFormat),
		Point:   Point{CE: 9999999.0, LE: 9999999.0},
		Detail: Detail{
			Link: &Link{UID: targetUID, Type: targetType, Relation: "none"},
			Other: []RawElement{
				{XMLName: xml.Name{Local: "__forcedelete"}},
			},
		},
	}, nil
}

// BuildTakProtocolAck constructs the client-side TakRequest event that
// answers the server's t-x-takp-v announcement. The wire type is
// "t-x-takp-q" (REQUEST) — TAK Server's negotiation listener checks
// against TAK_REQUEST_TYPE, not the announcement type, so sending
// "t-x-takp-v" gets ignored.
//
// version="0" requests "stay on XML" (server replies with status=false
// and re-arms the negotiation listener); version="1" upgrades the link
// to TAK Protocol v1 binary protobuf, which TerminalTAK does not yet
// decode. v0 is what we always want.
//
// Without this acknowledgement TAK Server treats us as a non-negotiated
// connection and silently drops inbound direct messages while still
// forwarding broadcasts (verified by reading
// StreamingProtoBufOrCoTProtocol#negotiationCallback in the server
// source — only TAK_REQUEST_TYPE events trigger processProtocolRequest).
func BuildTakProtocolAck(senderUID string, version int, now time.Time) (Event, error) {
	id, err := newUUID()
	if err != nil {
		return Event{}, err
	}
	t := now.UTC()
	stale := t.Add(20 * time.Second)
	return Event{
		Version: Version,
		UID:     id,
		Type:    "t-x-takp-q",
		How:     "m-g",
		Time:    t.Format(TimeFormat),
		Start:   t.Format(TimeFormat),
		Stale:   stale.Format(TimeFormat),
		Point:   Point{CE: 999999, LE: 999999},
		Detail: Detail{
			Other: []RawElement{
				{
					XMLName: xml.Name{Local: "TakControl"},
					Inner:   fmt.Sprintf(`<TakRequest version="%d"/>`, version),
				},
			},
		},
	}, nil
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
