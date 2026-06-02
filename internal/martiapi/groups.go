// Package martiapi is a thin client for the small subset of TAK Server's
// /Marti/* REST API that TerminalTAK consumes.
//
// In v1 we only call /Marti/api/groups/all to discover which LDAP-derived
// access groups the authenticated client cert is a member of. The streaming
// CoT wire format does not include this information directly — only the
// per-contact ATAK "team color" (the <__group/> element). Showing the LDAP
// groups in the TUI is the difference between "Cyan 5" (a team-colour
// derived from inbound events) and "testchan_common · testchan_alpha" (the
// authoritative server-side membership).
package martiapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Group is one entry in the /Marti/api/groups/all response. We mirror
// every field so we can replay the exact shape back to the server in
// /groups/active PUTs — earlier attempts that omitted bitpos or direction
// did not apply correctly.
type Group struct {
	Name              string `json:"name"`
	Direction         string `json:"direction"`
	Created           string `json:"created,omitempty"`
	Type              string `json:"type,omitempty"`
	BitPos            int    `json:"bitpos"`
	Active            bool   `json:"active"`
	DistinguishedName string `json:"distinguishedName,omitempty"`
	Description       string `json:"description,omitempty"`
}

// Subscription is one entry in /Marti/api/subscriptions/all — the most
// detailed per-connection view exposed by TAK Server. Crucially, each
// subscription carries a full Groups array (both IN and OUT directions
// with name + bitpos + active state), which is the authoritative UID →
// channels mapping. /Marti/api/contacts/all returns filterGroups=null in
// many deployments and /Marti/api/clientEndPoints has no groups field at
// all, so neither is a substitute.
type Subscription struct {
	ClientUID  string  `json:"clientUid"`
	Callsign   string  `json:"callsign"`
	Username   string  `json:"username"`
	Team       string  `json:"team"`
	Role       string  `json:"role"`
	TakClient  string  `json:"takClient"`
	TakVersion string  `json:"takVersion"`
	Groups     []Group `json:"groups"`
}

// Client is the REST client. Build it with the same TLS config the
// streaming takclient uses so mTLS works out of the box.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// New returns a Client targeting baseURL (e.g. "https://takserver.dev:8443")
// using a fresh http.Client backed by tlsCfg. The caller may pass the same
// *tls.Config the streaming connection uses — its Certificates and
// GetClientCertificate callbacks transfer across.
func New(baseURL string, tlsCfg *tls.Config) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg.Clone()},
		},
	}
}

// Groups calls GET /Marti/api/groups/all?useCache=true and returns the
// parsed group list. With cache enabled in CoreConfig, the default
// (no useCache) returns only ACTIVE groups, which is misleading on a
// fresh client whose cache hasn't been populated yet. useCache=true
// returns the full set with each group's current active flag — that is
// what the channels panel needs to show.
func (c *Client) Groups(ctx context.Context) ([]Group, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/Marti/api/groups/all?useCache=true", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("groups request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("groups: %s: %s", resp.Status, snippet(body))
	}
	// The response shape is {"data": [{...}, ...]}. Some deployments wrap
	// the array directly; tolerate both.
	var wrapped struct {
		Data []Group `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Data) > 0 {
		return wrapped.Data, nil
	}
	var bare []Group
	if err := json.Unmarshal(body, &bare); err == nil && len(bare) > 0 {
		return bare, nil
	}
	return nil, fmt.Errorf("groups: unrecognised response: %s", snippet(body))
}

// Subscriptions calls GET /Marti/api/subscriptions/all and returns the
// directory of currently-connected clients, each with the full list of
// channels (Group objects) they are subscribed to in either direction.
// This is the right endpoint for "what channels does this UID belong to"
// — see Subscription's docstring for why we don't use contacts/all or
// clientEndPoints.
func (c *Client) Subscriptions(ctx context.Context) ([]Subscription, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/Marti/api/subscriptions/all", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("subscriptions request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subscriptions: %s: %s", resp.Status, snippet(body))
	}
	var wrapped struct {
		Data []Subscription `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Data) > 0 {
		return wrapped.Data, nil
	}
	var bare []Subscription
	if err := json.Unmarshal(body, &bare); err == nil {
		return bare, nil
	}
	return nil, nil
}

// SetActiveBits calls PUT /Marti/api/groups/activebits?clientUid={uid}
// with an integer array of bit positions — the user's enabled channels.
// The server reconstructs the full Group[] from the active bits using
// its own group definitions, which avoids the JSON-shape mismatches
// /Marti/api/groups/active is sensitive to.
//
// After this call TAK Server filters both inbound and outbound CoT for
// the user's subscription: disabled channels stop forwarding inbound
// events to us AND stop carrying our own PLI / chat broadcasts into
// recipients in those channels.
func (c *Client) SetActiveBits(ctx context.Context, clientUID string, activeBits []int) error {
	q := url.Values{}
	if clientUID != "" {
		q.Set("clientUid", clientUID)
	}
	endpoint := c.BaseURL + "/Marti/api/groups/activebits"
	if encoded := q.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	body, err := json.Marshal(activeBits)
	if err != nil {
		return fmt.Errorf("marshal active bits: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("setActiveBits request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("setActiveBits: %s: %s", resp.Status, snippet(respBody))
	}
	return nil
}

func snippet(b []byte) string {
	const max = 256
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
