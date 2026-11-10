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

import "strings"

// Pseudo kinds for config a feature changes outside Kubernetes objects.
const (
	// KindMeshConfig is recorded for features that patch MeshConfig.
	KindMeshConfig = "MeshConfig"
	// KindIstiodEnv is recorded for features that set istiod environment variables (feature flags).
	KindIstiodEnv = "IstiodEnv"
)

// APIs a kind belongs to (see KindAPI).
const (
	APIGateway    = "gateway-api"
	APIIstio      = "istio"
	APIKubernetes = "kubernetes"
)

// QualifiedKind returns the kind of an object as Kind.group (e.g. HTTPRoute.gateway.networking.k8s.io),
// or Kind alone for the core group, so that kinds of the same name in different APIs (Gateway) differ.
func QualifiedKind(apiVersion, kind string) string {
	group, _, ok := strings.Cut(apiVersion, "/")
	if !ok || group == "" {
		return kind
	}
	return kind + "." + group
}

// KindAPI classifies a QualifiedKind (or pseudo kind) as APIGateway, APIIstio or APIKubernetes.
func KindAPI(qualified string) string {
	if qualified == KindMeshConfig || qualified == KindIstiodEnv {
		return APIIstio
	}
	_, group, _ := strings.Cut(qualified, ".")
	switch {
	case group == "istio.io" || strings.HasSuffix(group, ".istio.io"):
		return APIIstio
	case strings.HasSuffix(group, ".k8s.io") && (strings.HasPrefix(group, "gateway.networking.") || strings.HasPrefix(group, "inference.networking.")):
		return APIGateway
	default:
		return APIKubernetes
	}
}
