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
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"

	"istio.io/istio/pkg/slices"
)

// SuiteDir is the proxy conformance suite, relative to the repository root.
const SuiteDir = "tests/conformance/proxy"

// Feature profiles (Feature.Profile in the suite). An unset profile means core. Impl-specific
// features must say so explicitly: their ID prefix does not imply it.
const (
	ProfileCore         = "core"
	ProfileExtended     = "extended"
	ProfileImplSpecific = "impl-specific"
	// ImplSpecificPrefix is the ID prefix of impl-specific features. A feature with it must declare
	// ProfileImplSpecific (and vice versa), so the two can't drift apart.
	ImplSpecificPrefix = "impl-specific."
)

// profileConsts maps the suite's Profile constants to their values, so features may use either.
var profileConsts = map[string]string{
	"ProfileCore":         ProfileCore,
	"ProfileExtended":     ProfileExtended,
	"ProfileImplSpecific": ProfileImplSpecific,
}

// securityKinds are the APIs whose silent failure is a vulnerability: a proxy that accepts one of
// them and does not enforce it fails open. securityInteractions are the interactions.yaml ID prefixes
// among them.
var (
	securityKinds        = []string{"AuthorizationPolicy", "PeerAuthentication", "RequestAuthentication"}
	securityInteractions = []string{"interaction:authz.", "interaction:peerauthn.", "interaction:requestauthn.", "interaction:mtls."}
)

// IsSecurity reports whether a silent failure of a feature is a vulnerability: its ID is in the
// security.* namespace, or it covers a security API (securityKinds, or an interaction among them).
// It is derived from Covers rather than the ID alone, so an authorization feature filed under
// another area (e.g. mesh.authz.*) is still held to the fail-open rule.
func IsSecurity(id string, covers []string) bool {
	if strings.HasPrefix(id, "security.") {
		return true
	}
	for _, c := range covers {
		for _, k := range securityKinds {
			if inScope(c, k) {
				return true
			}
		}
		for _, i := range securityInteractions {
			if strings.HasPrefix(c, i) {
				return true
			}
		}
	}
	return false
}

// Feature is a Feature literal passed to Register in the suite, read from source. The suite's files
// carry the integ build tag and cannot be imported, so this package parses them.
type Feature struct {
	ID          string
	Description string
	Roles       []string
	Profile     string
	RequiresL7  bool
	Covers      []string
	Aliases     []string
	NoControl   string
	// Cases are the names of the subcases the feature's Run declares with s.Case(t, "<name>", ...).
	Cases []string
	// MeshWide is true when the feature sets MeshConfig or IstiodEnv.
	MeshWide bool
	// Security is IsSecurity(ID, Covers).
	Security bool
	// Pos is the position of the literal (file:line:col); File, Line and EndLine locate it.
	Pos     string
	File    string
	Line    int
	EndLine int
}

// Suite is what this package reads from the suite's source.
type Suite struct {
	Features []Feature
	// Roles are the declared Role constants, in declaration order.
	Roles []string
	// Proxies are the implementation names per role, from the []Proxy tables.
	Proxies map[string][]string
}

// RoleFeatures returns the features that list the role.
func (s *Suite) RoleFeatures(role string) []Feature {
	return slices.Filter(s.Features, func(f Feature) bool { return slices.Contains(f.Roles, role) })
}

// ReadSuite parses the suite's *_test.go files in dir. Every problem is reported in the returned
// error; the suite is only usable when the error is nil.
func ReadSuite(dir string) (*Suite, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	p := &suiteParser{fset: token.NewFileSet()}
	for _, f := range files {
		af, err := parser.ParseFile(p.fset, f, nil, 0)
		if err != nil {
			return nil, err
		}
		p.files = append(p.files, af)
	}
	p.readRoleTable()
	s := &Suite{Roles: p.roles.constNames(), Proxies: p.readProxies()}
	s.Features = p.readFeatures()
	switch {
	case len(p.roles.consts) == 0:
		p.errs = append(p.errs, fmt.Errorf("%s: no Role constants found", dir))
	case len(s.Features) == 0:
		p.errs = append(p.errs, fmt.Errorf("%s: no features found", dir))
	case len(s.Proxies) == 0:
		p.errs = append(p.errs, fmt.Errorf("%s: no []Proxy tables found", dir))
	}
	return s, errors.Join(p.errs...)
}

// suiteParser reads the suite's source and accumulates errors instead of stopping at the first.
type suiteParser struct {
	fset  *token.FileSet
	files []*ast.File
	roles *roleTable
	errs  []error
}

func (p *suiteParser) errorf(pos token.Pos, format string, args ...any) {
	p.errs = append(p.errs, fmt.Errorf("%s: %s", p.fset.Position(pos), fmt.Sprintf(format, args...)))
}

// readFeatures extracts the Feature literals passed to Register.
func (p *suiteParser) readFeatures() []Feature {
	var out []Feature
	for _, af := range p.files {
		ast.Inspect(af, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if id, ok := cl.Type.(*ast.Ident); !ok || id.Name != "Feature" {
				return true
			}
			start, end := p.fset.Position(cl.Pos()), p.fset.Position(cl.End())
			df := Feature{Pos: start.String(), File: start.Filename, Line: start.Line, EndLine: end.Line}
			registered := false
			for _, e := range cl.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				switch kv.Key.(*ast.Ident).Name {
				case "ID":
					df.ID = p.stringLit(kv.Value)
				case "Run":
					// Only features passed to Register have Run. Scenario literals such as
					// Feature{ID: "shared"} are placeholders and are not part of the suite.
					registered = true
					df.Cases = caseNames(kv.Value)
				case "Roles":
					df.Roles = p.resolveRoles(kv.Value)
				case "Covers":
					df.Covers = p.stringList("Covers", kv.Value)
				case "Aliases":
					df.Aliases = p.stringList("Aliases", kv.Value)
				case "Description":
					df.Description = p.concatLit("Description", kv.Value)
				case "NoControl":
					df.NoControl = p.concatLit("NoControl", kv.Value)
				case "MeshConfig", "IstiodEnv":
					df.MeshWide = true
				case "RequiresL7":
					id, ok := kv.Value.(*ast.Ident)
					if !ok || (id.Name != "true" && id.Name != "false") {
						p.errorf(kv.Value.Pos(), "RequiresL7 must be true or false (it is read from source)")
					} else {
						df.RequiresL7 = id.Name == "true"
					}
				case "Profile":
					df.Profile = p.profileValue(kv.Value)
				}
			}
			if df.ID != "" && registered {
				if df.Profile == "" {
					df.Profile = ProfileCore
				}
				df.Security = IsSecurity(df.ID, df.Covers)
				out = append(out, df)
			}
			return true
		})
	}
	return out
}

// roleTable resolves the role expressions features use: the role constants
// (RoleIngress Role = "ingress") and package-level aliases (var ingressRole = []Role{RoleIngress}).
type roleTable struct {
	consts map[string]string
	vars   map[string][]string
	order  []string
}

func (p *suiteParser) readRoleTable() {
	rt := &roleTable{consts: map[string]string{}, vars: map[string][]string{}}
	// Constants first: vars such as ingressRole = []Role{RoleIngress} reference them, possibly from
	// a file parsed later.
	for _, af := range p.files {
		forEachValueSpec(af, func(vs *ast.ValueSpec, i int, name string) {
			if !isRoleType(vs.Type) {
				return
			}
			if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				rt.consts[name] = p.stringLit(lit)
				rt.order = append(rt.order, name)
			}
		})
	}
	for _, af := range p.files {
		forEachValueSpec(af, func(vs *ast.ValueSpec, i int, name string) {
			if vs.Type != nil {
				return
			}
			if rs, ok := rt.resolveExpr(vs.Values[i]); ok {
				rt.vars[name] = rs
			}
		})
	}
	p.roles = rt
}

// forEachValueSpec calls f for each name/value pair in a const or var declaration.
func forEachValueSpec(af *ast.File, f func(vs *ast.ValueSpec, i int, name string)) {
	for _, d := range af.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != len(vs.Names) {
				continue
			}
			for i, name := range vs.Names {
				f(vs, i, name.Name)
			}
		}
	}
}

func isRoleType(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "Role"
}

// resolveExpr reads a []Role composite literal or a role constant, without reporting errors. It is
// used while building the table, before vars are fully known.
func (rt *roleTable) resolveExpr(e ast.Expr) ([]string, bool) {
	switch v := e.(type) {
	case *ast.Ident:
		if r, ok := rt.consts[v.Name]; ok {
			return []string{r}, true
		}
		return rt.vars[v.Name], rt.vars[v.Name] != nil
	case *ast.CompositeLit:
		var out []string
		for _, el := range v.Elts {
			rs, ok := rt.resolveExpr(el)
			if !ok {
				return nil, false
			}
			out = append(out, rs...)
		}
		return out, len(out) > 0
	}
	return nil, false
}

// resolveRoles reads a Feature's Roles expression and reports unknown identifiers.
func (p *suiteParser) resolveRoles(e ast.Expr) []string {
	if rs, ok := p.roles.resolveExpr(e); ok {
		return rs
	}
	if v, ok := e.(*ast.Ident); ok {
		p.errorf(v.Pos(), "unknown role identifier %q (roles must use the Role constants)", v.Name)
	} else {
		p.errorf(e.Pos(), "Roles must be a []Role literal or a role alias")
	}
	return nil
}

// constNames returns the role names in declaration order.
func (rt *roleTable) constNames() []string {
	out := make([]string, 0, len(rt.order))
	for _, n := range rt.order {
		out = append(out, rt.consts[n])
	}
	return out
}

// readProxies parses the suite's proxy tables ([]Proxy literals in proxies_test.go) and returns the
// implementation names per role.
func (p *suiteParser) readProxies() map[string][]string {
	out := map[string][]string{}
	for _, af := range p.files {
		ast.Inspect(af, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			at, ok := cl.Type.(*ast.ArrayType)
			if !ok {
				return true
			}
			if id, ok := at.Elt.(*ast.Ident); !ok || id.Name != "Proxy" {
				return true
			}
			for _, el := range cl.Elts {
				pl, ok := el.(*ast.CompositeLit)
				if !ok {
					continue
				}
				var name, role string
				for _, e := range pl.Elts {
					kv, ok := e.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					switch kv.Key.(*ast.Ident).Name {
					case "Name":
						name = p.stringLit(kv.Value)
					case "Role":
						if id, ok := kv.Value.(*ast.Ident); ok {
							role = p.roles.consts[id.Name]
						}
					}
				}
				if name == "" || role == "" {
					p.errorf(pl.Pos(), "Proxy literal needs a string Name and a Role constant (it is read from source)")
					continue
				}
				if !slices.Contains(out[role], name) {
					out[role] = append(out[role], name)
				}
			}
			return false
		})
	}
	return out
}

func (p *suiteParser) stringLit(e ast.Expr) string {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		p.errorf(e.Pos(), "expected a string literal")
		return ""
	}
	v, err := strconv.Unquote(bl.Value)
	if err != nil {
		p.errorf(e.Pos(), "%v", err)
	}
	return v
}

// caseNames returns the literal names passed to s.Case(t, "<name>", fn) in a feature's Run, in
// source order. Case names are informational in the catalog, so a non-literal name is skipped rather
// than failing the check.
func caseNames(run ast.Expr) []string {
	var out []string
	seen := map[string]bool{}
	ast.Inspect(run, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 3 {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Case" {
			return true
		}
		if bl, ok := call.Args[1].(*ast.BasicLit); ok && bl.Kind == token.STRING {
			if v, err := strconv.Unquote(bl.Value); err == nil && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
		return true
	})
	return out
}

// stringList reads a []string literal of string literals.
func (p *suiteParser) stringList(field string, e ast.Expr) []string {
	list, ok := e.(*ast.CompositeLit)
	if !ok {
		p.errorf(e.Pos(), "%s must be a []string literal (it is read from source)", field)
		return nil
	}
	var out []string
	for _, c := range list.Elts {
		out = append(out, p.stringLit(c))
	}
	return out
}

// concatLit reads a string literal or a '+' concatenation of string literals, the forms used for
// long descriptions. Anything else (constants, fmt.Sprintf) can't be resolved from source and fails,
// because the catalog would silently lose the text.
func (p *suiteParser) concatLit(field string, e ast.Expr) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			return p.stringLit(v)
		}
	case *ast.BinaryExpr:
		if v.Op == token.ADD {
			return p.concatLit(field, v.X) + p.concatLit(field, v.Y)
		}
	case *ast.ParenExpr:
		return p.concatLit(field, v.X)
	}
	p.errorf(e.Pos(), "%s must be a string literal or a '+' concatenation of string literals (it is read from source for catalog.yaml)", field)
	return ""
}

// profileValue reads a Feature's Profile: one of the Profile constants or a string literal holding a
// valid profile.
func (p *suiteParser) profileValue(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		if v, ok := profileConsts[x.Name]; ok {
			return v
		}
		p.errorf(e.Pos(), "unknown Profile %q (use ProfileCore, ProfileExtended or ProfileImplSpecific)", x.Name)
	case *ast.BasicLit:
		v := p.stringLit(x)
		for _, c := range profileConsts {
			if v == c {
				return v
			}
		}
		p.errorf(e.Pos(), "invalid Profile %q (want core, extended or impl-specific)", v)
	default:
		p.errorf(e.Pos(), "Profile must be a Profile constant or a string literal (it is read from source)")
	}
	return ""
}
