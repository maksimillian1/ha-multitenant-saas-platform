package tenantstate

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// ClientAuth is how this binary connects to Kafka (HAMSP-31, feature
// kafka-client-auth): TLS that trusts only the cluster CA in caFile, and a
// SCRAM-SHA-512 login as its own KafkaUser. Without a CA file and a username
// it returns no options, for tests against kfake. SCRAM runs only over TLS,
// so a username without a CA file is an error.
func ClientAuth(caFile, username, password string) ([]kgo.Opt, error) {
	if caFile == "" && username == "" {
		return nil, nil
	}
	if caFile == "" {
		return nil, errors.New("KAFKA_CA_FILE is required with KAFKA_USERNAME: the login runs only over TLS")
	}
	pem, err := os.ReadFile(caFile) //nolint:gosec // G304: the path is our own config, a mounted Secret
	if err != nil {
		return nil, fmt.Errorf("KAFKA_CA_FILE: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("KAFKA_CA_FILE %s holds no PEM certificate", caFile)
	}
	// kgo sets ServerName to the broker it dials, so the broker certificate
	// is checked against its own name.
	opts := []kgo.Opt{kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool})}
	if username == "" {
		return opts, nil
	}
	if password == "" {
		return nil, errors.New("KAFKA_PASSWORD is required when KAFKA_USERNAME is set")
	}
	auth := scram.Auth{User: username, Pass: password}
	return append(opts, kgo.SASL(auth.AsSha512Mechanism())), nil
}
