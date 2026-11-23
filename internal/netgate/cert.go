package netgate

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"net"
	"sort"
	"strings"
	"time"
)

// metaRoots: the certificate authorities Meta's servers chain to, by the
// SHA-256 of the root's public key (SPKI). All of facebook.com, fbcdn.net,
// messenger.com, fbsbx.com and whatsapp.net chain to DigiCert Global Root
// G2 (checked 2026-11-23); DigiCert's other TLS roots are here in case Meta
// moves between them. A connection the Mac trusts that ends anywhere else
// (a corporate proxy, a CA malware installed) means something sits in
// between: the gate warns (decision D3: warn, don't block).
var metaRoots = map[string]string{
	"aff988906dde12955d9bebbf928fdcc31cce328d5b9384f21c8941ca26e20391": "DigiCert Global Root CA",
	"8bb593a93be1d0e8a822bb887c547890c3e706aad2dab76254f97fb36b82fc26": "DigiCert Global Root G2",
	"b94c198300cec5c057ad0727b70bbe91816992256439a7b32f4598119dda9c97": "DigiCert Global Root G3",
	"5a889647220e54d6bd8a16817224520bb5c78e58984bd570506388b9de0f075f": "DigiCert High Assurance EV Root CA",
	"6a97b51c8219e93e5dec64bad5806cdeb0f8355be47e757010b702456e01aafd": "DigiCert TLS RSA4096 Root G5",
	"a02fafa192c8cb81cb1341554f9c05b71cca2a890b0d1298d683647c961efbdf": "DigiCert TLS ECC P384 Root G5",
}

// probeRoot makes its own TLS connection to host and returns the root its
// certificate chains to (verified against the Mac's trusted roots, the
// same ones the app uses), and whether that root is Meta's.
func probeRoot(host string, roots *x509.CertPool) (root string, meta bool, err error) {
	return probeRootAt(net.JoinHostPort(host, "443"), host, roots)
}

func probeRootAt(addr, host string, roots *x509.CertPool) (root string, meta bool, err error) {
	c, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr,
		&tls.Config{ServerName: host, RootCAs: roots})
	if err != nil {
		return "", false, err
	}
	defer c.Close()
	for _, chain := range c.ConnectionState().VerifiedChains {
		r := chain[len(chain)-1]
		sum := sha256.Sum256(r.RawSubjectPublicKeyInfo)
		if _, ok := metaRoots[hex.EncodeToString(sum[:])]; ok {
			return r.Subject.CommonName, true, nil
		}
		root = r.Subject.CommonName
		if root == "" && len(r.Subject.Organization) > 0 {
			root = strings.Join(r.Subject.Organization, " ")
		}
	}
	return root, false, nil
}

// checkCert probes each host once per run, in the background.
func (g *Gate) checkCert(hostport string) {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	g.mu.Lock()
	if g.probed == nil {
		g.probed = map[string]bool{}
	}
	done := g.probed[host]
	g.probed[host] = true
	g.mu.Unlock()
	if done || g.OnCert == nil {
		return
	}
	go func() {
		root, meta, err := g.probe(host)
		g.mu.Lock()
		switch {
		case err != nil:
			g.certs = append(g.certs, host+" (couldn't check)")
		case meta:
			g.certs = append(g.certs, host+" "+root)
		default:
			g.certs = append(g.certs, host+" NOT META: "+root)
		}
		g.mu.Unlock()
		if err != nil || meta {
			return // unreachable now: the app's own connection fails the same way
		}
		if root == "" {
			root = "an unknown authority"
		}
		g.OnCert(host, root)
	}()
}

// Certs lists each host checked this run and its root ("… NOT META: …").
func (g *Gate) Certs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := append([]string(nil), g.certs...)
	sort.Strings(out)
	return out
}
