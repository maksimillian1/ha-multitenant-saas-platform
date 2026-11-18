package tenantstate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
)

// testCA is a CA and a broker certificate for 127.0.0.1 signed by it.
type testCA struct {
	file   string // the CA certificate as PEM
	server tls.Certificate
}

func newTestCA(t *testing.T) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test cluster CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "broker"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, ca, &srvKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return testCA{file: file, server: tls.Certificate{Certificate: [][]byte{srvDER}, PrivateKey: srvKey}}
}

// tlsSCRAMCluster is a kfake broker that, like usage-kafka, accepts only TLS
// with a SCRAM-SHA-512 login.
func tlsSCRAMCluster(t *testing.T, ca testCA) *kfake.Cluster {
	t.Helper()
	c, err := kfake.NewCluster(
		kfake.NumBrokers(1),
		kfake.TLS(&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{ca.server}}),
		kfake.EnableSASL(),
		kfake.Superuser("SCRAM-SHA-512", "client", "secret"), // gitleaks:allow
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func ping(t *testing.T, c *kfake.Cluster, opts []kgo.Opt) error {
	t.Helper()
	client, err := kgo.NewClient(append(opts, kgo.SeedBrokers(c.ListenAddrs()...))...)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return client.Ping(ctx)
}

// Verifies: kafka-client-auth AC-2
func TestClientAuthOptions(t *testing.T) {
	ca := newTestCA(t)
	cluster := tlsSCRAMCluster(t, ca)

	t.Run("nothing set means no options", func(t *testing.T) {
		opts, err := ClientAuth("", "", "")
		if err != nil || opts != nil {
			t.Fatalf("opts=%v err=%v, want none", opts, err)
		}
	})
	t.Run("config errors", func(t *testing.T) {
		empty := filepath.Join(t.TempDir(), "empty.crt")
		if err := os.WriteFile(empty, []byte("no certificate"), 0o600); err != nil {
			t.Fatal(err)
		}
		for name, args := range map[string][3]string{
			"login without TLS":     {"", "client", "secret"},
			"user without password": {ca.file, "client", ""},
			"missing CA file":       {filepath.Join(t.TempDir(), "missing.crt"), "client", "secret"},
			"CA file without PEM":   {empty, "client", "secret"},
		} {
			if _, err := ClientAuth(args[0], args[1], args[2]); err == nil {
				t.Errorf("%s: want an error", name)
			}
		}
	})
	t.Run("TLS and the right login connect", func(t *testing.T) {
		opts, err := ClientAuth(ca.file, "client", "secret")
		if err != nil {
			t.Fatal(err)
		}
		if err := ping(t, cluster, opts); err != nil {
			t.Fatalf("ping: %v", err)
		}
	})
	t.Run("a wrong password is rejected", func(t *testing.T) {
		opts, err := ClientAuth(ca.file, "client", "wrong")
		if err != nil {
			t.Fatal(err)
		}
		if err := ping(t, cluster, opts); err == nil {
			t.Fatal("ping with a wrong password succeeded")
		}
	})
	t.Run("a broker signed by another CA is rejected", func(t *testing.T) {
		other := newTestCA(t)
		opts, err := ClientAuth(other.file, "client", "secret")
		if err != nil {
			t.Fatal(err)
		}
		if err := ping(t, cluster, opts); err == nil {
			t.Fatal("ping to a broker of another CA succeeded")
		}
	})
}
