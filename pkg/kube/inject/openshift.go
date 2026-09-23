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

// Functions below were copied from
// https://github.com/openshift/apiserver-library-go/blob/c22aa58bb57416b9f9f190957d07c9e7669c26df/pkg/securitycontextconstraints/sccmatching/matcher.go
// These functions are not exported, and, if they were, when imported bring k8s.io/kubernetes as dependency, which is problematic
// License is Apache 2.0: https://github.com/openshift/apiserver-library-go/blob/c22aa58bb57416b9f9f190957d07c9e7669c26df/LICENSE

package inject

import (
	"fmt"
	"strings"

	securityv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"istio.io/istio/pkg/kube"
	"istio.io/istio/pkg/kube/controllers"
	"istio.io/istio/pkg/kube/kclient"
	"istio.io/istio/pkg/log"
)

var sccGVR = schema.GroupVersionResource{
	Group:    "security.openshift.io",
	Version:  "v1",
	Resource: "securitycontextconstraints",
}

const sccAnnotation = "openshift.io/scc"

// getPreallocatedUIDRange retrieves the annotated value from the namespace, splits it to make
// the min/max and formats the data into the necessary types for the strategy options.
func getPreallocatedUIDRange(ns *corev1.Namespace) (*int64, *int64, error) {
	annotationVal, ok := ns.Annotations[securityv1.UIDRangeAnnotation]
	if !ok {
		return nil, nil, fmt.Errorf("unable to find annotation %s", securityv1.UIDRangeAnnotation)
	}
	if len(annotationVal) == 0 {
		return nil, nil, fmt.Errorf("found annotation %s but it was empty", securityv1.UIDRangeAnnotation)
	}
	uidBlock, err := ParseBlock(annotationVal)
	if err != nil {
		return nil, nil, err
	}

	minimum := int64(uidBlock.Start)
	maximum := int64(uidBlock.End)
	log.Debugf("got preallocated values for minimum: %d, maximum: %d for uid range in namespace %s", minimum, maximum, ns.Name)
	return &minimum, &maximum, nil
}

// getPreallocatedSupplementalGroups gets the annotated value from the namespace.
func getPreallocatedSupplementalGroups(ns *corev1.Namespace) ([]securityv1.IDRange, error) {
	groups, err := getSupplementalGroupsAnnotation(ns)
	if err != nil {
		return nil, err
	}
	log.Debugf("got preallocated value for groups: %s in namespace %s", groups, ns.Name)

	blocks, err := parseSupplementalGroupAnnotation(groups)
	if err != nil {
		return nil, err
	}

	idRanges := []securityv1.IDRange{}
	for _, block := range blocks {
		rng := securityv1.IDRange{
			Min: int64(block.Start),
			Max: int64(block.End),
		}
		idRanges = append(idRanges, rng)
	}
	return idRanges, nil
}

// getSupplementalGroupsAnnotation provides a backwards compatible way to get supplemental groups
// annotations from a namespace by looking for SupplementalGroupsAnnotation and falling back to
// UIDRangeAnnotation if it is not found.
func getSupplementalGroupsAnnotation(ns *corev1.Namespace) (string, error) {
	groups, ok := ns.Annotations[securityv1.SupplementalGroupsAnnotation]
	if !ok {
		log.Debugf("unable to find supplemental group annotation %s falling back to %s", securityv1.SupplementalGroupsAnnotation, securityv1.UIDRangeAnnotation)

		groups, ok = ns.Annotations[securityv1.UIDRangeAnnotation]
		if !ok {
			return "", fmt.Errorf("unable to find supplemental group or uid annotation for namespace %s", ns.Name)
		}
	}

	if len(groups) == 0 {
		return "", fmt.Errorf("unable to find groups using %s and %s annotations", securityv1.SupplementalGroupsAnnotation, securityv1.UIDRangeAnnotation)
	}
	return groups, nil
}

// parseSupplementalGroupAnnotation parses the group annotation into blocks.
func parseSupplementalGroupAnnotation(groups string) ([]Block, error) {
	blocks := []Block{}
	segments := strings.Split(groups, ",")
	for _, segment := range segments {
		block, err := ParseBlock(segment)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("no blocks parsed from annotation %s", groups)
	}
	return blocks, nil
}

type SCCClient struct {
	informer kclient.Untyped
}

func NewSCCClient(c kube.Client) *SCCClient {
	return &SCCClient{informer: kclient.NewDynamic(c, sccGVR, kclient.Filter{})}
}

func (s *SCCClient) Close()          { s.informer.ShutdownHandlers() }
func (s *SCCClient) HasSynced() bool { return s.informer.HasSynced() }

func (s *SCCClient) get(name string) (*securityv1.SecurityContextConstraints, bool) {
	obj := s.informer.Get(name, "")
	if controllers.IsNil(obj) {
		return nil, false
	}
	return toSCC(obj)
}

func toSCC(obj controllers.Object) (*securityv1.SecurityContextConstraints, bool) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, false
	}
	var scc securityv1.SecurityContextConstraints
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &scc); err != nil {
		log.Warnf("failed to convert SecurityContextConstraints %q: %v", u.GetName(), err)
		return nil, false
	}
	return &scc, true
}

// GetSCCProxyIDs resolves the proxy UID/GID from the SCC named in pod's sccAnnotation. If the
// namespace's preallocated range is a strict subset of what the SCC allows, the SCC agrees with
// the namespace and nil is returned so the caller falls back to the namespace-advertised value;
// otherwise the SCC's own range is used directly, since it isn't simply mirroring the namespace.
func GetSCCProxyIDs(sccs *SCCClient, ns *corev1.Namespace, pod *corev1.Pod) (uid, gid *int64) {
	if sccs == nil {
		return nil, nil
	}
	name := pod.Annotations[sccAnnotation]
	if name == "" {
		return nil, nil
	}
	scc, ok := sccs.get(name)
	if !ok {
		return nil, nil
	}

	// Only MustRunAsRange is handled: MustRunAs pins every container to one fixed UID, which would
	// collide with the app container instead of giving the proxy a distinct one.
	if sccMin, sccMax := scc.RunAsUser.UIDRangeMin, scc.RunAsUser.UIDRangeMax; scc.RunAsUser.Type == securityv1.RunAsUserStrategyMustRunAsRange {
		// If the SCC doesn't declare its own range, it defers to the namespace, so the
		// namespace's range is trivially a subset and there is nothing to override.
		isSubset := sccMin == nil || sccMax == nil
		if !isSubset && ns != nil {
			if nsMin, nsMax, err := getPreallocatedUIDRange(ns); err == nil {
				isSubset = *nsMin >= *sccMin && *nsMax <= *sccMax
			}
		}
		if !isSubset {
			uid = sccMax
		}
	}
	if scc.SupplementalGroups.Type == securityv1.SupplementalGroupsStrategyMustRunAs && len(scc.SupplementalGroups.Ranges) > 0 {
		sccRange := scc.SupplementalGroups.Ranges[0]
		isSubset := false
		if ns != nil {
			if nsGroups, err := getPreallocatedSupplementalGroups(ns); err == nil && len(nsGroups) > 0 {
				isSubset = nsGroups[0].Min >= sccRange.Min && nsGroups[0].Max <= sccRange.Max
			}
		}
		if !isSubset {
			maxGID := sccRange.Max
			gid = &maxGID
		}
	}
	return uid, gid
}

// Functions below were copied from
// https://github.com/openshift/library-go/blob/561433066966536ac17f3c9852d7d85f7b7e1e36/pkg/security/uid/uid.go
// Copied here to avoid bringing tons of dependencies
// License is Apache 2.0: https://github.com/openshift/library-go/blob/561433066966536ac17f3c9852d7d85f7b7e1e36/LICENSE

type Block struct {
	Start uint32
	End   uint32
}

var (
	ErrBlockSlashBadFormat = fmt.Errorf("block not in the format \"<start>/<size>\"")
	ErrBlockDashBadFormat  = fmt.Errorf("block not in the format \"<start>-<end>\"")
)

func ParseBlock(in string) (Block, error) {
	if strings.Contains(in, "/") {
		var start, size uint32
		n, err := fmt.Sscanf(in, "%d/%d", &start, &size)
		if err != nil {
			return Block{}, err
		}
		if n != 2 {
			return Block{}, ErrBlockSlashBadFormat
		}
		return Block{Start: start, End: start + size - 1}, nil
	}

	var start, end uint32
	n, err := fmt.Sscanf(in, "%d-%d", &start, &end)
	if err != nil {
		return Block{}, err
	}
	if n != 2 {
		return Block{}, ErrBlockDashBadFormat
	}
	return Block{Start: start, End: end}, nil
}

func (b Block) String() string {
	return fmt.Sprintf("%d/%d", b.Start, b.Size())
}

func (b Block) RangeString() string {
	return fmt.Sprintf("%d-%d", b.Start, b.End)
}

func (b Block) Size() uint32 {
	return b.End - b.Start + 1
}
