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
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/util/retry"
)

// Mesh-wide features: Feature.MeshConfig and Feature.IstiodEnv.
//
// Install-time settings (MeshConfig, istiod feature flags) affect every proxy, so a feature that
// changes them cannot share the mesh with other features. The runner gives such features their own
// phase per proxy:
//
//   - They are registered like every other feature, as parallel subtests named <proxy>/<feature-id>,
//     but each first waits at a serialGate until no parallel feature of the proxy is running, then
//     runs alone. Go starts parallel subtests only after the parent's function returns, and a
//     non-parallel subtest started from the parent would run *before* them, so a gate is the only
//     way to run after the parallel features while keeping the subtest names (a nested group subtest
//     would add a path element, and t.Run is not allowed from Cleanup).
//   - Among the waiting mesh-wide features, the gate prefers one with the istiod environment that is
//     currently applied, so istiod is restarted once per group of identical IstiodEnv.
//   - Each attempt (run, rerun, control) applies its MeshConfig patch and restores it when the
//     attempt ends; the istiod environment is switched only when the next attempt needs a different
//     one, and restored to the installed one when the proxy's features are done.
//   - In negative-control mode both count as feature config: they are not applied, and the scenario
//     records that something was stripped. Controls therefore never change mesh-wide state and keep
//     running in parallel.

// meshConfigSettle bounds how long istiod takes to reload a restored mesh config and push it, so the
// next features do not observe a stale patch. Features never rely on it: they assert a positive
// effect of their own patch.
const meshConfigSettle = 5 * time.Second

// gateSettle is how long no parallel feature must have been running before the gate lets a
// mesh-wide feature start: parallel subtests waiting for a slot start within milliseconds of one
// becoming free.
const gateSettle = 5 * time.Second

// serialGate orders a proxy's mesh-wide features after its parallel features and runs them one at a
// time. Parallel features hold the gate shared (enterParallel); mesh-wide features exclusively.
type serialGate struct {
	mu         sync.Mutex
	cond       *sync.Cond
	active     int       // parallel features running
	lastChange time.Time // last time a parallel feature started or ended
	running    bool      // a mesh-wide feature is running
	waiting    map[int]string
	nextTicket int
	currentEnv string
	stop       chan struct{}
}

func newSerialGate() *serialGate {
	g := &serialGate{lastChange: time.Now(), waiting: map[int]string{}, stop: make(chan struct{})}
	g.cond = sync.NewCond(&g.mu)
	// Wake waiters periodically: the settle condition depends on time.
	go func() {
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-g.stop:
				return
			case <-tick.C:
				g.cond.Broadcast()
			}
		}
	}()
	return g
}

func (g *serialGate) close() { close(g.stop) }

// enterParallel blocks while a mesh-wide feature runs (only possible when the mesh-wide features got
// all test slots first, see -test.parallel), then registers a running parallel feature.
func (g *serialGate) enterParallel() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.running {
		g.cond.Wait()
	}
	g.active++
	g.lastChange = time.Now()
}

func (g *serialGate) exitParallel() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	g.lastChange = time.Now()
	g.cond.Broadcast()
}

// enterSerial waits until no parallel feature has run for gateSettle and no other mesh-wide feature
// runs, and until this feature is the preferred one among the waiting (same istiod environment as the
// current one first, then by environment key and arrival).
func (g *serialGate) enterSerial(envKey string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ticket := g.nextTicket
	g.nextTicket++
	g.waiting[ticket] = envKey
	for g.running || g.active > 0 || time.Since(g.lastChange) < gateSettle || g.pick() != ticket {
		g.cond.Wait()
	}
	delete(g.waiting, ticket)
	g.running = true
	g.currentEnv = envKey
}

func (g *serialGate) exitSerial() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.running = false
	g.cond.Broadcast()
}

// pick returns the ticket that should run next.
func (g *serialGate) pick() int {
	best, bestKey := -1, ""
	for ticket, key := range g.waiting {
		better := best < 0 ||
			(key == g.currentEnv) != (bestKey == g.currentEnv) && key == g.currentEnv ||
			(key == g.currentEnv) == (bestKey == g.currentEnv) && (key < bestKey || key == bestKey && ticket < best)
		if better {
			best, bestKey = ticket, key
		}
	}
	return best
}

// envKey is a canonical string for an istiod environment, "" for none.
func envKey(env map[string]string) string {
	keys := slices.Sorted(maps.Keys(env))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+env[k])
	}
	return strings.Join(parts, ",")
}

// applyMeshWide applies the feature's MeshConfig and IstiodEnv for one attempt (t is the attempt's
// real test context: failing to apply is a harness error, not an observation). In control mode it
// only records that config was stripped, and makes sure istiod runs with its installed environment.
func (s *Scenario) applyMeshWide(t framework.TestContext) {
	t.Helper()
	f := s.Feature
	if s.control {
		if f.meshWide() {
			s.stripped = true
		}
		if len(f.IstiodEnv) > 0 {
			if err := istiod.ensureEnv(t, nil); err != nil {
				t.Fatalf("restoring istiod environment: %v", err)
			}
		}
		return
	}
	if len(f.IstiodEnv) > 0 {
		if err := istiod.ensureEnv(t, f.IstiodEnv); err != nil {
			t.Fatalf("setting istiod environment %v: %v", f.IstiodEnv, err)
		}
	} else if f.MeshConfig != "" {
		// A feature without its own istiod environment runs against the installed one.
		if err := istiod.ensureEnv(t, nil); err != nil {
			t.Fatalf("restoring istiod environment: %v", err)
		}
	}
	if f.MeshConfig != "" {
		// Cleanups run last-in first-out: the settle wait runs after the patch is restored.
		t.Cleanup(func() { time.Sleep(meshConfigSettle) })
		if err := inst.PatchMeshConfig(t, f.MeshConfig); err != nil {
			t.Fatalf("patching mesh config: %v", err)
		}
	}
}

// istiodEnv tracks the environment istiod currently runs with. istiod is shared by all tests, so
// there is one, guarded by a mutex; the serial gate already keeps mesh-wide features apart.
type istiodEnv struct {
	mu sync.Mutex
	// current is the applied feature environment (nil: as installed).
	current map[string]string
	// original is the discovery container's env as installed, captured on first change.
	original []corev1.EnvVar
	captured bool
}

var istiod = &istiodEnv{}

// istiodContainer is the istiod container that reads the feature flags.
const istiodContainer = "discovery"

// ensureEnv makes istiod run with the installed environment plus env (nil: as installed), restarting
// it and waiting for the rollout only when that changes the environment.
func (e *istiodEnv) ensureEnv(t framework.TestContext, env map[string]string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if envKey(e.current) == envKey(env) {
		return nil
	}
	ctx := context.Background()
	deps := t.Clusters().Default().Kube().AppsV1().Deployments(inst.Settings().SystemNamespace)
	d, err := deps.Get(ctx, istiodDeployment(t), metav1.GetOptions{})
	if err != nil {
		return err
	}
	idx := slices.IndexFunc(d.Spec.Template.Spec.Containers, func(c corev1.Container) bool { return c.Name == istiodContainer })
	if idx < 0 {
		return fmt.Errorf("istiod deployment %s has no %q container", d.Name, istiodContainer)
	}
	c := &d.Spec.Template.Spec.Containers[idx]
	if !e.captured {
		e.original = slices.Clone(c.Env)
		e.captured = true
	}
	next := slices.Clone(e.original)
	for _, k := range slices.Sorted(maps.Keys(env)) {
		next = slices.DeleteFunc(next, func(v corev1.EnvVar) bool { return v.Name == k })
		next = append(next, corev1.EnvVar{Name: k, Value: env[k]})
	}
	c.Env = next
	t.Logf("restarting istiod with feature environment %q", envKey(env))
	updated, err := deps.Update(ctx, d, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	if err := waitRollout(t, updated.Name, updated.Generation); err != nil {
		return err
	}
	e.current = maps.Clone(env)
	return nil
}

// istiodDeployment is the name of the istiod Deployment of the default revision.
func istiodDeployment(t framework.TestContext) string {
	if rev := t.Settings().Revisions.Default(); rev != "" && rev != "default" {
		return "istiod-" + rev
	}
	return "istiod"
}

// waitRollout waits until the istiod Deployment has rolled out the given generation and only pods
// of the new template are left.
func waitRollout(t framework.TestContext, name string, generation int64) error {
	ns := inst.Settings().SystemNamespace
	kc := t.Clusters().Default().Kube()
	return retry.UntilSuccess(func() error {
		d, err := kc.AppsV1().Deployments(ns).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		want := int32(1)
		if d.Spec.Replicas != nil {
			want = *d.Spec.Replicas
		}
		st := d.Status
		if st.ObservedGeneration < generation || st.UpdatedReplicas != want || st.AvailableReplicas != want || st.Replicas != want {
			return fmt.Errorf("istiod rollout in progress: %+v", st)
		}
		sel := metav1.FormatLabelSelector(d.Spec.Selector)
		pods, err := kc.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{LabelSelector: sel})
		if err != nil {
			return err
		}
		if int32(len(pods.Items)) != want {
			return fmt.Errorf("istiod has %d pods, want %d (old pods terminating)", len(pods.Items), want)
		}
		for _, p := range pods.Items {
			if p.DeletionTimestamp != nil || !podReady(p) {
				return fmt.Errorf("istiod pod %s not ready (%s)", p.Name, podStatus(p))
			}
		}
		return nil
	}, retry.Timeout(5*time.Minute), retry.Delay(2*time.Second))
}

// splitMeshWide separates features that run in parallel from mesh-wide ones, which are ordered by
// istiod environment so that each environment is applied once.
func splitMeshWide(features []Feature) (parallel, serial []Feature) {
	for _, f := range features {
		if f.meshWide() {
			serial = append(serial, f)
		} else {
			parallel = append(parallel, f)
		}
	}
	sort.SliceStable(serial, func(i, j int) bool { return envKey(serial[i].IstiodEnv) < envKey(serial[j].IstiodEnv) })
	return parallel, serial
}
