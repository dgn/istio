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
	"fmt"
	"net/http"
	"strings"
	"time"

	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/util/retry"
)

// Istio Gateway servers.tls, as a family: each feature is one HTTPS server setting (gatewayTLS) and
// the TLS clients that must be accepted or refused. The Istio Gateway is scaffolding for the negative
// controls (it also binds the route), so these features are self-validating instead: every refusal
// is paired with an accepted client against the same server.
func init() {
	Register(
		Feature{
			ID:          "istioapi.gateway.tls-mutual",
			Description: "Istio Gateway HTTPS server with tls.mode MUTUAL requires a client certificate signed by the CA in the credential (ca.crt).",
			Roles:       ingressRole,
			Covers:      []string{"Gateway.servers.tls.mode", "Gateway.servers.tls.mode=MUTUAL"},
			Run: gatewayTLS(`
      mode: MUTUAL`, func(k gatewayTLSKeys) []tlsClientCase {
				return []tlsClientCase{{"client-cert", k.Client, true}, {"no-client-cert", nil, false}, {"untrusted-client-cert", k.Untrusted, false}}
			}),
		},
		Feature{
			ID:          "istioapi.gateway.tls-optional-mutual",
			Description: "Istio Gateway HTTPS server with tls.mode OPTIONAL_MUTUAL accepts clients without a certificate but verifies one that is presented.",
			Roles:       ingressRole,
			Covers:      []string{"Gateway.servers.tls.mode=OPTIONAL_MUTUAL"},
			Run: gatewayTLS(`
      mode: OPTIONAL_MUTUAL`, func(k gatewayTLSKeys) []tlsClientCase {
				return []tlsClientCase{{"no-client-cert", nil, true}, {"client-cert", k.Client, true}, {"untrusted-client-cert", k.Untrusted, false}}
			}),
		},
		Feature{
			ID:          "istioapi.gateway.tls-subject-alt-names",
			Description: "Istio Gateway MUTUAL server with tls.subjectAltNames accepts only client certificates with a listed SAN.",
			Roles:       ingressRole,
			Covers:      []string{"Gateway.servers.tls.subjectAltNames"},
			Run: gatewayTLS(`
      mode: MUTUAL
      subjectAltNames: [client.example.com]`, func(k gatewayTLSKeys) []tlsClientCase {
				return []tlsClientCase{{"listed-san", k.Client, true}, {"other-san", k.Other, false}}
			}),
		},
		Feature{
			ID:          "istioapi.gateway.tls-verify-certificate-hash",
			Description: "Istio Gateway MUTUAL server with tls.verifyCertificateHash accepts only the client certificate with that SHA-256 fingerprint.",
			Roles:       ingressRole,
			Covers:      []string{"Gateway.servers.tls.verifyCertificateHash"},
			Run: gatewayTLS(`
      mode: MUTUAL
      verifyCertificateHash: ["{{.ClientHash}}"]`, func(k gatewayTLSKeys) []tlsClientCase {
				return []tlsClientCase{{"pinned-cert", k.Client, true}, {"other-cert", k.Other, false}}
			}),
		},
		Feature{
			ID:          "istioapi.gateway.tls-verify-certificate-spki",
			Description: "Istio Gateway MUTUAL server with tls.verifyCertificateSpki accepts only the client certificate with that public key.",
			Roles:       ingressRole,
			Covers:      []string{"Gateway.servers.tls.verifyCertificateSpki"},
			Run: gatewayTLS(`
      mode: MUTUAL
      verifyCertificateSpki: ["{{.ClientSpki}}"]`, func(k gatewayTLSKeys) []tlsClientCase {
				return []tlsClientCase{{"pinned-key", k.Client, true}, {"other-key", k.Other, false}}
			}),
		},
		Feature{
			ID:          "istioapi.gateway.tls-protocol-version",
			Description: "Istio Gateway server tls.minProtocolVersion and maxProtocolVersion TLSV1_2 refuse a client that requires TLS 1.3 and accept TLS 1.2.",
			Roles:       ingressRole,
			Covers: []string{
				"Gateway.servers.tls.minProtocolVersion", "Gateway.servers.tls.minProtocolVersion=TLSV1_2", "Gateway.servers.tls.maxProtocolVersion",
				"Gateway.servers.tls.maxProtocolVersion=TLSV1_2",
			},
			Run: gatewayTLS(`
      mode: SIMPLE
      minProtocolVersion: TLSV1_2
      maxProtocolVersion: TLSV1_2`, func(k gatewayTLSKeys) []tlsClientCase {
				return []tlsClientCase{{"tls12", nil, true}, {"tls13-only", func(o *echo.CallOptions) { o.TLS.MinVersion = "1.3" }, false}}
			}),
		},
		Feature{
			ID:          "istioapi.gateway.tls-ecdh-curves",
			Description: "Istio Gateway server tls.ecdhCurves restricts key exchange to the listed curves.",
			Roles:       ingressRole,
			Covers:      []string{"Gateway.servers.tls.ecdhCurves"},
			Run: gatewayTLS(`
      mode: SIMPLE
      ecdhCurves: [P-256]`, func(k gatewayTLSKeys) []tlsClientCase {
				return []tlsClientCase{
					{"listed-curve", func(o *echo.CallOptions) { o.TLS.CurvePreferences = []string{"P-256"} }, true},
					{"other-curve", func(o *echo.CallOptions) { o.TLS.CurvePreferences = []string{"X25519"} }, false},
				}
			}),
		},
		Feature{
			ID: "istioapi.gateway.tls-insecure-skip-verify",
			Description: "Istio Gateway MUTUAL server with tls.insecureSkipVerify still requires a client certificate " +
				"but accepts one that fails verification.",
			Roles:  ingressRole,
			Covers: []string{"Gateway.servers.tls.insecureSkipVerify"},
			Run: gatewayTLS(`
      mode: MUTUAL
      insecureSkipVerify: true`, func(k gatewayTLSKeys) []tlsClientCase {
				return []tlsClientCase{{"client-cert", k.Client, true}, {"untrusted-client-cert", k.Untrusted, true}, {"no-client-cert", nil, false}}
			}),
		},
		Feature{
			ID:          "istioapi.gateway.tls-cipher-suites",
			Description: "Istio Gateway server tls.cipherSuites restricts TLS 1.2 handshakes to the listed cipher suites.",
			Roles:       ingressRole,
			Covers:      []string{"Gateway.servers.tls.cipherSuites"},
			Run: gatewayServerTLS(gatewayTLSSpec{tls: `
      mode: SIMPLE
      credentialName: {{.Cred}}
      maxProtocolVersion: TLSV1_2
      cipherSuites: [ECDHE-ECDSA-AES256-GCM-SHA384]`, probes: []tlsProbe{
				{name: "listed-cipher", args: "-brief -tls1_2 -cipher ECDHE-ECDSA-AES256-GCM-SHA384", accepted: true, want: "Ciphersuite: ECDHE-ECDSA-AES256-GCM-SHA384"},
				{name: "other-cipher", args: "-brief -tls1_2 -cipher ECDHE-ECDSA-AES128-GCM-SHA256"},
			}}),
		},
		Feature{
			ID: "istioapi.gateway.tls-alpn-protocols",
			Description: "Istio Gateway server tls.alpnProtocols replaces the advertised ALPN protocols: " +
				"with [http/1.1], a client offering h2 and http/1.1 negotiates http/1.1, and one offering only h2 negotiates none.",
			Roles:  ingressRole,
			Covers: []string{"Gateway.servers.tls.alpnProtocols"},
			Run: gatewayServerTLS(gatewayTLSSpec{tls: `
      mode: SIMPLE
      credentialName: {{.Cred}}
      alpnProtocols: [http/1.1]`, probes: []tlsProbe{
				{name: "offers-h2-and-http1", args: "-alpn h2,http/1.1", accepted: true, want: "ALPN protocol: http/1.1"},
				{name: "offers-only-h2", args: "-alpn h2", accepted: true, want: "No ALPN negotiated"},
			}}),
		},
		Feature{
			ID:          "istioapi.gateway.tls-protocol-version-tls13",
			Description: "Istio Gateway server tls.minProtocolVersion and maxProtocolVersion TLSV1_3 accept TLS 1.3 and refuse TLS 1.2.",
			Roles:       ingressRole,
			Covers:      []string{"Gateway.servers.tls.minProtocolVersion=TLSV1_3", "Gateway.servers.tls.maxProtocolVersion=TLSV1_3"},
			Run: gatewayServerTLS(gatewayTLSSpec{tls: `
      mode: SIMPLE
      credentialName: {{.Cred}}
      minProtocolVersion: TLSV1_3
      maxProtocolVersion: TLSV1_3`, probes: []tlsProbe{
				{name: "tls13", args: "-brief -tls1_3", accepted: true, want: "Protocol version: TLSv1.3"},
				{name: "tls12", args: "-brief -tls1_2"},
			}}),
		},
		Feature{
			ID:          "istioapi.gateway.tls-protocol-version-auto",
			Description: "Istio Gateway server tls.minProtocolVersion and maxProtocolVersion TLS_AUTO accept TLS 1.2 and TLS 1.3.",
			Roles:       ingressRole,
			Covers:      []string{"Gateway.servers.tls.minProtocolVersion=TLS_AUTO", "Gateway.servers.tls.maxProtocolVersion=TLS_AUTO"},
			Run: gatewayServerTLS(gatewayTLSSpec{tls: `
      mode: SIMPLE
      credentialName: {{.Cred}}
      minProtocolVersion: TLS_AUTO
      maxProtocolVersion: TLS_AUTO`, probes: []tlsProbe{
				{name: "tls12", args: "-brief -tls1_2", accepted: true, want: "Protocol version: TLSv1.2"},
				{name: "tls13", args: "-brief -tls1_3", accepted: true, want: "Protocol version: TLSv1.3"},
			}}),
		},
		Feature{
			ID: "istioapi.gateway.tls-protocol-version-tls10",
			Description: "Istio Gateway server tls.minProtocolVersion and maxProtocolVersion TLSV1_0, with a TLS 1.0 cipher suite, " +
				"accept TLS 1.0 and refuse TLS 1.1 and 1.2.",
			Roles:  ingressRole,
			Covers: []string{"Gateway.servers.tls.minProtocolVersion=TLSV1_0", "Gateway.servers.tls.maxProtocolVersion=TLSV1_0"},
			Run:    legacyTLSVersion("TLSV1_0", "1.0", "-tls1", "TLSv1\n", "-tls1_1"),
		},
		Feature{
			ID: "istioapi.gateway.tls-protocol-version-tls11",
			Description: "Istio Gateway server tls.minProtocolVersion and maxProtocolVersion TLSV1_1, with a TLS 1.1 cipher suite, " +
				"accept TLS 1.1 and refuse TLS 1.0 and 1.2.",
			Roles:  ingressRole,
			Covers: []string{"Gateway.servers.tls.minProtocolVersion=TLSV1_1", "Gateway.servers.tls.maxProtocolVersion=TLSV1_1"},
			Run:    legacyTLSVersion("TLSV1_1", "1.1", "-tls1_1", "TLSv1.1\n", "-tls1"),
		},
		Feature{
			ID: "istioapi.gateway.tls-credential-names",
			Description: "Istio Gateway server tls.credentialNames with an RSA and an ECDSA credential presents the certificate " +
				"matching the client's key exchange.",
			Roles:  ingressRole,
			Covers: []string{"Gateway.servers.tls.credentialNames"},
			Run: gatewayServerTLS(gatewayTLSSpec{tls: `
      mode: SIMPLE
      credentialNames: [{{.Cred}}, {{.RSACred}}]`, probes: serverCertProbes}),
		},
		Feature{
			ID: "istioapi.gateway.tls-file-certificates",
			Description: "Istio Gateway MUTUAL server with serverCertificate, privateKey and caCertificates read from files mounted " +
				"in the gateway requires a client certificate signed by that CA.",
			Roles:  ingressRole,
			Covers: []string{"Gateway.servers.tls.serverCertificate", "Gateway.servers.tls.privateKey", "Gateway.servers.tls.caCertificates"},
			Run: gatewayServerTLS(gatewayTLSSpec{mount: true, tls: `
      mode: MUTUAL
      serverCertificate: {{.Dir}}/tls.crt
      privateKey: {{.Dir}}/tls.key
      caCertificates: {{.Dir}}/ca.crt`, clients: func(k gatewayTLSKeys) []tlsClientCase {
				return []tlsClientCase{{"client-cert", k.Client, true}, {"no-client-cert", nil, false}, {"untrusted-client-cert", k.Untrusted, false}}
			}}),
		},
		Feature{
			ID:          "istioapi.gateway.tls-ca-crl",
			Description: "Istio Gateway MUTUAL server with file certificates and tls.caCrl refuses client certificates revoked by that CRL.",
			Roles:       ingressRole,
			Covers:      []string{"Gateway.servers.tls.caCrl"},
			Run: gatewayServerTLS(gatewayTLSSpec{mount: true, tls: `
      mode: MUTUAL
      serverCertificate: {{.Dir}}/tls.crt
      privateKey: {{.Dir}}/tls.key
      caCertificates: {{.Dir}}/ca.crt
      caCrl: {{.Dir}}/ca.crl`, clients: func(k gatewayTLSKeys) []tlsClientCase {
				// The CRL revokes the Other certificate.
				return []tlsClientCase{{"client-cert", k.Client, true}, {"revoked-client-cert", k.Other, false}}
			}}),
		},
		Feature{
			ID: "istioapi.gateway.tls-certificates",
			Description: "Istio Gateway server tls.tlsCertificates with an RSA and an ECDSA certificate from mounted files presents " +
				"the certificate matching the client's key exchange.",
			Roles: ingressRole,
			Covers: []string{
				"Gateway.servers.tls.tlsCertificates", "Gateway.servers.tls.tlsCertificates.serverCertificate", "Gateway.servers.tls.tlsCertificates.privateKey",
			},
			Run: gatewayServerTLS(gatewayTLSSpec{mount: true, tls: `
      mode: SIMPLE
      tlsCertificates:
      - {serverCertificate: {{.Dir}}/rsa.crt, privateKey: {{.Dir}}/rsa.key}
      - {serverCertificate: {{.Dir}}/tls.crt, privateKey: {{.Dir}}/tls.key}`, probes: serverCertProbes}),
		},
	)
}

// serverCertProbes check which of gatewayServerTLS's server certificates is presented: forcing a
// TLS 1.2 RSA or ECDSA key exchange selects the RSA or the ECDSA certificate.
var serverCertProbes = []tlsProbe{
	{name: "rsa-key-exchange", args: "-brief -tls1_2 -cipher ECDHE-RSA-AES128-GCM-SHA256", accepted: true, want: `Peer certificate: CN ?= ?rsa-server`},
	{name: "ecdsa-key-exchange", args: "-brief -tls1_2 -cipher ECDHE-ECDSA-AES128-GCM-SHA256", accepted: true, want: `Peer certificate: CN ?= ?ecdsa-server`},
}

// legacyTLSVersion is a feature pinning a server to TLS 1.0 or 1.1 (version), which needs a cipher
// suite those versions support. The echo client (minimum clientMin) must reach v1, and openssl must
// negotiate the version with accept (output line want) and fail with refuse and TLS 1.2. OpenSSL
// only offers these versions and SHA-1 suites at security level 0.
func legacyTLSVersion(version, clientMin, accept, want, refuse string) func(t framework.TestContext, s *Scenario) {
	const cipher = "-cipher ECDHE-ECDSA-AES128-SHA:@SECLEVEL=0"
	return gatewayServerTLS(gatewayTLSSpec{
		tls: `
      mode: SIMPLE
      credentialName: {{.Cred}}
      minProtocolVersion: ` + version + `
      maxProtocolVersion: ` + version + `
      cipherSuites: [ECDHE-ECDSA-AES128-SHA]`,
		clients: func(gatewayTLSKeys) []tlsClientCase {
			return []tlsClientCase{
				{"legacy-client", func(o *echo.CallOptions) { o.TLS.MinVersion = clientMin }, true},
				{"tls12-client", nil, false},
			}
		},
		probes: []tlsProbe{
			{name: "accepted-version", args: "-brief " + accept + " " + cipher, accepted: true, want: "Protocol version: " + want},
			{name: "other-version", args: "-brief " + refuse + " " + cipher},
			{name: "tls12", args: "-brief -tls1_2"},
		},
	})
}

// gatewayTLSKeys are the client credentials a gatewayTLS feature can present: Client (SAN
// client.example.com) and Other (SAN other.example.com) are signed by the CA the server trusts,
// Untrusted by another CA. Each is a CallOptions modifier adding the certificate.
type gatewayTLSKeys struct {
	Client, Other, Untrusted func(*echo.CallOptions)
}

// tlsClientCase is one client of a gatewayTLS feature: opts modify the TLS client (nil: no client
// certificate, defaults), accepted is whether the request must succeed.
type tlsClientCase struct {
	name     string
	opts     func(*echo.CallOptions)
	accepted bool
}

// gatewayTLS is the body of the Istio Gateway servers.tls family: an HTTPS server on a dedicated
// gateway with credentialName and the tls settings (YAML indented for servers[].tls; templated with
// .ClientHash and .ClientSpki of the Client certificate), routed by a VirtualService to v1. Each client
// case runs as its own case: accepted clients must reach v1, refused ones must fail the handshake. A
// feature without an accepted case would pass on a broken listener, so the accepted ones run first.
func gatewayTLS(tlsYAML string, clients func(gatewayTLSKeys) []tlsClientCase) func(t framework.TestContext, s *Scenario) {
	return gatewayServerTLS(gatewayTLSSpec{tls: `
      credentialName: {{.Cred}}` + tlsYAML, clients: clients})
}

// gatewayTLSSpec is one feature of the servers.tls family (see gatewayServerTLS).
type gatewayTLSSpec struct {
	// tls is the YAML of servers[].tls (indented for it), templated with .Cred and .RSACred (credentials
	// with an ECDSA and an RSA server certificate, both with the client CA as ca.crt), .Dir (the files of
	// mount), .ClientHash and .ClientSpki.
	tls string
	// mount mounts the same material as files into the gateway at .Dir: tls.crt and tls.key (ECDSA),
	// rsa.crt and rsa.key (RSA), ca.crt (client CA) and ca.crl (revoking the Other client certificate).
	mount   bool
	clients func(gatewayTLSKeys) []tlsClientCase
	// probes are TLS handshakes with openssl, for what the echo client cannot pin or observe.
	probes []tlsProbe
}

// gatewayServerTLS is the body of the servers.tls family. Every feature has an accepted client
// reaching v1 through the VirtualService (negative controls remove it); accepted cases run first.
func gatewayServerTLS(spec gatewayTLSSpec) func(t framework.TestContext, s *Scenario) {
	return func(t framework.TestContext, s *Scenario) {
		host := "vs." + s.Host()
		serverCA := newCA(t)
		server := serverCA.leaf(t, "ecdsa-server", host)
		rsaServer := rsaLeaf(t, serverCA, "rsa-server", host)
		clientCA := newCRLCA(t)
		client := clientCA.leaf(t, "client", "client.example.com")
		other := clientCA.leaf(t, "other", "other.example.com")
		untrusted := newCerts(t, "client", "client.example.com")
		opts := GatewayOptions{}
		if spec.mount {
			opts.Infrastructure = mountGatewayFiles(t, s, map[string]string{
				"tls.crt": server.Cert, "tls.key": server.Key, "rsa.crt": rsaServer.Cert, "rsa.key": rsaServer.Key,
				"ca.crt": client.CA, "ca.crl": clientCA.crl(t, other),
			})
		}
		gw := s.NewGateway(t, opts)
		// Istio Gateway credentialName secrets require the proxy to be allowed to read them.
		gw.GrantSecretRead(t)
		// The server credentials carry the client CA as ca.crt.
		s.TLSSecret(t, s.Name("cred"), Certs{CA: client.CA, Cert: server.Cert, Key: server.Key})
		s.TLSSecret(t, s.Name("rsa"), Certs{CA: client.CA, Cert: rsaServer.Cert, Key: rsaServer.Key})
		s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}-istio
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:
  - port: {number: 8443, name: https, protocol: HTTPS}
    hosts: ["vs.{{.Host}}"]
    tls:`+spec.tls+`
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["vs.{{.Host}}"]
  gateways: ["{{.Name}}-istio"]
  http:
  - route:
    - destination: {host: {{.BackendV1}}, port: {number: 80}}
`, map[string]any{
			"GW": gw.Name, "Cred": s.Name("cred"), "RSACred": s.Name("rsa"), "Dir": gatewayFilesDir,
			"ClientHash": certHash(t, client.Cert), "ClientSpki": spkiHash(t, client.Cert),
		})
		withCert := func(c Certs) func(*echo.CallOptions) {
			return func(o *echo.CallOptions) { o.TLS.Cert, o.TLS.Key = c.Cert, c.Key }
		}
		var cases []tlsClientCase
		if spec.clients != nil {
			cases = spec.clients(gatewayTLSKeys{Client: withCert(client), Other: withCert(other), Untrusted: withCert(untrusted)})
		} else {
			cases = []tlsClientCase{{"https", nil, true}}
		}
		// Port 8443 is not exposed on the generated Service; address the pod.
		addr := gw.PodIP(t)
		request := func(c tlsClientCase) Request {
			return Request{Address: addr, Host: host, Port: 8443, Scheme: scheme.HTTPS, Opts: func(o *echo.CallOptions) {
				tlsClient(host, server.CA)(o)
				if c.opts != nil {
					c.opts(o)
				}
			}}
		}
		for _, accepted := range []bool{true, false} {
			for _, c := range cases {
				if c.accepted != accepted {
					continue
				}
				s.Case(t, c.name, func(t framework.TestContext) {
					if c.accepted {
						gw.Expect(t, request(c), Version("v1"))
					} else {
						gw.ExpectStable(t, request(c), check.Error())
					}
				})
			}
			for _, p := range spec.probes {
				if p.accepted == accepted {
					s.Case(t, p.name, func(t framework.TestContext) { s.expectHandshake(t, addr, 8443, host, p) })
				}
			}
		}
	}
}

// Istio Gateway credential handling beyond a single server certificate: several servers and certificates
// on one port (SNI selection), a separate CA credential, and rotation of a credential in place.
func init() {
	Register(
		Feature{
			ID: "istioapi.gateway.tls-ca-cert-credential-name",
			Description: "Istio Gateway MUTUAL server with tls.caCertCredentialName verifies client certificates against that credential, " +
				"not the ca.crt of the server credential.",
			Roles:  ingressRole,
			Covers: []string{"Gateway.servers.tls.caCertCredentialName"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				gw.GrantSecretRead(t)
				host := "vs." + s.Host()
				server := newCerts(t, host, host)
				clientCA := newCA(t)
				client := clientCA.leaf(t, "client", "client.example.com")
				untrusted := newCerts(t, "client", "client.example.com")
				// The server credential's own ca.crt trusts the wrong CA: only caCertCredentialName admits the client.
				s.TLSSecret(t, s.Name("cred"), Certs{CA: untrusted.CA, Cert: server.Cert, Key: server.Key})
				s.CAConfigSecret(t, s.Name("ca"), client.CA)
				applyTLSServers(t, s, gw, `
  - port: {number: 8443, name: https, protocol: HTTPS}
    hosts: ["vs.{{.Host}}"]
    tls:
      mode: MUTUAL
      credentialName: {{.Cred}}
      caCertCredentialName: {{.CA}}
`, map[string][]string{"vs." + s.Host(): {"BackendV1"}}, map[string]any{"Cred": s.Name("cred"), "CA": s.Name("ca")})
				addr := gw.PodIP(t)
				req := func(c Certs) Request {
					return Request{Address: addr, Host: host, Port: 8443, Scheme: scheme.HTTPS, Opts: func(o *echo.CallOptions) {
						tlsClient(host, server.CA)(o)
						if c.Cert != "" {
							o.TLS.Cert, o.TLS.Key = c.Cert, c.Key
						}
					}}
				}
				gw.Expect(t, req(client), Version("v1"))
				gw.ExpectStable(t, req(untrusted), check.Error())
				gw.ExpectStable(t, req(Certs{}), check.Error())
			},
		},
		Feature{
			ID: "istioapi.gateway.tls-sni-servers",
			Description: "Istio Gateway servers on one port with different hosts and credentials present the certificate for the requested SNI " +
				"(exact and wildcard hosts), and a VirtualService routes each host to its own backend.",
			Roles:  ingressRole,
			Covers: []string{"Gateway.servers.hosts", "Gateway.servers.tls.credentialName", "Gateway.servers.port.name", "Gateway.servers.port.number"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				gw.GrantSecretRead(t)
				hostA, hostB, wild := "a.vs."+s.Host(), "b.vs."+s.Host(), "wild.vs."+s.Host()
				wildHost := "x." + wild
				a, b, w := newCerts(t, hostA, hostA), newCerts(t, hostB, hostB), newCerts(t, wild, "*."+wild)
				s.TLSSecret(t, s.Name("a"), a)
				s.TLSSecret(t, s.Name("b"), b)
				s.TLSSecret(t, s.Name("w"), w)
				applyTLSServers(t, s, gw, `
  - port: {number: 8443, name: https-a, protocol: HTTPS}
    hosts: ["`+hostA+`"]
    tls: {mode: SIMPLE, credentialName: `+s.Name("a")+`}
  - port: {number: 8443, name: https-b, protocol: HTTPS}
    hosts: ["`+hostB+`"]
    tls: {mode: SIMPLE, credentialName: `+s.Name("b")+`}
  - port: {number: 8443, name: https-w, protocol: HTTPS}
    hosts: ["*.`+wild+`"]
    tls: {mode: SIMPLE, credentialName: `+s.Name("w")+`}
`, map[string][]string{hostA: {"BackendV1"}, hostB: {"BackendV2"}, wildHost: {"BackendV1"}}, nil)
				addr := gw.PodIP(t)
				req := func(host, ca string) Request {
					return Request{Address: addr, Host: host, Port: 8443, Scheme: scheme.HTTPS, Opts: func(o *echo.CallOptions) {
						tlsClient(host, ca)(o)
						newConnPerRequest(o)
					}}
				}
				s.Case(t, "exact-hosts", func(t framework.TestContext) {
					gw.Expect(t, req(hostA, a.CA), Version("v1"))
					gw.Expect(t, req(hostB, b.CA), Version("v2"))
					// Each host is only valid with its own server's certificate.
					gw.ExpectStable(t, req(hostA, b.CA), check.Error())
					gw.ExpectStable(t, req(hostB, a.CA), check.Error())
				})
				s.Case(t, "wildcard-host", func(t framework.TestContext) {
					gw.Expect(t, req(wildHost, w.CA), Version("v1"))
					gw.ExpectStable(t, req(wildHost, a.CA), check.Error())
				})
			},
		},
		Feature{
			ID: "istioapi.gateway.tls-credential-rotation",
			Description: "Updating the secret behind an Istio Gateway credentialName is picked up without restarting the gateway: " +
				"new connections get the new certificate.",
			Roles:  ingressRole,
			Covers: []string{"Gateway.servers.tls.credentialName"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				gw.GrantSecretRead(t)
				host := "vs." + s.Host()
				before, after := newCerts(t, host, host), newCerts(t, host, host)
				s.TLSSecret(t, s.Name("cred"), before)
				applyTLSServers(t, s, gw, `
  - port: {number: 8443, name: https, protocol: HTTPS}
    hosts: ["vs.{{.Host}}"]
    tls: {mode: SIMPLE, credentialName: {{.Cred}}}
`, map[string][]string{host: {"BackendV1"}}, map[string]any{"Cred": s.Name("cred")})
				addr := gw.PodIP(t)
				req := func(ca string) Request {
					return Request{Address: addr, Host: host, Port: 8443, Scheme: scheme.HTTPS, Opts: func(o *echo.CallOptions) {
						tlsClient(host, ca)(o)
						newConnPerRequest(o)
					}}
				}
				gw.Expect(t, req(before.CA), Version("v1"))
				s.TLSSecret(t, s.Name("cred"), after)
				gw.Expect(t, req(after.CA), Version("v1"))
				gw.ExpectStable(t, req(before.CA), check.Error())
			},
		},
	)
}

// Istio Gateway servers beyond one TLS server: Istio mTLS termination and passthrough for mesh
// clients, bind addresses, and how servers of several Gateway resources on one proxy are merged.
func init() {
	Register(
		Feature{
			ID: "istioapi.gateway.tls-istio-mutual",
			Description: "Istio Gateway HTTPS server with tls.mode ISTIO_MUTUAL accepts Istio mTLS from a sidecar " +
				"and refuses clients without a mesh certificate.",
			Roles:  ingressRole,
			Covers: []string{"Gateway.servers.tls.mode=ISTIO_MUTUAL"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				host := "vs." + s.Host()
				applyTLSServers(t, s, gw, `
  - port: {number: 8443, name: https, protocol: HTTPS}
    hosts: ["vs.{{.Host}}"]
    tls: {mode: ISTIO_MUTUAL}
`, map[string][]string{host: {"BackendV1"}}, nil)
				vip := meshMTLSDestination(t, s, gw, "mtls", 8443, host)
				s.Case(t, "mesh-client", func(t framework.TestContext) {
					gw.MeshExpect(t, Request{Address: vip, Host: host, Port: 8443}, Version("v1"))
				})
				addr := gw.PodIP(t)
				untrusted := newCerts(t, "client", "client.example.com")
				for name, c := range map[string]Certs{"no-client-cert": {}, "untrusted-client-cert": untrusted} {
					s.Case(t, name, func(t framework.TestContext) {
						gw.ExpectStable(t, Request{Address: addr, Host: host, Port: 8443, Scheme: scheme.HTTPS, Opts: insecureTLSClient(host, c)}, check.Error())
					})
				}
			},
		},
		Feature{
			ID: "istioapi.gateway.tls-auto-passthrough",
			Description: "Istio Gateway TLS server with tls.mode AUTO_PASSTHROUGH forwards Istio mTLS by the outbound cluster in the SNI " +
				"to services matching its hosts, without a route, and refuses other services and non-Istio clients.",
			Roles:  ingressRole,
			Covers: []string{"Gateway.servers.tls.mode=AUTO_PASSTHROUGH"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				fqdn := func(svc string) string { return svc + "." + s.Namespace() + ".svc.cluster.local" }
				v1, v2 := fqdn(BackendSvc+"-v1"), fqdn(BackendSvc+"-v2")
				applyTLSServers(t, s, gw, `
  - port: {number: 8443, name: tls, protocol: TLS}
    hosts: ["`+v1+`"]
    tls: {mode: AUTO_PASSTHROUGH}
`, nil, nil)
				sni := func(h string) string { return "outbound_.80_._." + h }
				vip1 := meshMTLSDestination(t, s, gw, "v1", 8443, sni(v1))
				vip2 := meshMTLSDestination(t, s, gw, "v2", 8443, sni(v2))
				s.Case(t, "mesh-client-listed-host", func(t framework.TestContext) {
					gw.MeshExpect(t, Request{Address: vip1, Port: 8443}, Version("v1"))
				})
				s.Case(t, "mesh-client-other-host", func(t framework.TestContext) {
					gw.MeshExpectStable(t, Request{Address: vip2, Port: 8443}, check.Error())
				})
				s.Case(t, "non-istio-client", func(t framework.TestContext) {
					gw.ExpectStable(t, Request{Address: gw.PodIP(t), Port: 8443, Scheme: scheme.HTTPS, Opts: insecureTLSClient(sni(v1), Certs{})}, check.Error())
				})
			},
		},
		Feature{
			ID: "istioapi.gateway.server-bind",
			Description: "Istio Gateway servers.bind binds the server's listener to that address only: a server bound to the pod IP " +
				"is reachable on it, one bound to 127.0.0.1 is not reachable from outside the gateway.",
			Roles:  ingressRole,
			Covers: []string{"Gateway.servers.bind"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				addr := gw.PodIP(t)
				host := "vs." + s.Host()
				applyTLSServers(t, s, gw, `
  - port: {number: 8081, name: http-pod-ip, protocol: HTTP}
    bind: {{.PodIP}}
    hosts: ["vs.{{.Host}}"]
  - port: {number: 8082, name: http-loopback, protocol: HTTP}
    bind: 127.0.0.1
    hosts: ["vs.{{.Host}}"]
`, map[string][]string{host: {"BackendV1"}}, map[string]any{"PodIP": addr})
				gw.Expect(t, Request{Address: addr, Host: host, Port: 8081}, Version("v1"))
				gw.ExpectStable(t, Request{Address: addr, Host: host, Port: 8082}, check.Error())
			},
		},
		Feature{
			ID: "istioapi.gateway.server-merge",
			Description: "HTTPS servers of two Istio Gateway resources selecting one gateway on the same port are merged: " +
				"each host gets its own certificate and the routes of the VirtualService bound to its Gateway.",
			Roles:  ingressRole,
			Covers: []string{"interaction:gateway.server-merge", "Gateway.servers.port.number"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				gw.GrantSecretRead(t)
				hostA, hostB := "a.vs."+s.Host(), "b.vs."+s.Host()
				a, b := newCerts(t, hostA, hostA), newCerts(t, hostB, hostB)
				s.TLSSecret(t, s.Name("a"), a)
				s.TLSSecret(t, s.Name("b"), b)
				for _, g := range []struct{ name, host, backend string }{{"a", hostA, "{{.BackendV1}}"}, {"b", hostB, "{{.BackendV2}}"}} {
					s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}-{{.G}}
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:
  - port: {number: 8443, name: https, protocol: HTTPS}
    hosts: ["{{.H}}"]
    tls: {mode: SIMPLE, credentialName: {{.Cred}}}
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}-{{.G}}
spec:
  hosts: ["{{.H}}"]
  gateways: ["{{.Name}}-{{.G}}"]
  http:
  - route:
    - destination: {host: `+g.backend+`, port: {number: 80}}
`, map[string]any{"G": g.name, "H": g.host, "GW": gw.Name, "Cred": s.Name(g.name)})
				}
				addr := gw.PodIP(t)
				req := func(host, ca string) Request {
					return Request{Address: addr, Host: host, Port: 8443, Scheme: scheme.HTTPS, Opts: func(o *echo.CallOptions) {
						tlsClient(host, ca)(o)
						newConnPerRequest(o)
					}}
				}
				gw.Expect(t, req(hostA, a.CA), Version("v1"))
				gw.Expect(t, req(hostB, b.CA), Version("v2"))
				gw.ExpectStable(t, req(hostA, b.CA), check.Error())
			},
		},
		Feature{
			ID: "istioapi.gateway.same-port-tls-conflict",
			Description: "When two Istio Gateway resources declare a TLS server for the same host and port, the older one is kept: " +
				"the newer Gateway's conflicting server is ignored while its other servers apply.",
			Roles:  ingressRole,
			Covers: []string{"interaction:gateway.same-port-tls-conflict"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				gw.GrantSecretRead(t)
				host, witness := "vs."+s.Host(), "w.vs."+s.Host()
				older, newer := newCerts(t, host, host), newCerts(t, host, host, witness)
				s.TLSSecret(t, s.Name("older"), older)
				s.TLSSecret(t, s.Name("newer"), newer)
				// The newer Gateway sorts first by name, so only its creation time makes it lose.
				gateway := `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}-{{.G}}
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:
  - port: {number: 8443, name: https, protocol: HTTPS}
    hosts: ["` + host + `"]
    tls: {mode: SIMPLE, credentialName: {{.Cred}}}
{{- if .Witness }}
  - port: {number: 8443, name: https-witness, protocol: HTTPS}
    hosts: ["` + witness + `"]
    tls: {mode: SIMPLE, credentialName: {{.Cred}}}
{{- end }}
`
				s.Apply(t, gateway+`---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["`+host+`", "`+witness+`"]
  gateways: ["{{.Name}}-z-older", "{{.Name}}-a-newer"]
  http:
  - match: [{authority: {prefix: "`+witness+`"}}]
    route:
    - destination: {host: {{.BackendV2}}, port: {number: 80}}
  - route:
    - destination: {host: {{.BackendV1}}, port: {number: 80}}
`, map[string]any{"G": "z-older", "Witness": false, "GW": gw.Name, "Cred": s.Name("older")})
				addr := gw.PodIP(t)
				req := func(host, ca string) Request {
					return Request{Address: addr, Host: host, Port: 8443, Scheme: scheme.HTTPS, Opts: func(o *echo.CallOptions) {
						tlsClient(host, ca)(o)
						newConnPerRequest(o)
					}}
				}
				start := time.Now()
				gw.Expect(t, req(host, older.CA), Version("v1"))
				// Creation timestamps have a resolution of one second.
				retry.UntilSuccessOrFail(t, func() error {
					if time.Since(start) < 2*time.Second {
						return fmt.Errorf("waiting for a later creation timestamp")
					}
					return nil
				}, retry.Timeout(10*time.Second), retry.Delay(200*time.Millisecond))
				s.Apply(t, gateway, map[string]any{"G": "a-newer", "Witness": true, "GW": gw.Name, "Cred": s.Name("newer")})
				// The newer Gateway's other server is not a duplicate, so it is served once the Gateway applies.
				gw.Expect(t, req(witness, newer.CA), Version("v2"))
				gw.ExpectStable(t, req(host, older.CA), Version("v1"))
				gw.ExpectStable(t, req(host, newer.CA), check.Error())
			},
		},
		Feature{
			ID: "istioapi.gateway.strict-merging",
			Description: "With strict Gateway merging (default), an Istio Gateway in another namespace selecting a Gateway API gateway's pods " +
				"is not merged into it, while Istio Gateways in the gateway's own namespace are.",
			Roles:  ingressRole,
			Covers: []string{"flag:istiod:PILOT_ENABLE_STRICT_GATEWAY_MERGING"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				host, other, witness := "vs."+s.Host(), "other.vs."+s.Host(), "witness.vs."+s.Host()
				applyTLSServers(t, s, gw, `
  - port: {number: 8081, name: http, protocol: HTTP}
    hosts: ["vs.{{.Host}}", "witness.vs.{{.Host}}"]
`, map[string][]string{host: {"BackendV1"}}, nil)
				// In the other namespace: a Gateway selecting the same pods with its own host and route, and a
				// witness route bound to the local Gateway, applied together.
				s.ApplyTo(t, s.Env.ExternalNamespace.Name(), `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}-other
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:
  - port: {number: 8081, name: http, protocol: HTTP}
    hosts: ["`+other+`"]
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}-other
spec:
  hosts: ["`+other+`"]
  gateways: ["{{.Name}}-other"]
  http:
  - route:
    - destination: {host: {{.BackendV1}}.{{.Namespace}}.svc.cluster.local, port: {number: 80}}
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}-witness
spec:
  hosts: ["`+witness+`"]
  gateways: ["{{.Namespace}}/{{.Name}}-istio"]
  http:
  - route:
    - destination: {host: {{.BackendV1}}.{{.Namespace}}.svc.cluster.local, port: {number: 80}}
`, map[string]any{"GW": gw.Name})
				addr := gw.PodIP(t)
				gw.Expect(t, Request{Address: addr, Host: host, Port: 8081}, Version("v1"))
				gw.Expect(t, Request{Address: addr, Host: witness, Port: 8081}, Version("v1"))
				gw.ExpectStable(t, Request{Address: addr, Host: other, Port: 8081}, check.Status(http.StatusNotFound))
			},
		},
	)
}

// insecureTLSClient configures the call for TLS with SNI sni, without verifying the server, presenting
// the client certificate c if set.
func insecureTLSClient(sni string, c Certs) func(*echo.CallOptions) {
	return func(o *echo.CallOptions) {
		o.TLS.ServerName = sni
		o.TLS.InsecureSkipVerify = true
		if c.Cert != "" {
			o.TLS.Cert, o.TLS.Key = c.Cert, c.Key
		}
	}
}

func init() {
	Register(
		Feature{
			ID: "istioapi.gateway.server-name",
			Description: "Istio Gateway servers.name names the stats of a TLS-terminating server: after a request to it, " +
				"the gateway's Prometheus metrics carry the server name.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"Gateway.servers.name"},
			Run: func(t framework.TestContext, s *Scenario) {
				name := s.Name("server")
				// Envoy only creates the stats matching its inclusion list; pilot uses the server name as the
				// HTTP connection manager stat prefix ("http.<name>.").
				gw := s.NewGateway(t, GatewayOptions{Annotations: map[string]string{
					"proxy.istio.io/config": `{"proxyStatsMatcher":{"inclusionPrefixes":["http.` + name + `."]}}`,
				}})
				gw.GrantSecretRead(t)
				host := "vs." + s.Host()
				server := newCerts(t, host, host)
				s.TLSSecret(t, s.Name("cred"), server)
				applyTLSServers(t, s, gw, `
  - port: {number: 8443, name: https, protocol: HTTPS}
    name: {{.Server}}
    hosts: ["vs.{{.Host}}"]
    tls: {mode: SIMPLE, credentialName: {{.Cred}}}
`, map[string][]string{host: {"BackendV1"}}, map[string]any{"Server": name, "Cred": s.Name("cred")})
				// Port 8443 is not exposed on the generated Service; address the pod.
				req := Request{Address: gw.PodIP(t), Host: host, Port: 8443, Scheme: scheme.HTTPS, Count: 3, Opts: tlsClient(host, server.CA)}
				// Any response (even a 404 once the control strips the route) passes through the server's
				// connection manager, so only the metric check below depends on the feature config.
				gw.Expect(t, req, check.NoError())
				// Prometheus exposition replaces '-' with '_' in metric names, but not in label values.
				underscored := strings.ReplaceAll(name, "-", "_")
				retry.UntilSuccessOrFail(t, func() error {
					m, err := gw.Metrics(t)
					if err != nil {
						return err
					}
					if !strings.Contains(m, name) && !strings.Contains(m, underscored) {
						return fmt.Errorf("no metric names the server %q", name)
					}
					return nil
				}, retry.Timeout(time.Minute), retry.Delay(2*time.Second))
			},
		},
	)
}

// applyTLSServers applies an Istio Gateway with the given servers (YAML list items, templated) selecting
// the dedicated gateway's pods, and a VirtualService routing each host to the named template variable
// (BackendV1 or BackendV2) on port 80.
func applyTLSServers(t framework.TestContext, s *Scenario, gw *Gateway, servers string, routes map[string][]string, extra map[string]any) {
	t.Helper()
	type route struct{ Host, Backend string }
	var rs []route
	for h, b := range routes {
		rs = append(rs, route{h, b[0]})
	}
	vars := map[string]any{"GW": gw.Name, "Routes": rs}
	for k, v := range extra {
		vars[k] = v
	}
	s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}-istio
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:`+servers+`
{{- range $i, $r := .Routes }}
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{$.Name}}-{{$i}}
spec:
  hosts: ["{{ $r.Host }}"]
  gateways: ["{{$.Name}}-istio"]
  http:
  - route:
    - destination: {host: {{ index $ $r.Backend }}, port: {number: 80}}
{{- end }}
`, vars)
}
