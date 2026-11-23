package netgate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

// interceptor: a TLS server for 127.0.0.1 signed by a throwaway CA, the way
// a corporate proxy or malware's own CA would present Meta's hosts.
func interceptor(t *testing.T) (addr string, roots *x509.CertPool) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Totally Legit Inspection CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "www.facebook.com"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leaf, caCert, &key.PublicKey, caKey)
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: key}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { _ = c.(*tls.Conn).Handshake(); c.Close() }()
		}
	}()
	roots = x509.NewCertPool()
	roots.AddCert(caCert) // as if installed in the Mac's trusted roots
	return l.Addr().String(), roots
}

func TestProbeRootSpotsInterceptor(t *testing.T) {
	addr, roots := interceptor(t)
	host, port, _ := net.SplitHostPort(addr)
	_ = port
	// probeRoot dials host:443; reach the test server through a host:port form
	c, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", addr, &tls.Config{ServerName: host, RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	root, meta, err := probeRootAt(addr, host, roots)
	if err != nil || meta || root != "Totally Legit Inspection CA" {
		t.Fatalf("probe: root %q meta %v err %v", root, meta, err)
	}
}

// TestCertAlarmOncePerHost: a host whose chain isn't Meta's raises one
// alarm per run; Meta's roots and failed probes raise none.
func TestCertAlarmOncePerHost(t *testing.T) {
	g := New()
	g.probe = func(host string) (string, bool, error) {
		switch host {
		case "www.facebook.com":
			return "DigiCert Global Root G2", true, nil
		case "gateway.facebook.com":
			return "Totally Legit Inspection CA", false, nil
		}
		return "", false, net.ErrClosed
	}
	got := make(chan string, 4)
	g.OnCert = func(host, root string) { got <- host + " " + root }
	for _, hp := range []string{"www.facebook.com:443", "gateway.facebook.com:443", "gateway.facebook.com:443", "mmg.whatsapp.net:443"} {
		g.checkCert(hp)
	}
	time.Sleep(100 * time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("%d alarms, want 1", len(got))
	}
	if s := <-got; s != "gateway.facebook.com Totally Legit Inspection CA" {
		t.Fatal(s)
	}
}

func TestMetaRootsAreDigiCertTLS(t *testing.T) {
	if len(metaRoots) != 6 || metaRoots["8bb593a93be1d0e8a822bb887c547890c3e706aad2dab76254f97fb36b82fc26"] != "DigiCert Global Root G2" {
		t.Fatal("Meta's current root (DigiCert Global Root G2) must be pinned")
	}
}
