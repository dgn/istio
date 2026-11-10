//go:build integ

// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"path"
	"strconv"
	"time"

	"istio.io/istio/pkg/test"
	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/util/file"
)

// Certs is a CA plus a leaf certificate signed by it.
type Certs struct {
	CA   string // PEM CA certificate
	Cert string // PEM leaf certificate
	Key  string // PEM leaf private key
}

// newCerts generates a fresh CA and a leaf certificate for the given DNS names. The leaf is
// usable for both server and client authentication.
func newCerts(t test.Failer, cn string, dnsNames ...string) Certs {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "pcs-ca", Organization: []string{"istio-pcs"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return Certs{
		CA:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
		Cert: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})),
		Key:  string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})),
	}
}

// TLSSecret creates a kubernetes.io/tls Secret (with ca.crt) in the scenario namespace.
func (s *Scenario) TLSSecret(t framework.TestContext, name string, c Certs) {
	t.Helper()
	t.ConfigKube().Eval(s.Namespace(), map[string]any{"name": name, "c": c}, `
apiVersion: v1
kind: Secret
type: kubernetes.io/tls
metadata:
  name: {{.name}}
stringData:
  tls.crt: |
{{ .c.Cert | indent 4 }}
  tls.key: |
{{ .c.Key | indent 4 }}
  ca.crt: |
{{ .c.CA | indent 4 }}
`).ApplyOrFail(t)
}

// CAConfigMap creates a ConfigMap with ca.crt in the given namespace (for BackendTLSPolicy and
// frontend client-certificate validation).
func (s *Scenario) CAConfigMap(t framework.TestContext, ns, name, ca string) {
	t.Helper()
	t.ConfigKube().Eval(ns, map[string]any{"name": name, "ca": ca}, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{.name}}
data:
  ca.crt: |
{{ .ca | indent 4 }}
`).ApplyOrFail(t)
}

// echoTestCA is the CA of the certificate baked into the echo image (SAN BackendTLSHost).
func echoTestCA() string {
	return file.MustAsString(path.Join(env.IstioSrc, "tests/testdata/certs/dns/root-cert.pem"))
}

func itoa(i int) string { return strconv.Itoa(i) }

// CAConfigSecret creates a generic Secret holding only ca.crt (for DestinationRule credentialName
// with SIMPLE TLS) in the scenario namespace.
func (s *Scenario) CAConfigSecret(t framework.TestContext, name, ca string) {
	t.Helper()
	s.CAConfigSecretIn(t, s.Namespace(), name, ca)
}

// CAConfigSecretIn is CAConfigSecret in an explicit namespace. Use the installation namespace for
// gateways that are installed rather than deployed per feature (the egress gateway): Istio serves
// credentialName secrets from the proxy's own namespace.
func (s *Scenario) CAConfigSecretIn(t framework.TestContext, ns, name, ca string) {
	t.Helper()
	t.ConfigKube().Eval(ns, map[string]any{"name": name, "ca": ca}, `
apiVersion: v1
kind: Secret
metadata:
  name: {{.name}}
stringData:
  ca.crt: |
{{ .ca | indent 4 }}
`).ApplyOrFail(t)
}
