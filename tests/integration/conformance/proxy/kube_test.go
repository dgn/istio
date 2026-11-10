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
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/util/retry"
)

// Kubernetes-level observations of a gateway. These are implementation-neutral: they rely only on
// the standard gateway.networking.k8s.io/gateway-name label, never on resource names (which differ
// between implementations).

// podReady reports whether the pod has Ready=True.
func podReady(p corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// podStatus summarizes why a pod is not ready, so a timeout says what the proxy was doing rather
// than only that it was not ready.
func podStatus(p corev1.Pod) string {
	if podReady(p) {
		return "ready"
	}
	var parts []string
	for _, cs := range p.Status.ContainerStatuses {
		switch {
		case cs.State.Waiting != nil:
			msg := cs.State.Waiting.Message
			if len(msg) > 200 {
				msg = msg[:200] + "..."
			}
			parts = append(parts, fmt.Sprintf("%s waiting: %s: %s", cs.Name, cs.State.Waiting.Reason, msg))
		case cs.State.Terminated != nil:
			parts = append(parts, fmt.Sprintf("%s terminated: %s: %s", cs.Name, cs.State.Terminated.Reason, cs.State.Terminated.Message))
		default:
			parts = append(parts, fmt.Sprintf("%s running, restarts %d", cs.Name, cs.RestartCount))
		}
	}
	if len(parts) == 0 {
		return string(p.Status.Phase)
	}
	return fmt.Sprintf("%s: %s", p.Status.Phase, strings.Join(parts, "; "))
}

// Pods returns the gateway's pods.
func (g *Gateway) Pods(t framework.TestContext) []corev1.Pod {
	t.Helper()
	pods, err := t.Clusters().Default().Kube().CoreV1().Pods(g.Namespace).
		List(context.Background(), metav1.ListOptions{LabelSelector: g.Selector()})
	if err != nil {
		t.Fatal(err)
	}
	return pods.Items
}

// ServiceAccount returns the service account the gateway pods run as.
func (g *Gateway) ServiceAccount(t framework.TestContext) string {
	t.Helper()
	var sa string
	retry.UntilSuccessOrFail(t, func() error {
		pods := g.Pods(t)
		if len(pods) == 0 {
			return fmt.Errorf("no pods for gateway %s", g.Name)
		}
		sa = pods[0].Spec.ServiceAccountName
		return nil
	}, retry.Timeout(time.Minute))
	return sa
}

// Service returns the gateway's generated Service.
func (g *Gateway) Service(t framework.TestContext) (*corev1.Service, error) {
	svcs, err := t.Clusters().Default().Kube().CoreV1().Services(g.Namespace).
		List(context.Background(), metav1.ListOptions{LabelSelector: g.Selector()})
	if err != nil {
		return nil, err
	}
	if len(svcs.Items) != 1 {
		return nil, fmt.Errorf("expected 1 service for gateway %s, got %d", g.Name, len(svcs.Items))
	}
	return &svcs.Items[0], nil
}

// Deployment returns the gateway's generated Deployment.
func (g *Gateway) Deployment(t framework.TestContext) (*appsv1.Deployment, error) {
	ds, err := t.Clusters().Default().Kube().AppsV1().Deployments(g.Namespace).
		List(context.Background(), metav1.ListOptions{LabelSelector: g.Selector()})
	if err != nil {
		return nil, err
	}
	if len(ds.Items) != 1 {
		return nil, fmt.Errorf("expected 1 deployment for gateway %s, got %d", g.Name, len(ds.Items))
	}
	return &ds.Items[0], nil
}

// Logs returns the concatenated logs of all gateway pods.
func (g *Gateway) Logs(t framework.TestContext) (string, error) {
	var sb strings.Builder
	for _, p := range g.Pods(t) {
		for _, c := range p.Spec.Containers {
			b, err := t.Clusters().Default().Kube().CoreV1().Pods(g.Namespace).
				GetLogs(p.Name, &corev1.PodLogOptions{Container: c.Name}).Do(context.Background()).Raw()
			if err != nil {
				return "", err
			}
			sb.Write(b)
		}
	}
	return sb.String(), nil
}

// EventuallyKube retries f until it succeeds (for asynchronous controller behavior).
func EventuallyKube(t framework.TestContext, f func() error) {
	t.Helper()
	retry.UntilSuccessOrFail(t, f, retry.Timeout(2*time.Minute), retry.Delay(time.Second))
}

// expectRouteCondition waits until a route in the scenario namespace has a parent status condition
// with the given type, status and (optional) reason.
func expectRouteCondition(t framework.TestContext, s *Scenario, kind, name, typ, status, reason string) {
	t.Helper()
	EventuallyKube(t, func() error {
		var parents [][]metav1.Condition
		gw := t.Clusters().Default().GatewayAPI().GatewayV1()
		switch kind {
		case "HTTPRoute":
			r, err := gw.HTTPRoutes(s.Namespace()).Get(context.Background(), name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			for _, p := range r.Status.Parents {
				parents = append(parents, p.Conditions)
			}
		case "GRPCRoute":
			r, err := gw.GRPCRoutes(s.Namespace()).Get(context.Background(), name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			for _, p := range r.Status.Parents {
				parents = append(parents, p.Conditions)
			}
		default:
			return fmt.Errorf("unsupported route kind %s", kind)
		}
		return hasCondition(parents, typ, status, reason)
	})
}

// expectGatewayCondition waits until the Gateway has the condition.
func expectGatewayCondition(t framework.TestContext, g *Gateway, typ, status, reason string) {
	t.Helper()
	EventuallyKube(t, func() error {
		obj, err := g.Get(t)
		if err != nil {
			return err
		}
		return hasCondition([][]metav1.Condition{obj.Status.Conditions}, typ, status, reason)
	})
}

func hasCondition(sets [][]metav1.Condition, typ, status, reason string) error {
	for _, conds := range sets {
		for _, c := range conds {
			if c.Type == typ && string(c.Status) == status && (reason == "" || c.Reason == reason) {
				return nil
			}
		}
	}
	return fmt.Errorf("condition %s=%s (reason %q) not found in %+v", typ, status, reason, sets)
}

// PodIP returns the IP of the first gateway pod (once running).
func (g *Gateway) PodIP(t framework.TestContext) string {
	t.Helper()
	var ip string
	retry.UntilSuccessOrFail(t, func() error {
		for _, p := range g.Pods(t) {
			if p.Status.PodIP != "" && p.Status.Phase == corev1.PodRunning {
				ip = p.Status.PodIP
				return nil
			}
		}
		return fmt.Errorf("no running pod for gateway %s", g.Name)
	}, retry.Timeout(2*time.Minute))
	return ip
}

// GrantSecretRead gives the gateway's service account read access to Secrets in its namespace. Istio
// only serves credentialName secrets referenced from Istio APIs (Istio Gateway servers, DestinationRule
// client certs) to proxies allowed to read them; Gateway-API auto-deployed gateways get no such Role
// by default (Gateway API certificateRefs are authorized via the Gateway instead).
func (g *Gateway) GrantSecretRead(t framework.TestContext) {
	t.Helper()
	sa := g.ServiceAccount(t)
	t.ConfigKube().Eval(g.Namespace, map[string]any{"name": g.Name + "-secret-reader", "sa": sa, "ns": g.Namespace}, `
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{.name}}
rules:
- apiGroups: [""]
  resources: [secrets]
  verbs: [get, watch, list]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{.name}}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: {{.name}}}
subjects:
- {kind: ServiceAccount, name: {{.sa}}, namespace: {{.ns}}}
`).ApplyOrFail(t)
}
