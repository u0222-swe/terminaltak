package enroll

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"software.sslmate.com/src/go-pkcs12"
)

// ImportP12 reads a PKCS#12 (.p12 / .pfx) bundle and writes its contents
// out as the three PEM files TerminalTak expects in outDir:
//
//	cert.pem (0600) — the leaf client certificate
//	key.pem  (0600) — the matching RSA / EC / Ed25519 private key
//	ca.pem   (0644) — every additional certificate from the bundle, in
//	                  chain order, concatenated as PEM
//
// We try the pure-Go decoder first (software.sslmate.com/src/go-pkcs12).
// It is faster and has no external dependencies but is strict about the
// PKCS#12 shape — it rejects bundles with more than two items in the
// authenticated safe and bundles encoded with BER indefinite-length
// elements. Both shapes are common in the wild (TAK Server's
// makeCert.sh, OpenSSL with default flags, older OpenSSL builds), so we
// fall back to the system `openssl` binary on any decode error. If
// openssl is also unavailable we surface both errors to the user.
func ImportP12(p12Path, password, outDir string) error {
	data, err := os.ReadFile(p12Path)
	if err != nil {
		return fmt.Errorf("read p12: %w", err)
	}

	leafPEM, keyPEM, caPEM, goErr := importP12Pure(data, password)
	if goErr != nil {
		// Fall back to openssl. It handles every PKCS#12 variant we have
		// seen (multi-item safes, BER indefinite-length, legacy ciphers).
		var oerr error
		leafPEM, keyPEM, caPEM, oerr = importP12OpenSSL(p12Path, password)
		if oerr != nil {
			return fmt.Errorf("decode p12: pure-go decoder: %v; openssl: %v", goErr, oerr)
		}
	}

	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return fmt.Errorf("ensure cert dir: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(outDir, "cert.pem"), leafPEM, 0o600); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(outDir, "key.pem"), keyPEM, 0o600); err != nil {
		return err
	}
	// caPEM may be nil for self-contained bundles; that is fine — the TLS
	// handshake then falls back to system roots or InsecureSkipVerify.
	if err := writeFileAtomic(filepath.Join(outDir, "ca.pem"), caPEM, 0o644); err != nil {
		return err
	}
	return nil
}

func importP12Pure(data []byte, password string) (leafPEM, keyPEM, caPEM []byte, err error) {
	priv, leaf, caCerts, err := pkcs12.DecodeChain(data, password)
	if err != nil {
		return nil, nil, nil, err
	}
	if leaf == nil {
		return nil, nil, nil, errors.New("no leaf certificate in p12")
	}
	if priv == nil {
		return nil, nil, nil, errors.New("no private key in p12")
	}
	keyPEM, err = encodePrivateKeyPEM(priv)
	if err != nil {
		return nil, nil, nil, err
	}
	leafPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	for _, c := range caCerts {
		caPEM = append(caPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	return leafPEM, keyPEM, caPEM, nil
}

// importP12OpenSSL invokes the system `openssl` binary to convert the
// bundle to PEM. We run three separate invocations rather than parsing
// the combined output ourselves so each output is unambiguous (one cert
// in clcerts, the chain in cacerts, the key alone with -nocerts -nodes).
//
// We try plain pkcs12 first because:
//   - OpenSSL 1.1.x has no -legacy flag at all and rejects unknown args
//   - OpenSSL 3.x decodes modern p12 bundles fine without -legacy
//
// If the plain invocation fails for any reason, we retry with -legacy.
// That covers the OpenSSL 3.x + old PBE algorithm case (TAK Server's
// makeCert.sh historically used pbeWithSHA1And3-KeyTripleDES-CBC,
// which 3.x relegates to the legacy provider).
func importP12OpenSSL(p12Path, password string) (leafPEM, keyPEM, caPEM []byte, err error) {
	openssl, lerr := exec.LookPath("openssl")
	if lerr != nil {
		return nil, nil, nil, fmt.Errorf("openssl not found in PATH")
	}
	run := func(extraArgs ...string) ([]byte, error) {
		args := append([]string{"pkcs12", "-in", p12Path, "-passin", "pass:" + password}, extraArgs...)
		var stderr bytes.Buffer
		cmd := exec.Command(openssl, args...)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err == nil {
			return out, nil
		}
		// Plain attempt failed. Retry with -legacy; on OpenSSL 1.1.x
		// this will also fail (unknown flag) and we surface the
		// retry's stderr as the real error.
		legacyArgs := append(args, "-legacy")
		var stderr2 bytes.Buffer
		cmd2 := exec.Command(openssl, legacyArgs...)
		cmd2.Stderr = &stderr2
		out2, err2 := cmd2.Output()
		if err2 == nil {
			return out2, nil
		}
		// Both failed. The plain run's stderr is usually the more
		// useful one (e.g. "Mac verify error: invalid password").
		return nil, fmt.Errorf("openssl %v: %v: %s", args, err, stderr.String())
	}

	leaf, err := run("-nokeys", "-clcerts")
	if err != nil {
		return nil, nil, nil, err
	}
	leafPEM = stripBag(leaf)
	if !bytes.Contains(leafPEM, []byte("BEGIN CERTIFICATE")) {
		return nil, nil, nil, errors.New("openssl: no leaf certificate in p12")
	}

	ca, err := run("-nokeys", "-cacerts")
	if err == nil {
		caPEM = stripBag(ca)
	}

	key, err := run("-nocerts", "-nodes")
	if err != nil {
		return nil, nil, nil, err
	}
	keyPEM = stripBag(key)
	if !bytes.Contains(keyPEM, []byte("PRIVATE KEY")) {
		return nil, nil, nil, errors.New("openssl: no private key in p12")
	}
	return leafPEM, keyPEM, caPEM, nil
}

// stripBag drops the human-readable "Bag Attributes" / "subject=" /
// "issuer=" preamble openssl prints before each PEM block. tls.LoadX509KeyPair
// tolerates the noise but it bloats the on-disk PEMs with junk that
// confuses anyone running `openssl x509 -in cert.pem -text` later.
func stripBag(in []byte) []byte {
	var out bytes.Buffer
	rest := in
	for {
		idx := bytes.Index(rest, []byte("-----BEGIN "))
		if idx < 0 {
			break
		}
		rest = rest[idx:]
		end := bytes.Index(rest, []byte("-----END "))
		if end < 0 {
			break
		}
		// Find end of the END line.
		lineEnd := bytes.IndexByte(rest[end:], '\n')
		if lineEnd < 0 {
			out.Write(rest)
			break
		}
		out.Write(rest[:end+lineEnd+1])
		rest = rest[end+lineEnd+1:]
	}
	return out.Bytes()
}

// encodePrivateKeyPEM emits PKCS#8 for every key type. tls.LoadX509KeyPair
// reads PKCS#8 transparently regardless of algorithm, so a single block
// type keeps the downstream code simple.
func encodePrivateKeyPEM(priv interface{}) ([]byte, error) {
	switch priv.(type) {
	case *rsa.PrivateKey, *ecdsa.PrivateKey, ed25519.PrivateKey:
	default:
		return nil, fmt.Errorf("unsupported private key type %T", priv)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}
