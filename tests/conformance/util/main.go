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

// Command util generates a single-page, offline HTML report to browse the proxy conformance suite
// features, their declared support per proxy and the API surface coverage.
//
// Usage (from the repository root):
//
//	go run ./tests/conformance/util -out conformance.html
package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"istio.io/istio/pkg/slices"
	"istio.io/istio/tests/conformance/proxy/coverage"
)

//go:embed report.html.tmpl
var pageTemplate string

//go:embed app.js
var appScript string

const (
	areaAuthn      = "Authentication (mTLS & JWT)"
	areaAuthz      = "Authorization"
	areaRouting    = "Routing & traffic shifting"
	areaResilience = "Load balancing & resilience"
	areaDiscovery  = "Service discovery & sidecar scope"
	areaGateways   = "Gateways & listeners"
	areaGatewayAPI = "Gateway API"
	areaObserv     = "Observability"
	areaProtocols  = "Protocols"
	areaHeaders    = "Headers & connection handling"
	areaEgress     = "Egress"
	areaExtension  = "Extensibility (implementation specific)"
	areaFlags      = "Feature flags"
	areaOther      = "Other"
)

// areaRules map a feature ID prefix to the area it is shown under. The longest matching prefix wins.
var areaRules = map[string]string{
	"security.authz.":                            areaAuthz,
	"mesh.authz":                                 areaAuthz,
	"egress.authz":                               areaAuthz,
	"security.jwt.":                              areaAuthn,
	"mesh.mtls":                                  areaAuthn,
	"mesh.peerauthentication":                    areaAuthn,
	"dataplane.permissive-mtls-default":          areaAuthn,
	"meshconfig.enable-auto-mtls-disabled":       areaAuthn,
	"sidecar.ingress-tls":                        areaAuthn,
	"security.mtls.":                             areaAuthn,
	"meshconfig.trust-domain":                    areaAuthn,
	"meshconfig.ca-certificates":                 areaAuthn,
	"istioapi.virtualservice.":                   areaRouting,
	"mesh.routing":                               areaRouting,
	"mesh.timeout":                               areaRouting,
	"mesh.fault":                                 areaRouting,
	"mesh.baseline":                              areaRouting,
	"dataplane.no-route-404":                     areaRouting,
	"dataplane.no-default-route-timeout":         areaRouting,
	"dataplane.default-retry-on-connect-failure": areaRouting,
	"meshconfig.default-http-retry-policy":       areaRouting,
	"backend.destinationrule.":                   areaResilience,
	"backend.mesh":                               areaResilience,
	"meshconfig.h2-upgrade-policy":               areaResilience,
	"meshconfig.connect-timeout":                 areaResilience,
	"backend.serviceentry.":                      areaDiscovery,
	"sidecar.":                                   areaDiscovery,
	"dataplane.outbound-allow-any":               areaDiscovery,
	"backend.kubernetes-externalname":            areaDiscovery,
	"backend.workloadentry.":                     areaDiscovery,
	"meshconfig.default-":                        areaDiscovery,
	"meshconfig.discovery-selectors":             areaDiscovery,
	"meshconfig.root-namespace":                  areaDiscovery,
	"meshconfig.service-entry-visibility":        areaDiscovery,
	"istioapi.gateway":                           areaGateways,
	"listener.":                                  areaGateways,
	"infra.":                                     areaGatewayAPI,
	"status.":                                    areaGatewayAPI,
	"telemetry.":                                 areaObserv,
	"meshconfig.access-log":                      areaObserv,
	"dataplane.trace-context-propagation":        areaObserv,
	"dataplane.request-id":                       areaObserv,
	"protocol.":                                  areaProtocols,
	"proxyconfig.":                               areaHeaders,
	"dataplane.":                                 areaHeaders,
	"meshconfig.path-normalization":              areaHeaders,
	"egress.":                                    areaEgress,
	"impl-specific.":                             areaExtension,
	"flag.":                                      areaFlags,
}

func areaOf(id string) string {
	best := ""
	for p := range areaRules {
		if strings.HasPrefix(id, p) && len(p) > len(best) {
			best = p
		}
	}
	if best == "" {
		return areaOther
	}
	return areaRules[best]
}

type catalogFeature struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Roles       []string `json:"roles"`
	Profile     string   `json:"profile"`
	RequiresL7  bool     `json:"requiresL7"`
	Security    bool     `json:"security"`
	Covers      []string `json:"covers"`
	Cases       []string `json:"cases"`
}

type coverageEntry struct {
	Status    string              `json:"status"`
	Scope     []string            `json:"scope"`
	CoveredBy map[string][]string `json:"coveredBy"`
	Gaps      []string            `json:"gaps"`
}

// Output types, serialized into the page.
type cell struct {
	Status string   `json:"status"`
	Reason string   `json:"reason,omitempty"`
	Issue  string   `json:"issue,omitempty"`
	Cases  []string `json:"cases,omitempty"`
}

type featureOut struct {
	ID          string   `json:"id"`
	Area        string   `json:"area"`
	Description string   `json:"description"`
	Profile     string   `json:"profile"`
	RequiresL7  bool     `json:"requiresL7,omitempty"`
	Security    bool     `json:"security,omitempty"`
	Covers      []string `json:"covers,omitempty"`
	Cases       []string `json:"cases,omitempty"`
	// Kinds are the API kinds the feature applies in this role, as recorded by the reference run.
	Kinds []string        `json:"kinds,omitempty"`
	Cells map[string]cell `json:"cells,omitempty"` // keyed by implementation
}

type roleOut struct {
	Name string `json:"name"`
	// Impls lists the role's implementations, the reference implementation first.
	Impls     []string     `json:"impls"`
	Reference string       `json:"reference,omitempty"`
	Features  []featureOut `json:"features"`
}

type surfaceOut struct {
	Entry     string              `json:"entry"`
	Kind      string              `json:"kind"`
	Source    string              `json:"source"`
	Status    string              `json:"status"` // covered, todo, exempt
	Reason    string              `json:"reason,omitempty"`
	Scope     []string            `json:"scope,omitempty"`
	CoveredBy map[string][]string `json:"coveredBy,omitempty"`
	Gaps      []string            `json:"gaps,omitempty"`
}

type pageData struct {
	Roles   []roleOut    `json:"roles"`
	Surface []surfaceOut `json:"surface"`
}

// packedData is the compact form of pageData embedded in the page; app.js unpacks it. Feature
// metadata is stored once rather than per role and the surface is stored column-wise, which roughly
// halves the compressed size (deflate only finds repeats within 32KB).
type packedData struct {
	Features []featureOut  `json:"features"` // without cells and kinds
	Roles    []packedRole  `json:"roles"`
	Surface  packedSurface `json:"surface"`
	// KindAPIs maps every kind in Roles[].Kinds to its API (coverage.KindAPI).
	KindAPIs map[string]string `json:"kindAPIs"`
}

type packedRole struct {
	Name      string            `json:"name"`
	Impls     []string          `json:"impls"`
	Reference string            `json:"reference,omitempty"`
	IDs       []string          `json:"ids"`
	Cells     []map[string]cell `json:"cells"` // parallel to IDs
	Kinds     [][]string        `json:"kinds"` // parallel to IDs
}

type packedSurface struct {
	Entry     []string              `json:"entry"`
	Kind      []string              `json:"kind"`
	Source    []string              `json:"source"`
	Status    []string              `json:"status"`
	Reason    []string              `json:"reason"`
	Scope     [][]string            `json:"scope"`
	CoveredBy []map[string][]string `json:"coveredBy"`
	Gaps      [][]string            `json:"gaps"`
}

func pack(pd pageData) packedData {
	p := packedData{KindAPIs: map[string]string{}}
	seen := map[string]bool{}
	for _, r := range pd.Roles {
		pr := packedRole{Name: r.Name, Impls: r.Impls, Reference: r.Reference}
		for _, f := range r.Features {
			pr.IDs = append(pr.IDs, f.ID)
			pr.Cells = append(pr.Cells, f.Cells)
			pr.Kinds = append(pr.Kinds, f.Kinds)
			for _, k := range f.Kinds {
				p.KindAPIs[k] = coverage.KindAPI(k)
			}
			if !seen[f.ID] {
				seen[f.ID] = true
				f.Cells = nil
				f.Kinds = nil
				p.Features = append(p.Features, f)
			}
		}
		p.Roles = append(p.Roles, pr)
	}
	s := &p.Surface
	for _, e := range pd.Surface {
		s.Entry = append(s.Entry, e.Entry)
		s.Kind = append(s.Kind, e.Kind)
		s.Source = append(s.Source, e.Source)
		s.Status = append(s.Status, e.Status)
		s.Reason = append(s.Reason, e.Reason)
		s.Scope = append(s.Scope, e.Scope)
		s.CoveredBy = append(s.CoveredBy, e.CoveredBy)
		s.Gaps = append(s.Gaps, e.Gaps)
	}
	return p
}

func readYAML(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	return nil
}

func main() {
	suite := flag.String("suite", coverage.SuiteDir, "proxy conformance suite directory")
	out := flag.String("out", "conformance.html", "output HTML file")
	flag.Parse()
	data, err := build(*suite)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := render(data, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", *out)
}

func build(suite string) (pageData, error) {
	dataDir := filepath.Join(suite, "coverage", coverage.DataDir)
	var catalog []catalogFeature
	if err := readYAML(filepath.Join(dataDir, "catalog.yaml"), &catalog); err != nil {
		return pageData{}, err
	}
	pd := pageData{}

	// Roles and implementations come from the declared manifests.
	roleDirs, err := os.ReadDir(filepath.Join(suite, coverage.DeclaredDir))
	if err != nil {
		return pd, err
	}
	for _, rd := range roleDirs {
		if !rd.IsDir() {
			continue
		}
		role := rd.Name()
		files, err := filepath.Glob(filepath.Join(suite, coverage.DeclaredDir, role, "*.yaml"))
		if err != nil {
			return pd, err
		}
		sort.Strings(files)
		manifests := map[string]coverage.Manifest{}
		ro := roleOut{Name: role}
		for _, f := range files {
			impl := strings.TrimSuffix(filepath.Base(f), ".yaml")
			m, err := coverage.LoadManifest(f)
			if err != nil {
				return pd, err
			}
			manifests[impl] = m
			if m.Reference {
				ro.Reference = impl
				ro.Impls = append([]string{impl}, ro.Impls...)
			} else {
				ro.Impls = append(ro.Impls, impl)
			}
		}
		for _, f := range catalog {
			if !slices.Contains(f.Roles, role) {
				continue
			}
			fo := featureOut{
				ID: f.ID, Area: areaOf(f.ID), Description: f.Description, Profile: f.Profile,
				RequiresL7: f.RequiresL7, Security: f.Security, Covers: f.Covers, Cases: f.Cases,
				Cells: map[string]cell{},
			}
			for impl, m := range manifests {
				d, ok := m.Features[f.ID]
				if !ok {
					d = coverage.Declared{Status: "missing"}
				}
				fo.Cells[impl] = cell{Status: d.Status, Reason: d.Reason, Issue: d.Issue, Cases: d.Cases}
				if m.Reference {
					fo.Kinds = d.Kinds
				}
			}
			ro.Features = append(ro.Features, fo)
		}
		sort.Slice(ro.Features, func(i, j int) bool {
			a, b := ro.Features[i], ro.Features[j]
			if a.Area != b.Area {
				return a.Area < b.Area
			}
			return a.ID < b.ID
		})
		pd.Roles = append(pd.Roles, ro)
	}

	// API surface.
	sources := map[string]string{}
	if err := readYAML(filepath.Join(dataDir, "surface.yaml"), &sources); err != nil {
		return pd, err
	}
	cov := map[string]json.RawMessage{}
	if err := readYAML(filepath.Join(dataDir, "coverage.yaml"), &cov); err != nil {
		return pd, err
	}
	var ex coverage.Exemptions
	if err := readYAML(filepath.Join(dataDir, coverage.ExemptionsFile), &ex); err != nil {
		return pd, err
	}
	for entry, src := range sources {
		so := surfaceOut{Entry: entry, Source: src, Kind: kindOf(entry), Status: "todo"}
		var exemptOnly struct {
			Exempt any `json:"exempt"`
		}
		var e coverageEntry
		raw := cov[entry]
		if json.Unmarshal(raw, &exemptOnly) == nil && exemptOnly.Exempt != nil {
			so.Status = "exempt"
			key := entry
			if s, ok := exemptOnly.Exempt.(string); ok {
				key = s
			}
			so.Reason = ex.Exempt[key]
		} else if json.Unmarshal(raw, &e) == nil && e.Status != "" {
			so.Status, so.Scope, so.CoveredBy, so.Gaps = e.Status, e.Scope, e.CoveredBy, e.Gaps
		}
		pd.Surface = append(pd.Surface, so)
	}
	sort.Slice(pd.Surface, func(i, j int) bool { return pd.Surface[i].Entry < pd.Surface[j].Entry })
	return pd, nil
}

func kindOf(entry string) string {
	if i := strings.IndexAny(entry, ".=:"); i > 0 {
		return entry[:i]
	}
	return entry
}

func render(pd pageData, path string) error {
	b, err := json.Marshal(struct {
		App  string     `json:"app"`
		Data packedData `json:"data"`
	}{appScript, pack(pd)})
	if err != nil {
		return err
	}
	t, err := template.New("page").Parse(pageTemplate)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := compress(b)
	if err != nil {
		return err
	}
	// Embedded as a JSON string; base64 contains no markup characters, so it is safe inside a script element.
	js, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return t.Execute(f, struct{ Data template.JS }{template.JS(js)})
}

// compress gzips and base64-encodes the page payload (the app script and data). This keeps the file
// small for previews that truncate large files (Slack's thread viewer showed only about 50KB); the
// page decompresses it with the browser's DecompressionStream.
func compress(b []byte) (string, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := zw.Write(b); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
