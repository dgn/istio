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

package coverage

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	yamlv3 "gopkg.in/yaml.v3" // nolint: depguard // generated views keep struct field order and flow-style lists

	"istio.io/istio/pkg/maps"
	"istio.io/istio/pkg/slices"
)

// Statuses of a surface entry in coverage.yaml.
const (
	// EntryCovered: at least one feature covers the entry.
	EntryCovered = "covered"
	// EntryExempt: exempted with a reason in exemptions.yaml (directly or by a "*" prefix).
	EntryExempt = "exempt"
	// EntryTodo: neither covered nor exempt; the backlog, bounded by maxTodo.
	EntryTodo = "todo"
)

// Entry is the resolved state of one surface entry: where it comes from (surface.yaml), whether it is
// covered, and which roles still need to cover it (coverage.yaml).
type Entry struct {
	Source string `yaml:"-"`
	Status string `yaml:"status"`
	// Scope are the roles that must each cover the entry (scope.yaml); empty for exempt entries.
	Scope flowList `yaml:"scope,omitempty"`
	// CoveredBy maps a role to the features of that role that cover the entry.
	CoveredBy map[string]flowList `yaml:"coveredBy,omitempty"`
	// Gaps are the roles in Scope with no covering feature.
	Gaps flowList `yaml:"gaps,omitempty"`
	// ExemptBy is the "*" exemption the entry falls under, if any. Its reason is in exemptions.yaml.
	ExemptBy string `yaml:"exemptBy,omitempty"`
}

// entryCoverage is how coverage.yaml renders a covered or todo Entry.
type entryCoverage struct {
	Status    string              `yaml:"status"`
	Scope     flowList            `yaml:"scope,omitempty"`
	CoveredBy map[string]flowList `yaml:"coveredBy,omitempty"`
	Gaps      flowList            `yaml:"gaps,omitempty"`
}

// MarshalYAML renders an exempt entry on one line, {exempt: true} or {exempt: <"*" exemption>}, as
// its reason is in exemptions.yaml; other entries as entryCoverage.
func (e Entry) MarshalYAML() (any, error) {
	if e.Status != EntryExempt {
		return entryCoverage{Status: e.Status, Scope: e.Scope, CoveredBy: e.CoveredBy, Gaps: e.Gaps}, nil
	}
	v := &yamlv3.Node{Kind: yamlv3.ScalarNode, Tag: "!!bool", Value: "true"}
	if e.ExemptBy != "" {
		v = &yamlv3.Node{Kind: yamlv3.ScalarNode, Tag: "!!str", Value: e.ExemptBy}
	}
	return &yamlv3.Node{Kind: yamlv3.MappingNode, Style: yamlv3.FlowStyle, Content: []*yamlv3.Node{
		{Kind: yamlv3.ScalarNode, Tag: "!!str", Value: "exempt"}, v,
	}}, nil
}

// UnmarshalYAML reads what MarshalYAML renders.
func (e *Entry) UnmarshalYAML(n *yamlv3.Node) error {
	var exempt struct {
		Exempt yamlv3.Node `yaml:"exempt"`
	}
	if err := n.Decode(&exempt); err != nil {
		return err
	}
	if exempt.Exempt.Kind != 0 {
		*e = Entry{Status: EntryExempt}
		if exempt.Exempt.Tag != "!!bool" {
			e.ExemptBy = exempt.Exempt.Value
		}
		return nil
	}
	var c entryCoverage
	if err := n.Decode(&c); err != nil {
		return err
	}
	*e = Entry{Status: c.Status, Scope: c.Scope, CoveredBy: c.CoveredBy, Gaps: c.Gaps}
	return nil
}

// flowList is a list rendered in flow style ([a, b]), which keeps coverage.yaml readable.
type flowList []string

func (l flowList) MarshalYAML() (any, error) {
	n := &yamlv3.Node{Kind: yamlv3.SequenceNode, Style: yamlv3.FlowStyle}
	for _, s := range l {
		n.Content = append(n.Content, &yamlv3.Node{Kind: yamlv3.ScalarNode, Tag: "!!str", Value: s})
	}
	return n, nil
}

// Resolution is the outcome of resolving the surface against the features and the config.
type Resolution struct {
	Entries      map[string]*Entry
	Suite        *Suite
	Interactions []Interaction
	Config       Config
	// Stale exemptions name nothing that exists; Redundant ones name entries a feature covers.
	Stale, Redundant []string
	// Errors are problems in the features, interactions or config that -update cannot fix.
	Errors []string
}

// Resolve computes each surface entry's status, scope, covering features and gaps. sources is
// SurfaceSources(); keys are the translator keys of ExtensionKeys, to check "key:" exemptions.
func Resolve(sources map[string]string, s *Suite, interactions []Interaction, cfg Config, keys map[string]bool) *Resolution {
	r := &Resolution{Entries: map[string]*Entry{}, Suite: s, Interactions: interactions, Config: cfg}
	knownRoles := map[string]bool{}
	for _, role := range s.Roles {
		knownRoles[role] = true
	}
	for _, f := range s.Features {
		if len(f.Roles) == 0 {
			r.errorf("%s: feature %q lists no Roles: list every role the behavior applies to", f.Pos, f.ID)
		}
		for _, role := range f.Roles {
			if !knownRoles[role] {
				r.errorf("%s: feature %q lists unknown role %q", f.Pos, f.ID, role)
			}
		}
	}
	for _, role := range s.Roles {
		if len(s.RoleFeatures(role)) == 0 {
			r.errorf("role %q has no conformance features: every declared role must be exercised", role)
		}
	}
	r.Errors = append(r.Errors, featureIDErrors(s.Features)...)

	// coveredBy[entry][role] are the features of the role covering the entry.
	coveredBy := map[string]map[string][]string{}
	cover := func(entry, role, id string) {
		if coveredBy[entry] == nil {
			coveredBy[entry] = map[string][]string{}
		}
		if !slices.Contains(coveredBy[entry][role], id) {
			coveredBy[entry][role] = append(coveredBy[entry][role], id)
		}
	}
	byID := map[string]Feature{}
	for _, f := range s.Features {
		byID[f.ID] = f
		for _, c := range f.Covers {
			if _, ok := sources[c]; !ok {
				r.errorf("%s: feature %q covers %q, which is not part of the API surface (typo, or removed field?)", f.Pos, f.ID, c)
				continue
			}
			for _, role := range f.Roles {
				cover(c, role, f.ID)
			}
		}
	}
	ierrs, icovered := checkInteractions(interactions, s.Features, knownRoles)
	r.Errors = append(r.Errors, ierrs...)
	for entry, ids := range icovered {
		for _, id := range ids {
			for _, role := range byID[id].Roles {
				cover(entry, role, id)
			}
		}
	}

	scope := r.scopeKeys(sources, knownRoles)
	prefixes := cfg.Exemptions.prefixes()
	for entry, source := range sources {
		e := &Entry{Source: source}
		r.Entries[entry] = e
		if len(coveredBy[entry]) > 0 {
			e.CoveredBy = map[string]flowList{}
			for role, ids := range coveredBy[entry] {
				e.CoveredBy[role] = slices.Sort(ids)
			}
		}
		_, explicit := cfg.Exemptions.Exempt[entry]
		wildcard := prefixes.match(entry)
		if wildcard != "" {
			e.ExemptBy = wildcard + "*"
		}
		switch {
		case explicit:
			e.Status = EntryExempt
			if len(coveredBy[entry]) > 0 {
				r.Redundant = append(r.Redundant, entry)
			}
		case len(coveredBy[entry]) > 0:
			e.Status = EntryCovered
		case wildcard != "":
			e.Status = EntryExempt
		default:
			e.Status = EntryTodo
		}
		if e.Status == EntryExempt || wildcard != "" {
			continue // out of scope for every role
		}
		e.Scope = scopeFor(scope, cfg.Scope.Default, entry)
		for _, role := range e.Scope {
			if len(coveredBy[entry][role]) == 0 {
				e.Gaps = append(e.Gaps, role)
			}
		}
	}
	sort.Strings(r.Redundant)

	for ex, reason := range cfg.Exemptions.Exempt {
		switch strings.TrimSpace(reason) {
		case "":
			r.errorf("exemption %q has no reason: give one in coverage/data/%s", ex, ExemptionsFile)
		case EntryTodo:
			r.errorf("exemption %q is %q: the backlog is derived, so remove the entry from coverage/data/%s (it shows as todo in %s)",
				ex, EntryTodo, ExemptionsFile, SurfaceFile)
		}
		switch {
		case strings.HasPrefix(ex, "key:"):
			if !keys[strings.TrimPrefix(ex, "key:")] {
				r.Stale = append(r.Stale, ex)
			}
		case strings.HasSuffix(ex, "*"):
			pre := strings.TrimSuffix(ex, "*")
			if !slices.ContainsFunc(maps.Keys(sources), func(s string) bool { return strings.HasPrefix(s, pre) }) {
				r.Stale = append(r.Stale, ex)
			}
		default:
			if _, ok := sources[ex]; !ok {
				r.Stale = append(r.Stale, ex)
			}
		}
	}
	sort.Strings(r.Stale)
	for role := range cfg.Ratchets.MaxRoleGaps {
		if !knownRoles[role] {
			r.errorf("maxRoleGaps in coverage/data/%s lists unknown role %q", RatchetsFile, role)
		}
	}
	sort.Strings(r.Errors)
	return r
}

func (r *Resolution) errorf(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

// scopeKeys merges the implicit roleScope of the interactions with scope.yaml (which wins), and
// validates scope.yaml.
func (r *Resolution) scopeKeys(sources map[string]string, knownRoles map[string]bool) map[string][]string {
	out := interactionScope(r.Interactions)
	if len(r.Config.Scope.Default) == 0 {
		r.errorf("coverage/data/%s: default lists no roles: entries no roleScope key matches would be in nobody's scope", ScopeFile)
	}
	for _, role := range r.Config.Scope.Default {
		if !knownRoles[role] {
			r.errorf("coverage/data/%s: default lists unknown role %q", ScopeFile, role)
		}
	}
	for pre, roles := range r.Config.Scope.RoleScope {
		if !slices.ContainsFunc(maps.Keys(sources), func(s string) bool { return inScope(s, pre) }) {
			r.errorf("coverage/data/%s: roleScope key %q matches no surface entry: fix or remove it", ScopeFile, pre)
		}
		for _, role := range roles {
			if !knownRoles[role] {
				r.errorf("coverage/data/%s: roleScope key %q lists unknown role %q", ScopeFile, pre, role)
			}
		}
		out[pre] = roles
	}
	return out
}

// Roles returns the roles that must cover the entry (interaction keys aside).
func (s Scope) Roles(entry string) []string {
	return scopeFor(s.RoleScope, s.Default, entry)
}

// scopeFor returns the roles of the longest scope key matching the entry, or def.
func scopeFor(scope map[string][]string, def []string, entry string) flowList {
	best := ""
	for pre := range scope {
		if inScope(entry, pre) && len(pre) > len(best) {
			best = pre
		}
	}
	if best == "" {
		return slices.Sort(slices.Clone(def))
	}
	return slices.Sort(slices.Clone(scope[best]))
}

// inScope reports whether entry s is pre itself or below it (a field or enum value of pre). A
// trailing "*" makes pre a plain string prefix, e.g. "annotation:sidecar.istio.io/*".
func inScope(s, pre string) bool {
	if p, ok := strings.CutSuffix(pre, "*"); ok {
		return strings.HasPrefix(s, p)
	}
	return s == pre || strings.HasPrefix(s, pre+".") || strings.HasPrefix(s, pre+"=")
}

// interactionScope returns the implicit roleScope of the interactions: each interaction must be
// covered by every role it lists.
func interactionScope(interactions []Interaction) map[string][]string {
	out := map[string][]string{}
	for _, i := range interactions {
		if len(i.Roles) > 0 {
			out["interaction:"+i.ID] = i.Roles
		}
	}
	return out
}

// Todo returns the sorted entries with status todo.
func (r *Resolution) Todo() []string {
	var out []string
	for entry, e := range r.Entries {
		if e.Status == EntryTodo {
			out = append(out, entry)
		}
	}
	return slices.Sort(out)
}

// Gaps returns, for every role in scope of some entry, the sorted entries it does not cover.
func (r *Resolution) Gaps() map[string][]string {
	out := map[string][]string{}
	for _, role := range r.ScopedRoles() {
		out[role] = []string{}
	}
	for entry, e := range r.Entries {
		for _, role := range e.Gaps {
			out[role] = append(out[role], entry)
		}
	}
	for role := range out {
		sort.Strings(out[role])
	}
	return out
}

// ScopedRoles returns the roles that scope.yaml or interactions.yaml put in scope of any entry, in
// declaration order.
func (r *Resolution) ScopedRoles() []string {
	scoped := map[string]bool{}
	for _, role := range r.Config.Scope.Default {
		scoped[role] = true
	}
	for _, roles := range r.Config.Scope.RoleScope {
		for _, role := range roles {
			scoped[role] = true
		}
	}
	for _, roles := range interactionScope(r.Interactions) {
		for _, role := range roles {
			scoped[role] = true
		}
	}
	return slices.Filter(r.Suite.Roles, func(role string) bool { return scoped[role] })
}

// featureIDErrors checks that feature IDs and aliases are globally unique, that no alias names a
// live feature, and that the impl-specific ID prefix and profile agree. Aliases keep references to a
// renamed feature (release notes, commit trailers, declared manifests) resolvable, so an alias must
// point to exactly one feature.
func featureIDErrors(features []Feature) []string {
	var errs []string
	ids := map[string]string{}
	for _, f := range features {
		if prev, ok := ids[f.ID]; ok {
			errs = append(errs, fmt.Sprintf("%s: duplicate feature ID %q (also at %s)", f.Pos, f.ID, prev))
		}
		ids[f.ID] = f.Pos
		if strings.HasPrefix(f.ID, ImplSpecificPrefix) != (f.Profile == ProfileImplSpecific) {
			errs = append(errs, fmt.Sprintf("%s: feature %q has Profile %q: features with the %q ID prefix, and only those, "+
				"must set Profile: ProfileImplSpecific", f.Pos, f.ID, f.Profile, ImplSpecificPrefix))
		}
	}
	aliases := map[string]string{}
	for _, f := range features {
		for _, a := range f.Aliases {
			if _, ok := ids[a]; ok {
				errs = append(errs, fmt.Sprintf("%s: alias %q of feature %q is a live feature ID", f.Pos, a, f.ID))
			}
			if prev, ok := aliases[a]; ok {
				errs = append(errs, fmt.Sprintf("%s: alias %q of feature %q is also an alias of %q", f.Pos, a, f.ID, prev))
			}
			aliases[a] = f.ID
		}
	}
	return errs
}

// interactionIDPattern keeps interaction IDs usable in Covers and surface names.
var interactionIDPattern = regexp.MustCompile(`^[a-z0-9]+([.-][a-z0-9]+)*$`)

// checkInteractions validates interactions.yaml and returns, per covered interaction surface entry,
// the live feature IDs its coveredBy names (aliases resolved).
func checkInteractions(interactions []Interaction, features []Feature, knownRoles map[string]bool) ([]string, map[string][]string) {
	var errs []string
	ids := map[string]string{}
	for _, f := range features {
		ids[f.ID] = f.ID
		for _, a := range f.Aliases {
			ids[a] = f.ID
		}
	}
	seen := map[string]bool{}
	coveredBy := map[string][]string{}
	for _, i := range interactions {
		src := fmt.Sprintf("%s: interaction %q", InteractionsFile, i.ID)
		if !interactionIDPattern.MatchString(i.ID) {
			errs = append(errs, fmt.Sprintf("%s: invalid id (want lowercase words separated by '.' or '-')", src))
		}
		if seen[i.ID] {
			errs = append(errs, fmt.Sprintf("%s: duplicate id", src))
		}
		seen[i.ID] = true
		if strings.TrimSpace(i.Description) == "" {
			errs = append(errs, fmt.Sprintf("%s: needs a description", src))
		}
		if len(i.Roles) == 0 {
			errs = append(errs, fmt.Sprintf("%s: lists no roles", src))
		}
		for _, r := range i.Roles {
			if !knownRoles[r] {
				errs = append(errs, fmt.Sprintf("%s: unknown role %q", src, r))
			}
		}
		if err := checkSource(i.Source); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", src, err))
		}
		for _, ref := range i.CoveredBy {
			id, ok := ids[ref]
			if !ok {
				errs = append(errs, fmt.Sprintf("%s: coveredBy names unknown feature %q", src, ref))
				continue
			}
			coveredBy["interaction:"+i.ID] = append(coveredBy["interaction:"+i.ID], id)
		}
	}
	return errs, coveredBy
}

type prefixSet []string

// prefixes returns the prefix exemptions: entries ending in "*", e.g. a subtree ("Kind.path.*") or
// an annotation family ("annotation:readiness.status.sidecar.istio.io/*").
func (e Exemptions) prefixes() prefixSet {
	var out prefixSet
	for k := range e.Exempt {
		if strings.HasSuffix(k, "*") {
			out = append(out, strings.TrimSuffix(k, "*"))
		}
	}
	// Longest first, so ExemptBy names the narrowest exemption.
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) || len(out[i]) == len(out[j]) && out[i] < out[j] })
	return out
}

func (p prefixSet) match(s string) string {
	for _, pre := range p {
		if strings.HasPrefix(s, pre) || s == strings.TrimSuffix(pre, ".") {
			return pre
		}
	}
	return ""
}

const surfaceHeader = `# Generated by TestCoverage from SurfaceSources() in surface.go. DO NOT EDIT.
# Regenerate: go test ./tests/conformance/proxy/coverage/ -run TestCoverage -update
#
# The API surface the suite must cover, one entry per field, enum value, annotation, label, flag,
# Gateway API extension or interaction, mapped to where it comes from:
#   proto                istio/api proto field or enum value, by reflection
#   annotation-registry  istio/api annotation registry
#   label-registry       istio/api label registry
#   feature-flags        pilot/pkg/features flag referenced by istiod code that generates proxy config
#   extension-registry   Istio extension of Gateway API (pilot/pkg/config/kube/gatewayext)
#   manual               listed by hand in interactions.yaml
# An entry found by several sources has the first of them in this list. This file changes only with
# the API; what covers each entry is in coverage.yaml.
`

const coverageHeader = `# Generated by TestCoverage from surface.yaml, the features (catalog.yaml) and
# exemptions.yaml/scope.yaml. DO NOT EDIT.
# Regenerate: go test ./tests/conformance/proxy/coverage/ -run TestCoverage -update
#
# Every surface.yaml entry, resolved against the features and the decisions:
#   {exempt: true}     exempt with a reason in exemptions.yaml
#   {exempt: <prefix>} exempt by the "*" exemption <prefix> in exemptions.yaml
#   status             covered (by a feature) or todo (the backlog, bounded by maxTodo in ratchets.yaml)
#   scope              the roles that must each cover the entry (scope.yaml)
#   coveredBy          per role, the features that cover the entry
#   gaps               roles in scope without a covering feature (bounded by maxRoleGaps in ratchets.yaml)
`

// RenderSurface renders surface.yaml: every entry and its source.
func (r *Resolution) RenderSurface() ([]byte, error) {
	sources := make(map[string]string, len(r.Entries))
	for k, e := range r.Entries {
		sources[k] = e.Source
	}
	var buf bytes.Buffer
	buf.WriteString(surfaceHeader)
	if err := encodeYAML(&buf, sources); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderCoverage renders coverage.yaml: every entry's status, scope, covering features and gaps.
func (r *Resolution) RenderCoverage() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(coverageHeader)
	if err := encodeYAML(&buf, r.Entries); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// LoadCoverage reads the surface.yaml and coverage.yaml in dir into entries with their source. Missing
// files are an empty surface.
func LoadCoverage(dir string) (map[string]*Entry, error) {
	sources := map[string]string{}
	if err := loadYAML(filepath.Join(dir, SurfaceFile), &sources); err != nil {
		return nil, err
	}
	out := map[string]*Entry{}
	if err := loadYAML(filepath.Join(dir, CoverageFile), &out); err != nil {
		return nil, err
	}
	for k, e := range out {
		e.Source = sources[k]
	}
	return out, nil
}

// loadYAML decodes the file at path into v, leaving v alone if the file does not exist.
func loadYAML(path string, v any) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := yamlv3.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	return nil
}

// Summary is summary.yaml: the counts the README and the design doc refer to, generated so they
// cannot go stale.
type Summary struct {
	Surface      SurfaceSummary         `yaml:"surface"`
	Features     FeatureSummary         `yaml:"features"`
	Roles        map[string]RoleSummary `yaml:"roles"`
	Interactions InteractionSummary     `yaml:"interactions"`
	Ratchets     Ratchets               `yaml:"ratchets"`
}

// SurfaceSummary counts surface entries.
type SurfaceSummary struct {
	Total    int            `yaml:"total"`
	BySource map[string]int `yaml:"bySource"`
	ByStatus map[string]int `yaml:"byStatus"`
}

// FeatureSummary counts features.
type FeatureSummary struct {
	Total     int            `yaml:"total"`
	Security  int            `yaml:"security"`
	ByRole    map[string]int `yaml:"byRole"`
	ByProfile map[string]int `yaml:"byProfile"`
}

// RoleSummary is the coverage of one role: the entries in its scope, how many of them a feature of
// the role covers, and the rest (gaps).
type RoleSummary struct {
	Features int `yaml:"features"`
	InScope  int `yaml:"inScope"`
	Covered  int `yaml:"covered"`
	Gaps     int `yaml:"gaps"`
}

// InteractionSummary counts interactions.yaml entries.
type InteractionSummary struct {
	Total   int `yaml:"total"`
	Covered int `yaml:"covered"`
}

// Summary computes summary.yaml.
func (r *Resolution) Summary() Summary {
	s := Summary{
		Surface:  SurfaceSummary{Total: len(r.Entries), BySource: map[string]int{}, ByStatus: map[string]int{}},
		Features: FeatureSummary{Total: len(r.Suite.Features), ByRole: map[string]int{}, ByProfile: map[string]int{}},
		Roles:    map[string]RoleSummary{},
		Ratchets: r.Config.Ratchets,
	}
	for entry, e := range r.Entries {
		s.Surface.BySource[e.Source]++
		s.Surface.ByStatus[e.Status]++
		if strings.HasPrefix(entry, "interaction:") {
			s.Interactions.Total++
			if e.Status == EntryCovered {
				s.Interactions.Covered++
			}
		}
	}
	for _, f := range r.Suite.Features {
		s.Features.ByProfile[f.Profile]++
		if f.Security {
			s.Features.Security++
		}
		for _, role := range f.Roles {
			s.Features.ByRole[role]++
		}
	}
	for _, role := range r.ScopedRoles() {
		rs := RoleSummary{Features: s.Features.ByRole[role]}
		for _, e := range r.Entries {
			if slices.Contains(e.Scope, role) {
				rs.InScope++
				if slices.Contains(e.Gaps, role) {
					rs.Gaps++
				} else {
					rs.Covered++
				}
			}
		}
		s.Roles[role] = rs
	}
	return s
}

const summaryHeader = `# Generated by TestCoverage. DO NOT EDIT. The numbers the README and the design doc refer to.
# Regenerate: go test ./tests/conformance/proxy/coverage/ -run TestCoverage -update
#
#   surface       entries by source (surface.yaml) and status (coverage.yaml)
#   features      by role and profile; security: features held to the fail-open rule (IsSecurity)
#   roles         per scoped role: its features, the entries in its scope (scope.yaml), and how many of
#                 them a feature of the role covers; gaps = inScope - covered
#   interactions  interactions.yaml entries, and how many a feature covers
#   ratchets      ratchets.yaml
`

// RenderSummary renders summary.yaml.
func (r *Resolution) RenderSummary() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(summaryHeader)
	if err := encodeYAML(&buf, r.Summary()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeYAML(buf *bytes.Buffer, v any) error {
	enc := yamlv3.NewEncoder(buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return enc.Close()
}
