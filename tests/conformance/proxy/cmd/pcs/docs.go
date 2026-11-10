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

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"istio.io/api/annotation"
	"istio.io/api/label"
	"istio.io/istio/pilot/pkg/config/kube/gatewayext"
	_ "istio.io/istio/pilot/pkg/features" // registers the feature flags with pkg/env
	"istio.io/istio/pkg/env"
	"istio.io/istio/tests/conformance/proxy/coverage"
)

// docs looks up the documentation of surface entries in the sources they come from: the comments of
// the generated Go code for proto fields (the same text as the reference docs on istio.io), and the
// registries for flags, annotations, labels, Gateway API extensions and interactions.
type docs struct {
	byKey map[string]string
	pkgs  map[string]*goPkg
}

func newDocs() (*docs, error) {
	d := &docs{byKey: map[string]string{}, pkgs: map[string]*goPkg{}}
	for _, v := range env.VarDescriptions() {
		d.byKey["flag:"+v.Name] = strings.TrimSpace(v.Description)
	}
	for _, a := range annotation.AllResourceAnnotations() {
		d.byKey["annotation:"+a.Name] = strings.TrimSpace(a.Description)
	}
	for _, l := range label.AllResourceLabels() {
		d.byKey["label:"+l.Name] = strings.TrimSpace(l.Description)
	}
	// After the annotations: some extensions are annotations, and the registry says what they mean
	// for a Gateway.
	for _, e := range gatewayext.Registry() {
		d.byKey[e.Entry] = e.Description
	}
	interactions, err := coverage.Interactions()
	if err != nil {
		return nil, err
	}
	for _, i := range interactions {
		d.byKey["interaction:"+i.ID] = fmt.Sprintf("%s\nRoles: %s. Semantics: %s", i.Description, strings.Join(i.Roles, ", "), i.Source)
	}
	return d, nil
}

// of returns the documentation of a surface entry, or "" if there is none.
func (d *docs) of(key string) string {
	if f, ok := strings.CutPrefix(key, "flag:"); ok {
		// Env vars are documented by name, whichever component reads them; only those linked into
		// pcs are known.
		_, name, _ := strings.Cut(f, ":")
		key = "flag:" + name
	}
	if s, ok := d.byKey[key]; ok {
		return s
	}
	fd, ev, ok := coverage.Field(key)
	if !ok {
		return ""
	}
	doc := d.fieldDoc(fd)
	if ev != nil {
		if v := d.enumValueDoc(ev); v != "" {
			doc = strings.TrimSpace(doc + "\n\n" + string(ev.Name()) + ": " + v)
		}
	}
	return doc
}

// fieldDoc returns the comment of the Go struct field generated for fd.
func (d *docs) fieldDoc(fd protoreflect.FieldDescriptor) string {
	mt, err := protoregistry.GlobalTypes.FindMessageByName(fd.ContainingMessage().FullName())
	if err != nil {
		return ""
	}
	msg := mt.New()
	t := reflect.TypeOf(msg.Interface()).Elem()
	// A oneof member is generated as the only field of a wrapper type (e.g. StringMatch_Exact). Set
	// the field to find it.
	if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
		msg.Set(fd, msg.NewField(fd))
		v := reflect.ValueOf(msg.Interface()).Elem()
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).Tag.Get("protobuf_oneof") == string(od.Name()) {
				t = v.Field(i).Elem().Type().Elem()
				break
			}
		}
	}
	p := d.pkg(t.PkgPath())
	if p == nil {
		return ""
	}
	return p.fields[t.Name()][string(fd.Name())]
}

// enumValueDoc returns the comment of the Go constant generated for ev.
func (d *docs) enumValueDoc(ev protoreflect.EnumValueDescriptor) string {
	et, err := protoregistry.GlobalTypes.FindEnumByName(ev.Parent().FullName())
	if err != nil {
		return ""
	}
	t := reflect.TypeOf(et.New(0))
	p := d.pkg(t.PkgPath())
	if p == nil {
		return ""
	}
	// protoc-gen-go names the values of a top-level enum <Enum>_<VALUE>, and those of an enum nested
	// in a message <Message>_<VALUE>, where the enum's own Go type is <Message>_<Enum>.
	prefix := t.Name()
	if _, nested := ev.Parent().Parent().(protoreflect.MessageDescriptor); nested {
		prefix = strings.TrimSuffix(prefix, "_"+string(ev.Parent().Name()))
	}
	return p.enumValues[prefix+"_"+string(ev.Name())]
}

// goPkg holds the doc comments of a package of generated protobuf code.
type goPkg struct {
	// fields maps a struct type and a proto field name to the field's comment.
	fields map[string]map[string]string
	// enumValues maps the name of an enum value constant to its comment.
	enumValues map[string]string
}

// pkg parses the *.pb.go files of a Go package, or returns nil if they can't be found.
func (d *docs) pkg(path string) *goPkg {
	if p, ok := d.pkgs[path]; ok {
		return p
	}
	d.pkgs[path] = nil
	out, err := exec.Command("go", "list", "-f", "{{.Dir}}", path).Output()
	if err != nil {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(strings.TrimSpace(string(out)), "*.pb.go"))
	p := &goPkg{fields: map[string]map[string]string{}, enumValues: map[string]string{}}
	for _, f := range files {
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ParseComments)
		if err != nil {
			continue
		}
		p.add(af)
	}
	d.pkgs[path] = p
	return p
}

func (p *goPkg) add(af *ast.File) {
	for _, decl := range af.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				st, ok := s.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, f := range st.Fields.List {
					if name := protoName(f.Tag); name != "" {
						if p.fields[s.Name.Name] == nil {
							p.fields[s.Name.Name] = map[string]string{}
						}
						p.fields[s.Name.Name][name] = cleanDoc(f.Doc)
					}
				}
			case *ast.ValueSpec:
				// Enum values are typed constants, one per spec.
				if _, typed := s.Type.(*ast.Ident); gd.Tok != token.CONST || !typed || len(s.Names) != 1 {
					continue
				}
				doc := s.Doc
				if doc == nil && len(gd.Specs) == 1 {
					doc = gd.Doc
				}
				p.enumValues[s.Names[0].Name] = cleanDoc(doc)
			}
		}
	}
}

// protoName returns the proto field name from a struct tag like `protobuf:"bytes,1,opt,name=foo,..."`.
func protoName(tag *ast.BasicLit) string {
	if tag == nil {
		return ""
	}
	v := reflect.StructTag(strings.Trim(tag.Value, "`")).Get("protobuf")
	for _, part := range strings.Split(v, ",") {
		if name, ok := strings.CutPrefix(part, "name="); ok {
			return name
		}
	}
	return ""
}

// cleanDoc returns a comment's text without generator directives (+kubebuilder..., $hide_from_docs).
func cleanDoc(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(cg.Text(), "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "+") || strings.HasPrefix(t, "$") {
			continue
		}
		lines = append(lines, l)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
