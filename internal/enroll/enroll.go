// Package enroll implements TAK Server's certificate enrollment protocol:
//
//  1. GET /Marti/api/tls/config — fetches the required Relative Distinguished
//     Names (O, OU, …) the server expects in client CSRs.
//  2. POST /Marti/api/tls/signClient/v2 — submits a PEM-encoded CSR and
//     receives back the signed client certificate plus the CA chain.
//
// Both endpoints live on the dedicated enrollment port (8446 by default) and
// are protected by HTTP Basic Auth backed by either the file-based or the
// LDAP auth backend configured in the server's CoreConfig.xml.
//
// References:
//   - TAK Server Configuration Guide v5.7, Appendix C: Certificate Signing.
//   - src/docker/takserver/templates/CoreConfig.xml.j2 — confirms takserver
//     uses LDAP-backed Basic Auth on 8446 with SHA256WithRSA, validityDays
//     365.
package enroll

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// extKeyUsageOIDClientAuth is the standard "TLS Web Client Authentication"
// extended-key-usage OID. Required for the cert to be accepted as a TLS
// client certificate.
var extKeyUsageOIDClientAuth = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 2}

// extKeyUsageOIDTAKGroupCache is the marker OID TAK Server's X509Authen
// ticator looks for when deciding whether to enable per-user active-group
// caching. It happens to be the X.509 Challenge Password OID, repurposed
// by TAK Server as a feature flag — without it the user's activebits
// PUT (channel toggle) is ignored, and outbound CoT broadcasts to every
// channel the cert is authorised under regardless of toggle state.
//
// See takserver-core/.../groups/X509Authenticator.java line ~198:
//
//	useGroupCache =
//	    user.getCert().getExtendedKeyUsage()
//	        .contains("1.2.840.113549.1.9.7")
//	    || !x509UseGroupCacheRequiresExtKeyUsage;
//
// ATAK builds its CSRs with this OID baked in. We do the same.
var extKeyUsageOIDTAKGroupCache = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 7}

// extOIDExtendedKeyUsage is the OID for the X.509 ExtendedKeyUsage
// extension itself.
var extOIDExtendedKeyUsage = asn1.ObjectIdentifier{2, 5, 29, 37}

// Default endpoint paths. Defined as constants so tests can reference them.
const (
	pathTLSConfig  = "/Marti/api/tls/config"
	pathSignClient = "/Marti/api/tls/signClient/v2"
)

// Enroller holds the parameters needed to enrol a single client certificate
// against a TAK server.
type Enroller struct {
	// BaseURL is the enrolment endpoint root, e.g.
	// "https://takserver.dev.example:8446". Path components are appended.
	BaseURL string

	// Username is the LDAP account that performs Basic Auth and ends up as
	// the CN of the issued cert.
	Username string

	// Password is the LDAP/file-auth password.
	Password string

	// HTTPClient is used for every request. If nil, a default client with
	// InsecureSkipVerify=false and a 30s timeout is used. For takservers's
	// self-signed CA, callers should supply a client built with the right
	// RootCAs or — last resort — InsecureSkipVerify=true.
	HTTPClient *http.Client
}

// NewEnroller returns an Enroller configured against host:port. The optional
// insecureSkipVerify flag bypasses TLS verification — useful when the CA
// chain has not yet been bootstrapped on the local machine.
func NewEnroller(host string, port int, username, password string, insecureSkipVerify bool) *Enroller {
	return &Enroller{
		BaseURL:  fmt.Sprintf("https://%s:%d", host, port),
		Username: username,
		Password: password,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion:         tls.VersionTLS12,
					InsecureSkipVerify: insecureSkipVerify,
				},
			},
		},
	}
}

// NameEntry is a single Relative Distinguished Name the server expects in
// the CSR Subject (e.g. "O" -> "Test Org Name").
type NameEntry struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// TLSConfigResponse mirrors the <certificateConfig> XML returned by GET
// /Marti/api/tls/config. The response sometimes arrives wrapped in a
// <certificateSigning> envelope from the server's CoreConfig.xml — we accept
// either shape by trying both unmarshallers.
type TLSConfigResponse struct {
	XMLName      xml.Name    `xml:"certificateConfig"`
	NameEntries  nameEntries `xml:"nameEntries"`
	ValidityDays int         `xml:"-"` // populated only when wrapped in CoreConfig form
}

type nameEntries struct {
	Entries []NameEntry `xml:"nameEntry"`
}

// EnrollmentResult is the parsed signClient/v2 response — the leaf cert and
// the CA chain that signed it, all in PEM form ready to write to disk.
type EnrollmentResult struct {
	ClientCertPEM []byte
	CAChainPEM    []byte
	KeyPEM        []byte
}

// FetchTLSConfig calls GET /Marti/api/tls/config and parses the resulting
// XML into a TLSConfigResponse. The request carries Basic Auth — the same
// credentials are used later for SubmitCSR.
func (e *Enroller) FetchTLSConfig(ctx context.Context) (*TLSConfigResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.BaseURL+pathTLSConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("build tls/config request: %w", err)
	}
	req.SetBasicAuth(e.Username, e.Password)
	req.Header.Set("Accept", "application/xml")

	resp, err := e.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("tls/config request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read tls/config body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tls/config: %s: %s", resp.Status, snippet(body))
	}

	cfg := &TLSConfigResponse{}
	if err := xml.Unmarshal(body, cfg); err == nil && len(cfg.NameEntries.Entries) > 0 {
		return cfg, nil
	}

	// Try the alternative wrapped shape (full <certificateSigning>...).
	wrapped := &struct {
		XMLName xml.Name          `xml:"certificateSigning"`
		Cfg     TLSConfigResponse `xml:"certificateConfig"`
	}{}
	if err := xml.Unmarshal(body, wrapped); err == nil && len(wrapped.Cfg.NameEntries.Entries) > 0 {
		return &wrapped.Cfg, nil
	}

	return nil, fmt.Errorf("tls/config: unrecognised response: %s", snippet(body))
}

// GenerateCSR creates a fresh RSA-2048 keypair and a corresponding
// PEM-encoded PKCS#10 certificate signing request. The CN is set to
// commonName (must match the HTTP Basic Auth username TAK validates against)
// and the additional RDNs are taken from the server-supplied nameEntries.
func GenerateCSR(commonName string, rdns []NameEntry) (csrPEM, keyPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generate rsa key: %w", err)
	}
	subject := pkix.Name{CommonName: commonName}
	for _, e := range rdns {
		switch strings.ToUpper(e.Name) {
		case "O":
			subject.Organization = append(subject.Organization, e.Value)
		case "OU":
			subject.OrganizationalUnit = append(subject.OrganizationalUnit, e.Value)
		case "C":
			subject.Country = append(subject.Country, e.Value)
		case "ST":
			subject.Province = append(subject.Province, e.Value)
		case "L":
			subject.Locality = append(subject.Locality, e.Value)
		case "STREET":
			subject.StreetAddress = append(subject.StreetAddress, e.Value)
		case "POSTALCODE":
			subject.PostalCode = append(subject.PostalCode, e.Value)
		}
	}
	// Embed the ExtendedKeyUsage extension directly. The server can either
	// pass it through to the issued cert (which is what we want) or strip
	// it; we cannot force the latter. The asn1-marshalled value is a
	// SEQUENCE OF OID — clientAuth plus the TAK group-cache marker.
	ekuValue, err := asn1.Marshal([]asn1.ObjectIdentifier{
		extKeyUsageOIDClientAuth,
		extKeyUsageOIDTAKGroupCache,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal extKeyUsage: %w", err)
	}
	tmpl := &x509.CertificateRequest{
		Subject:            subject,
		SignatureAlgorithm: x509.SHA256WithRSA,
		ExtraExtensions: []pkix.Extension{
			{Id: extOIDExtendedKeyUsage, Value: ekuValue},
		},
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create CSR: %w", err)
	}
	csrPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return csrPEM, keyPEM, nil
}

// SubmitCSR posts a PEM-encoded CSR to /Marti/api/tls/signClient/v2 and
// parses the returned client cert and CA chain. clientUID is the stable
// identifier the server records the cert under — typically the same UUID
// stored in cfg.SelfPos.UID so re-enrollments are idempotent.
//
// We always pass a non-empty `version` query parameter so TAK Server's
// CertManagerService applies addChannelsExtUsage=true and bakes the
// channels-cache OID (1.2.840.113549.1.9.7) into the issued cert.
// Without that flag the server's CA strips the OID even if the CSR
// requests it, and the user's channel toggle has no effect on outbound
// routing — see X509Authenticator.java line ~198 for the server-side
// gating logic.
func (e *Enroller) SubmitCSR(ctx context.Context, csrPEM []byte, clientUID string) (clientCertPEM, caChainPEM []byte, err error) {
	q := url.Values{}
	q.Set("clientUid", clientUID)
	q.Set("version", "1")
	endpoint := e.BaseURL + pathSignClient + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(csrPEM))
	if err != nil {
		return nil, nil, fmt.Errorf("build signClient request: %w", err)
	}
	req.SetBasicAuth(e.Username, e.Password)
	req.Header.Set("Content-Type", "application/pkcs10")
	req.Header.Set("Accept", "application/xml, application/x-pem-file, application/pkcs7-mime")

	resp, err := e.client().Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("signClient request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("read signClient body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("signClient: %s: %s", resp.Status, snippet(body))
	}

	leaf, chain, err := parseEnrollmentResponse(body)
	if err != nil {
		return nil, nil, fmt.Errorf("parse signClient response: %w", err)
	}
	return leaf, chain, nil
}

// parseEnrollmentResponse extracts the signed leaf certificate and CA chain
// from the various shapes a TAK server may return:
//
//  1. concatenated PEM (signed cert first, CA next) — most common for v5.7.
//  2. XML envelope with base64-encoded certs in <signedCert>/<ca0>... tags.
//
// Behaviour against PKCS7 (`application/pkcs7-mime`) is documented as an open
// question in the project plan — if encountered in production we add a
// branch here. For now we surface a clear error.
func parseEnrollmentResponse(body []byte) (leaf, chain []byte, err error) {
	if pemMarker := []byte("-----BEGIN CERTIFICATE-----"); bytes.Contains(body, pemMarker) {
		return splitPEMCertChain(body)
	}
	// Try XML envelope.
	env := &enrollmentEnvelope{}
	if err := xml.Unmarshal(body, env); err == nil && env.SignedCert != "" {
		leafBytes, err := decodeMaybeBase64Cert(env.SignedCert)
		if err != nil {
			return nil, nil, err
		}
		leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafBytes})
		var chainPEM []byte
		for _, c := range env.CACerts {
			caBytes, err := decodeMaybeBase64Cert(c)
			if err != nil {
				return nil, nil, err
			}
			chainPEM = append(chainPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caBytes})...)
		}
		return leafPEM, chainPEM, nil
	}
	return nil, nil, fmt.Errorf("response neither PEM nor known XML envelope: %s", snippet(body))
}

// enrollmentEnvelope matches the XML form used by some TAK builds:
//
//	<enrollment>
//	  <signedCert>BASE64DER</signedCert>
//	  <ca0>BASE64DER</ca0>
//	  <ca1>BASE64DER</ca1>
//	</enrollment>
//
// The number of <ca*> elements varies, so we capture all child elements with
// names starting with "ca".
type enrollmentEnvelope struct {
	XMLName    xml.Name `xml:"enrollment"`
	SignedCert string   `xml:"signedCert"`
	CACerts    []string `xml:",any"`
}

func splitPEMCertChain(body []byte) (leaf, chain []byte, err error) {
	var leafBlock *pem.Block
	var chainBlocks []*pem.Block
	rest := body
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if leafBlock == nil {
			leafBlock = block
		} else {
			chainBlocks = append(chainBlocks, block)
		}
	}
	if leafBlock == nil {
		return nil, nil, errors.New("no CERTIFICATE PEM block found")
	}
	leaf = pem.EncodeToMemory(leafBlock)
	for _, b := range chainBlocks {
		chain = append(chain, pem.EncodeToMemory(b)...)
	}
	return leaf, chain, nil
}

func decodeMaybeBase64Cert(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "-----BEGIN") {
		block, _ := pem.Decode([]byte(s))
		if block == nil {
			return nil, errors.New("malformed PEM in XML envelope")
		}
		return block.Bytes, nil
	}
	der, err := base64Decode(s)
	if err != nil {
		return nil, fmt.Errorf("decode base64 cert: %w", err)
	}
	return der, nil
}

// PromptFunc collects the credentials and target server from the user. The
// TUI implements one variant; tests use a fake. The function returns the
// host, port, username, and password (and any error from the prompt — e.g.
// the user cancelling).
type PromptFunc func(ctx context.Context) (host string, port int, username, password string, insecureSkipVerify bool, err error)

// Run drives the full enrolment flow end-to-end:
//
//  1. prompt the user for server + credentials
//  2. fetch the required CSR Subject RDNs
//  3. generate a fresh RSA-2048 keypair and matching CSR
//  4. submit the CSR and receive the signed cert + CA chain
//  5. write cert.pem (0600), key.pem (0600), ca.pem (0644) to outDir
//
// On success Run returns the host/port the user supplied so the caller can
// persist them into config.yaml; the cert files have already been written.
func (e *Enroller) Run(ctx context.Context, prompt PromptFunc, outDir, clientUID string) (host string, port int, err error) {
	host, port, username, password, insecureSkipVerify, err := prompt(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("prompt: %w", err)
	}
	*e = *NewEnroller(host, port, username, password, insecureSkipVerify)

	cfg, err := e.FetchTLSConfig(ctx)
	if err != nil {
		return host, port, fmt.Errorf("fetch tls config: %w", err)
	}

	csrPEM, keyPEM, err := GenerateCSR(username, cfg.NameEntries.Entries)
	if err != nil {
		return host, port, err
	}

	leafPEM, caPEM, err := e.SubmitCSR(ctx, csrPEM, clientUID)
	if err != nil {
		return host, port, fmt.Errorf("submit csr: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return host, port, fmt.Errorf("ensure cert dir: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(outDir, "cert.pem"), leafPEM, 0o600); err != nil {
		return host, port, err
	}
	if err := writeFileAtomic(filepath.Join(outDir, "key.pem"), keyPEM, 0o600); err != nil {
		return host, port, err
	}
	if err := writeFileAtomic(filepath.Join(outDir, "ca.pem"), caPEM, 0o644); err != nil {
		return host, port, err
	}
	return host, port, nil
}

func (e *Enroller) client() *http.Client {
	if e.HTTPClient != nil {
		return e.HTTPClient
	}
	return http.DefaultClient
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s: %w", path, err)
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

// base64Decode tolerates standard and URL-safe alphabets, with or without
// padding — TAK responses observed in the wild have used both.
func base64Decode(s string) ([]byte, error) {
	var lastErr error
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		out, err := enc.DecodeString(s)
		if err == nil {
			return out, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
