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
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"hash/fnv"
	"math/big"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"istio.io/istio/pkg/test"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/util/retry"
)

// Helpers for Istio Gateway server and Gateway API TLS-option features: TLS handshakes probed with
// openssl (for versions, cipher suites, ALPN and certificate selection, which the echo client cannot
// pin), certificates the shared helpers do not issue (RSA, CRLs), files mounted into a gateway, and
// calls from the sidecar-injected client over Istio mTLS.

// rsaLeaf issues an RSA certificate for the DNS names, signed by ca.
func rsaLeaf(t test.Failer, ca *testCA, cn string, dnsNames ...string) Certs {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(ca.next),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	ca.next++
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return Certs{
		CA:   ca.pem,
		Cert: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		Key:  string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
	}
}

// newCRLCA is newCA for a CA that may also sign certificate revocation lists.
func newCRLCA(t test.Failer) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "pcs-crl-ca", Organization: []string{"istio-pcs"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	return &testCA{cert: ca, key: key, pem: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), next: 2}
}

// crl returns a PEM CRL signed by c that revokes the given certificates (c must be from newCRLCA).
func (c *testCA) crl(t test.Failer, revoked ...Certs) string {
	t.Helper()
	var entries []x509.RevocationListEntry
	for _, r := range revoked {
		b, _ := pem.Decode([]byte(r.Cert))
		cert, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, x509.RevocationListEntry{SerialNumber: cert.SerialNumber, RevocationTime: time.Now().Add(-time.Minute)})
	}
	der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number:                    big.NewInt(1),
		ThisUpdate:                time.Now().Add(-time.Hour),
		NextUpdate:                time.Now().Add(24 * time.Hour),
		RevokedCertificateEntries: entries,
	}, c.cert, c.key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: der}))
}

// gatewayFilesDir is where mountGatewayFiles mounts files into the gateway's proxy container.
const gatewayFilesDir = "/etc/pcs-tls"

// mountGatewayFiles creates a Secret with the files and returns the GatewayOptions.Infrastructure
// that mounts it at gatewayFilesDir in the gateway's proxy container, through a parametersRef
// overlay of the generated Deployment. The container is named after the one in the shared gateway
// of the same class. Negative controls drop the infrastructure and the ConfigMap, so the files are
// then missing.
func mountGatewayFiles(t framework.TestContext, s *Scenario, files map[string]string) string {
	t.Helper()
	var container string
	EventuallyKube(t, func() error {
		d, err := s.SharedGateway().Deployment(t)
		if err != nil {
			return err
		}
		container = d.Spec.Template.Spec.Containers[0].Name
		return nil
	})
	s.applyKube(t, s.Namespace(), map[string]any{"name": s.Name("files"), "files": files}, `
apiVersion: v1
kind: Secret
metadata:
  name: {{.name}}
stringData:
{{- range $k, $v := .files }}
  {{$k}}: |
{{ $v | indent 4 }}
{{- end }}
`)
	s.Apply(t, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{.Name}}-files
data:
  deployment: |
    spec:
      template:
        spec:
          volumes:
          - name: pcs-tls
            secret:
              secretName: {{.Secret}}
          containers:
          - name: {{.Container}}
            volumeMounts:
            - name: pcs-tls
              mountPath: {{.Dir}}
              readOnly: true
`, map[string]any{"Secret": s.Name("files"), "Container": container, "Dir": gatewayFilesDir})
	return `
    parametersRef:
      group: ""
      kind: ConfigMap
      name: ` + s.Name() + `-files
`
}

// tlsProbe is a TLS handshake from the uninjected client with openssl s_client (args are added to
// the command line; -brief prints the negotiated version, cipher and peer certificate). An accepted
// probe must complete the handshake and its output must match want; a refused one must fail it.
// pipe, if set, is a command the s_client output is also piped to (its output is matched too).
type tlsProbe struct {
	name     string
	args     string
	accepted bool
	want     string
	pipe     string
}

// expectHandshake runs p against addr:port with SNI sni, retrying until it holds (accepted) or holds
// consistently (refused, like Gateway.ExpectStable).
func (s *Scenario) expectHandshake(t framework.TestContext, addr string, port int, sni string, p tlsProbe) {
	t.Helper()
	pod := s.Env.Client.WorkloadsOrFail(t)[0].PodName()
	script := fmt.Sprintf("out=$(timeout 10 openssl s_client -connect %s -servername %s %s </dev/null 2>&1); rc=$?; echo \"$out\"; ",
		net.JoinHostPort(addr, strconv.Itoa(port)), sni, p.args)
	if p.pipe != "" {
		script += `echo "$out" | ` + p.pipe + "; "
	}
	script += "echo pcs-rc=$rc"
	want := regexp.MustCompile(p.want)
	opts := []retry.Option{retry.Timeout(2 * time.Minute), retry.Delay(time.Second)}
	if !p.accepted {
		opts = []retry.Option{retry.Timeout(time.Minute), retry.Converge(10), retry.Delay(200 * time.Millisecond)}
	}
	retry.UntilSuccessOrFail(t, func() error {
		out, _, err := t.Clusters().Default().PodExecCommands(pod, s.Env.Client.Config().Namespace.Name(), "app", []string{"sh", "-c", script})
		if err != nil {
			return fmt.Errorf("exec openssl: %v", err)
		}
		completed := strings.Contains(out, "pcs-rc=0")
		var cerr error
		switch {
		case p.accepted && !completed:
			cerr = fmt.Errorf("handshake %q failed, want it accepted:\n%s", p.args, out)
		case p.accepted && !want.MatchString(out):
			cerr = fmt.Errorf("handshake %q does not match %q:\n%s", p.args, p.want, out)
		case !p.accepted && completed:
			cerr = fmt.Errorf("handshake %q completed, want it refused:\n%s", p.args, out)
		}
		if cerr != nil {
			s.evidence.mu.Lock()
			s.evidence.lastFailedWith = "tls handshake " + map[bool]string{true: "completed", false: "failed"}[completed]
			s.evidence.mu.Unlock()
		}
		return cerr
	}, opts...)
}

// meshMTLSDestination makes the gateway pod's port reachable from the sidecar-injected client over
// Istio mTLS with the given SNI: a TCP ServiceEntry with a unique virtual IP whose endpoint is the
// gateway pod, and an ISTIO_MUTUAL DestinationRule. It returns the virtual IP to call. Negative
// controls drop the DestinationRule, so the client's sidecar then sends plaintext.
func meshMTLSDestination(t framework.TestContext, s *Scenario, gw *Gateway, name string, port int, sni string) string {
	t.Helper()
	h := fnv.New32a()
	_, _ = h.Write([]byte(s.Name(name)))
	sum := h.Sum32()
	vip := fmt.Sprintf("240.240.%d.%d", (sum>>8)&0xff, sum&0xff)
	s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.SE}}
spec:
  hosts: ["{{.SE}}.pcs.internal"]
  addresses: ["{{.VIP}}"]
  location: MESH_INTERNAL
  resolution: STATIC
  ports:
  - {number: {{.Port}}, name: tcp, protocol: TCP}
  endpoints:
  - address: {{.PodIP}}
---
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.SE}}
spec:
  host: "{{.SE}}.pcs.internal"
  trafficPolicy:
    tls:
      mode: ISTIO_MUTUAL
      sni: "{{.SNI}}"
`, map[string]any{"SE": s.Name(name), "VIP": vip, "Port": port, "PodIP": gw.PodIP(t), "SNI": sni})
	return vip
}

// MeshExpect is Gateway.Expect from the sidecar-injected client.
func (g *Gateway) MeshExpect(t framework.TestContext, r Request, check echo.Checker) echo.CallResult {
	t.Helper()
	return g.s.Env.MeshClient.CallOrFail(t, g.options(r, g.s.observe(check)))
}

// MeshExpectStable is Gateway.ExpectStable from the sidecar-injected client.
func (g *Gateway) MeshExpectStable(t framework.TestContext, r Request, check echo.Checker) echo.CallResult {
	t.Helper()
	o := g.options(r, g.s.observe(check))
	o.Retry.Options = []retry.Option{retry.Timeout(time.Minute), retry.Converge(10), retry.Delay(200 * time.Millisecond)}
	return g.s.Env.MeshClient.CallOrFail(t, o)
}
