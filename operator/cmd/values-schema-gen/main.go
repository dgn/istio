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

// values-schema-gen generates JSON Schema files and nil-safe accessor methods
// from Go structs defined in the per-chart values packages and operator types.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"text/template"

	"github.com/google/jsonschema-go/jsonschema"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	opapis "istio.io/istio/operator/pkg/apis"
)

type chartDef struct {
	typ    reflect.Type
	output string
}

// enumValues maps Go enum types to their allowed string values.
// These are used to generate the "enum" field in the JSON schema.
var enumValues = map[reflect.Type][]any{
	reflect.TypeOf(opapis.ImagePullPolicy("")): {"", "Always", "IfNotPresent", "Never"},
	reflect.TypeOf(opapis.ResourceScope("")):   {"all", "cluster", "namespace"},

	reflect.TypeOf(opapis.CniValuesProvider("")): {"default", "multus"},

	reflect.TypeOf(opapis.GatewayValuesKind("")):                                          {"DaemonSet", "Deployment"},
	reflect.TypeOf(opapis.GatewayValuesServiceIpFamilyPolicy("")):                         {"", "SingleStack", "PreferDualStack", "RequireDualStack"},
	reflect.TypeOf(opapis.GatewayValuesPodDisruptionBudgetUnhealthyPodEvictionPolicy("")): {"", "AlwaysAllow", "IfHealthyBudget"},

	reflect.TypeOf(opapis.IngressValuesGatewaysIstioIngressgatewayIpFamiliesElem("")): {"IPv4", "IPv6"},
	reflect.TypeOf(opapis.IngressValuesGatewaysIstioIngressgatewayIpFamilyPolicy("")): {"", "SingleStack", "PreferDualStack", "RequireDualStack"},
}

func main() {
	charts := []chartDef{
		{reflect.TypeOf(opapis.BaseValues{}), "manifests/charts/base/values.schema.json"},
		{reflect.TypeOf(opapis.CniValues{}), "manifests/charts/istio-cni/values.schema.json"},
		{reflect.TypeOf(opapis.DefaultchartValues{}), "manifests/charts/default/values.schema.json"},
		{reflect.TypeOf(opapis.DiscoveryValues{}), "manifests/charts/istio-control/istio-discovery/values.schema.json"},
		{reflect.TypeOf(opapis.EgressValues{}), "manifests/charts/gateways/istio-egress/values.schema.json"},
		{reflect.TypeOf(opapis.GatewayValues{}), "manifests/charts/gateway/values.schema.json"},
		{reflect.TypeOf(opapis.IngressValues{}), "manifests/charts/gateways/istio-ingress/values.schema.json"},
		{reflect.TypeOf(opapis.ZtunnelValues{}), "manifests/charts/ztunnel/values.schema.json"},
	}

	// Build TypeSchemas from enumValues + IntOrString + time wrappers.
	typeSchemas := map[reflect.Type]*jsonschema.Schema{
		reflect.TypeOf(opapis.IntOrString{}): {
			Types: []string{"integer", "string"},
		},
		// metav1.Time and MicroTime embed time.Time, which the jsonschema library
		// maps to {Type: "string"}. The library rejects embedded fields with non-object
		// schemas, so we register these wrappers with the same string schema.
		reflect.TypeOf(metav1.Time{}):      {Type: "string"},
		reflect.TypeOf(metav1.MicroTime{}): {Type: "string"},
	}
	for typ, vals := range enumValues {
		typeSchemas[typ] = &jsonschema.Schema{
			Type: "string",
			Enum: vals,
		}
	}

	opts := &jsonschema.ForOptions{
		IgnoreInvalidTypes: true,
		TypeSchemas:        typeSchemas,
	}

	for _, c := range charts {
		if err := generateSchema(c, opts); err != nil {
			fmt.Fprintf(os.Stderr, "error generating schema for %s: %v\n", c.output, err)
			os.Exit(1)
		}
	}

	if err := generateAccessors(); err != nil {
		fmt.Fprintf(os.Stderr, "error generating accessors: %v\n", err)
		os.Exit(1)
	}
}

// --- JSON Schema generation ---

// schemaDoc is the structure we marshal to match the current schema format.
type schemaDoc struct {
	Schema   string                        `json:"$schema"`
	Defs     map[string]*jsonschema.Schema `json:"$defs"`
	Defaults *schemaRef                    `json:"defaults"`
	Ref      string                        `json:"$ref"`
}

type schemaRef struct {
	Ref string `json:"$ref"`
}

func generateSchema(c chartDef, opts *jsonschema.ForOptions) error {
	schema, err := jsonschema.ForType(c.typ, opts)
	if err != nil {
		return fmt.Errorf("ForType: %w", err)
	}

	// Clean up the schema to match the current format:
	// - Remove additionalProperties and required constraints
	// - Strip "null" from type unions (pointer types)
	// - Remove "items: true" for untyped arrays
	cleanSchema(schema)

	doc := schemaDoc{
		Schema: "http://json-schema.org/schema#",
		Defs: map[string]*jsonschema.Schema{
			"values": schema,
		},
		Defaults: &schemaRef{Ref: "#/$defs/values"},
		Ref:      "#/$defs/values",
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(c.output)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	if err := os.WriteFile(c.output, data, 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	fmt.Printf("Generated %s\n", c.output)
	return nil
}

// cleanSchema recursively cleans up a schema to match the existing format.
func cleanSchema(s *jsonschema.Schema) {
	if s == nil {
		return
	}

	// Remove additionalProperties constraints
	s.AdditionalProperties = nil

	// Remove required arrays
	s.Required = nil

	// Convert single-element Types to Type for cleaner output.
	// Keep multi-type arrays (including ["null", "type"]) as-is since pointer
	// types should allow null values.
	if len(s.Types) == 1 {
		s.Type = s.Types[0]
		s.Types = nil
	}

	// Remove "items: true" for untyped arrays ([]interface{}).
	// The library generates items={} (which marshals as true) for any-typed arrays.
	// The existing schemas either omit items or don't constrain them.
	if s.Items != nil && isEmptySchema(s.Items) {
		s.Items = nil
	}

	// Recurse into properties
	for _, prop := range s.Properties {
		cleanSchema(prop)
	}

	// Recurse into items
	if s.Items != nil {
		cleanSchema(s.Items)
	}

	// Recurse into defs
	for _, def := range s.Defs {
		cleanSchema(def)
	}

	// Recurse into allOf, anyOf, oneOf
	for _, sub := range s.AllOf {
		cleanSchema(sub)
	}
	for _, sub := range s.AnyOf {
		cleanSchema(sub)
	}
	for _, sub := range s.OneOf {
		cleanSchema(sub)
	}
}

// isEmptySchema checks if a schema has no meaningful constraints set.
// An empty schema {} is equivalent to "true" (allows anything).
func isEmptySchema(s *jsonschema.Schema) bool {
	if s == nil {
		return true
	}
	return s.Type == "" &&
		len(s.Types) == 0 &&
		len(s.Properties) == 0 &&
		s.Items == nil &&
		len(s.Enum) == 0 &&
		s.Ref == "" &&
		len(s.AllOf) == 0 &&
		len(s.AnyOf) == 0 &&
		len(s.OneOf) == 0 &&
		s.AdditionalProperties == nil &&
		len(s.Required) == 0
}

// --- Accessor generation ---

// accessorFileDef groups types by output file for accessor generation.
type accessorFileDef struct {
	output  string
	pkg     string
	pkgPath string
	types   []reflect.Type
}

type accessorInfo struct {
	RecvType string
	RecvVar  string
	Field    string
	Return   string
	Zero     string
	Deref    bool
}

type accessorFileData struct {
	Package   string
	Imports   []string
	Accessors []accessorInfo
}

var accessorTmpl = template.Must(template.New("accessors").Parse(`// Code generated by values-schema-gen; DO NOT EDIT.

package {{.Package}}
{{- if .Imports}}

import (
{{- range .Imports}}
	"{{.}}"
{{- end}}
)
{{- end}}
{{range .Accessors}}
func ({{.RecvVar}} *{{.RecvType}}) Get{{.Field}}() {{.Return}} {
{{- if .Deref}}
	if {{.RecvVar}} == nil || {{.RecvVar}}.{{.Field}} == nil {
		return {{.Zero}}
	}
	return *{{.RecvVar}}.{{.Field}}
{{- else}}
	if {{.RecvVar}} == nil {
		return {{.Zero}}
	}
	return {{.RecvVar}}.{{.Field}}
{{- end}}
}
{{end}}`))

func generateAccessors() error {
	files := []accessorFileDef{
		{
			output:  "operator/pkg/apis/values_helpers_gen.go",
			pkg:     "apis",
			pkgPath: reflect.TypeOf(opapis.Values{}).PkgPath(),
			types: []reflect.Type{
				// Global types
				reflect.TypeOf(opapis.ArchConfig{}),
				reflect.TypeOf(opapis.DefaultPodDisruptionBudgetConfig{}),
				reflect.TypeOf(opapis.DefaultResourcesConfig{}),
				reflect.TypeOf(opapis.GlobalLoggingConfig{}),
				reflect.TypeOf(opapis.GlobalValues{}),
				reflect.TypeOf(opapis.IstiodConfig{}),
				reflect.TypeOf(opapis.MultiClusterConfig{}),
				reflect.TypeOf(opapis.NetworkPolicyConfig{}),
				reflect.TypeOf(opapis.ProxyConfig{}),
				reflect.TypeOf(opapis.ProxyInitConfig{}),
				reflect.TypeOf(opapis.Resources{}),
				reflect.TypeOf(opapis.ResourcesRequestsConfig{}),
				reflect.TypeOf(opapis.SDSConfig{}),
				reflect.TypeOf(opapis.STSConfig{}),
				reflect.TypeOf(opapis.StartupProbe{}),
				reflect.TypeOf(opapis.WaypointConfig{}),
				// Operator-level types
				reflect.TypeOf(opapis.Values{}),
				reflect.TypeOf(opapis.BaseConfig{}),
				reflect.TypeOf(opapis.CNIUsageConfig{}),
				reflect.TypeOf(opapis.DiscoveryValues{}),
				reflect.TypeOf(opapis.EgressGatewayConfig{}),
				reflect.TypeOf(opapis.GatewaysConfig{}),
				reflect.TypeOf(opapis.IngressGatewayConfig{}),
				reflect.TypeOf(opapis.IstiodRemoteConfig{}),
				reflect.TypeOf(opapis.OutboundTrafficPolicyConfig{}),
				reflect.TypeOf(opapis.PilotIngressConfig{}),
				reflect.TypeOf(opapis.PilotPolicyConfig{}),
				reflect.TypeOf(opapis.PortsConfig{}),
				reflect.TypeOf(opapis.SecretVolume{}),
				reflect.TypeOf(opapis.ServiceAccount{}),
				reflect.TypeOf(opapis.SidecarInjectorConfig{}),
				reflect.TypeOf(opapis.TargetUtilizationConfig{}),
				reflect.TypeOf(opapis.TracerConfig{}),
				reflect.TypeOf(opapis.TracerDatadogConfig{}),
				reflect.TypeOf(opapis.TracerLightStepConfig{}),
				reflect.TypeOf(opapis.TracerStackdriverConfig{}),
				reflect.TypeOf(opapis.TracerZipkinConfig{}),
				reflect.TypeOf(opapis.ValuesExperimental{}),
				reflect.TypeOf(opapis.ValuesTelemetry{}),
				reflect.TypeOf(opapis.ValuesTelemetryV2{}),
				reflect.TypeOf(opapis.ValuesTelemetryV2Prometheus{}),
				reflect.TypeOf(opapis.ValuesTelemetryV2Stackdriver{}),
			},
		},
	}

	for _, f := range files {
		if err := generateAccessorsFile(f); err != nil {
			return fmt.Errorf("generating %s: %w", f.output, err)
		}
	}
	return nil
}

func generateAccessorsFile(fd accessorFileDef) error {
	imports := map[string]bool{}
	var accessors []accessorInfo

	for _, t := range fd.types {
		recvVar := strings.ToLower(t.Name()[:1])
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}

			ft := field.Type
			deref := false
			returnType := ft

			// If the field is a pointer to a scalar, deref it
			if ft.Kind() == reflect.Ptr {
				elem := ft.Elem()
				if isScalarKind(elem.Kind()) {
					deref = true
					returnType = elem
				}
			}

			returnTypeStr := goTypeName(returnType, fd.pkgPath, imports)
			zero := zeroExpr(returnType)

			accessors = append(accessors, accessorInfo{
				RecvType: t.Name(),
				RecvVar:  recvVar,
				Field:    field.Name,
				Return:   returnTypeStr,
				Zero:     zero,
				Deref:    deref,
			})
		}
	}

	var importPaths []string
	for p := range imports {
		importPaths = append(importPaths, p)
	}
	sort.Strings(importPaths)

	data := accessorFileData{
		Package:   fd.pkg,
		Imports:   importPaths,
		Accessors: accessors,
	}

	var buf bytes.Buffer
	if err := accessorTmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("template: %w", err)
	}

	src, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("gofmt %s: %w\n\n%s", fd.output, err, buf.String())
	}

	if err := os.WriteFile(fd.output, src, 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	fmt.Printf("Generated %s\n", fd.output)
	return nil
}

// goTypeName returns the Go source representation of a type, relative to targetPkgPath.
// Cross-package references are added to the imports map.
func goTypeName(t reflect.Type, targetPkgPath string, imports map[string]bool) string {
	switch t.Kind() {
	case reflect.Ptr:
		return "*" + goTypeName(t.Elem(), targetPkgPath, imports)
	case reflect.Slice:
		return "[]" + goTypeName(t.Elem(), targetPkgPath, imports)
	case reflect.Map:
		return "map[" + goTypeName(t.Key(), targetPkgPath, imports) + "]" + goTypeName(t.Elem(), targetPkgPath, imports)
	case reflect.Interface:
		return "any"
	default:
		pkgPath := t.PkgPath()
		if pkgPath == "" {
			return t.Name()
		}
		if pkgPath == targetPkgPath {
			return t.Name()
		}
		imports[pkgPath] = true
		return path.Base(pkgPath) + "." + t.Name()
	}
}

func isScalarKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.String:
		return true
	}
	return false
}

func zeroExpr(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Bool:
		return "false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "0"
	case reflect.String:
		return `""`
	default:
		return "nil"
	}
}
