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

package defaults

import (
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/anypb"
)

// The walker turns generated xDS into value-free paths. The rules exist to make the inventory a
// statement about *which* behaviors istiod configures, independent of the scenario's names and
// values, so that it only changes when istiod starts or stops setting something:
//
//   - Only set fields are visited (protoreflect Range), i.e. what istiod sets explicitly. Envoy's
//     own defaults for unset fields are the proxy's business and out of scope.
//   - Values are dropped; a path ends at a scalar, an enum, a repeated scalar, or an empty message.
//   - List indexes are dropped, so N listeners/clusters/routes collapse into one subtree.
//   - Extension elements (a message with a string `name` and a `typed_config`, e.g. network and
//     HTTP filters, listener filters, transport sockets, access loggers) are keyed by their name,
//     and their typed_config is unpacked in place: `filters[envoy.filters.network.http_connection_manager]/use_remote_address`.
//   - Other Any fields are unpacked via the protobuf registry and keyed by type: `field[@Type]`.
//     Every type istiod emits is linked into istiod, so it resolves; an unknown one stays a leaf.
//   - Map keys are dropped (`field[*]`), except for maps of Any, whose keys are extension names
//     (typed_per_filter_config, typed_extension_protocol_options). Other map keys are service or
//     workload names (ztunnel Workload.services) or metadata namespaces.
//   - Well-known value types (Duration, wrappers, Struct, ...) are leaves.

// leafTypes are messages treated as values: their inner structure says nothing about which
// behavior is configured.
var leafTypes = map[protoreflect.FullName]bool{
	"google.protobuf.Duration":        true,
	"google.protobuf.Timestamp":       true,
	"google.protobuf.Struct":          true,
	"google.protobuf.Value":           true,
	"google.protobuf.ListValue":       true,
	"google.protobuf.BoolValue":       true,
	"google.protobuf.UInt32Value":     true,
	"google.protobuf.UInt64Value":     true,
	"google.protobuf.Int32Value":      true,
	"google.protobuf.Int64Value":      true,
	"google.protobuf.DoubleValue":     true,
	"google.protobuf.FloatValue":      true,
	"google.protobuf.StringValue":     true,
	"google.protobuf.BytesValue":      true,
	"envoy.type.v3.Percent":           true,
	"envoy.type.v3.FractionalPercent": true,
}

// typedStructTypes wrap an untyped config (e.g. a Wasm or Istio extension); keyed by type URL.
var typedStructTypes = map[protoreflect.FullName]bool{
	"udpa.type.v1.TypedStruct": true,
	"xds.type.v3.TypedStruct":  true,
}

const anyFullName = protoreflect.FullName("google.protobuf.Any")

type walker struct {
	paths map[string]bool
}

// inventory returns the sorted, de-duplicated paths of all set fields of msgs, prefixed by prefix.
func inventory(prefix string, msgs []proto.Message) []string {
	w := &walker{paths: map[string]bool{}}
	for _, m := range msgs {
		w.message(prefix, m.ProtoReflect())
	}
	out := make([]string, 0, len(w.paths))
	for p := range w.paths {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (w *walker) emit(p string) { w.paths[p] = true }

// message visits m at path p.
func (w *walker) message(p string, m protoreflect.Message) {
	fn := m.Descriptor().FullName()
	switch {
	case leafTypes[fn]:
		w.emit(p)
		return
	case typedStructTypes[fn]:
		url := m.Get(m.Descriptor().Fields().ByName("type_url")).String()
		w.emit(p + "[" + typeName(url) + "]")
		return
	case fn == anyFullName:
		inner, key := unpack(m)
		if inner == nil {
			w.emit(p + "[" + key + "]")
			return
		}
		w.message(p+"[@"+key+"]", inner)
		return
	}
	keyed := extensionName(m) != ""
	n := 0
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		name := string(fd.Name())
		if keyed && name == "name" {
			return true // it is the element's key
		}
		n++
		child := p + "/" + name
		switch {
		case fd.IsMap():
			w.mapField(child, fd, v.Map())
		case fd.IsList():
			if fd.Kind() != protoreflect.MessageKind && fd.Kind() != protoreflect.GroupKind {
				w.emit(child)
				return true
			}
			l := v.List()
			for i := 0; i < l.Len(); i++ {
				w.element(child, l.Get(i).Message())
			}
		case fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind:
			if keyed && name == "typed_config" && fd.Message().FullName() == anyFullName {
				// An extension's config belongs to the extension, already named in the path.
				w.anyInPlace(p, v.Message())
				return true
			}
			w.element(child, v.Message())
		default:
			w.emit(child)
		}
		return true
	})
	if n == 0 {
		w.emit(p) // an empty message is a set field too (e.g. http2_protocol_options {})
	}
}

// element visits a list element or singular message field at path p, keyed if it is a named
// extension (e.g. transport_socket[envoy.transport_sockets.tls]).
func (w *walker) element(p string, m protoreflect.Message) {
	if n := extensionName(m); n != "" {
		w.message(p+"["+n+"]", m)
		return
	}
	w.message(p, m)
}

func (w *walker) mapField(p string, fd protoreflect.FieldDescriptor, m protoreflect.Map) {
	vd := fd.MapValue()
	isMsg := vd.Kind() == protoreflect.MessageKind
	if !isMsg {
		w.emit(p)
		return
	}
	if vd.Message().FullName() != anyFullName {
		m.Range(func(_ protoreflect.MapKey, v protoreflect.Value) bool {
			w.message(p+"[*]", v.Message())
			return true
		})
		return
	}
	m.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
		w.anyInPlace(p+"["+k.String()+"]", v.Message())
		return true
	})
}

// anyInPlace visits the content of an Any at p, without a type segment, because p already
// identifies the extension (by filter name or map key).
func (w *walker) anyInPlace(p string, m protoreflect.Message) {
	inner, key := unpack(m)
	if inner == nil {
		w.emit(p + "[" + key + "]")
		return
	}
	w.message(p, inner)
}

// unpack returns the message in an Any and its type name, or nil and the type URL's name if the
// type is not registered.
func unpack(m protoreflect.Message) (protoreflect.Message, string) {
	a, ok := m.Interface().(*anypb.Any)
	if !ok {
		a = &anypb.Any{}
		proto.Merge(a, m.Interface())
	}
	name := typeName(a.GetTypeUrl())
	mt, err := protoregistry.GlobalTypes.FindMessageByURL(a.GetTypeUrl())
	if err != nil {
		return nil, "unresolved:" + name
	}
	inner := mt.New()
	if err := proto.Unmarshal(a.GetValue(), inner.Interface()); err != nil {
		return nil, "unparsable:" + name
	}
	if typedStructTypes[inner.Descriptor().FullName()] {
		// A TypedStruct is keyed by its inner type, and its fields are opaque.
		url := inner.Get(inner.Descriptor().Fields().ByName("type_url")).String()
		return nil, typeName(url)
	}
	return inner, string(inner.Descriptor().Name())
}

// extensionName returns the name of an extension element (a message with a string `name` and a
// `typed_config` Any or a `config_type` oneof), or "".
func extensionName(m protoreflect.Message) string {
	fields := m.Descriptor().Fields()
	nf := fields.ByName("name")
	if nf == nil || nf.Kind() != protoreflect.StringKind || nf.IsList() {
		return ""
	}
	tc := fields.ByName("typed_config")
	if tc == nil && m.Descriptor().Oneofs().ByName("config_type") == nil {
		return ""
	}
	// Istio names some extensions after a filter chain or cluster ("inbound-vip|80|http|<host>");
	// those are keyed by type, so the inventory does not depend on the scenario's services.
	if n := m.Get(nf).String(); n != "" && !strings.ContainsAny(n, "|~") && !strings.Contains(n, ".svc.") {
		return n
	}
	if tc != nil && m.Has(tc) {
		_, key := unpack(m.Get(tc).Message())
		return "@" + key
	}
	return ""
}

// typeName is the last segment of a type URL: type.googleapis.com/a.b.C -> a.b.C.
func typeName(url string) string {
	return url[strings.LastIndex(url, "/")+1:]
}
