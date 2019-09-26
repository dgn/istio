//  Copyright 2018 Istio Authors
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package pilot

import (
	"fmt"
	"io"
	"net"

	"github.com/hashicorp/go-multierror"

	"istio.io/istio/pkg/test/framework/components/environment/kube"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/resource"
	testKube "istio.io/istio/pkg/test/kube"
)

const (
	pilotService = "istio-pilot"
	grpcPortName = "grpc-xds"
	httpPortName = "http"
)

var (
	_ Instance  = &kubeComponent{}
	_ io.Closer = &kubeComponent{}
)

func newKube(ctx resource.Context, _ Config) (Instance, error) {
	c := &kubeComponent{}
	c.id = ctx.TrackResource(c)

	env := ctx.Environment().(*kube.Environment)

	// TODO: This should be obtained from an Istio deployment.
	icfg, err := istio.DefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	ns := icfg.ConfigNamespace

	fetchFn := env.NewSinglePodFetch(ns, "istio=pilot")
	pods, err := env.WaitUntilPodsAreReady(fetchFn)
	if err != nil {
		return nil, err
	}
	pod := pods[0]

	ports, err := getPorts(env, ns)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			_ = c.Close()
		}
	}()

	// Start port-forwarding for pilot.
	c.grpcForwarder, err = env.NewPortForwarder(pod, 0, ports[grpcPortName])
	if err != nil {
		return nil, err
	}
	if err = c.grpcForwarder.Start(); err != nil {
		return nil, err
	}
	c.httpForwarder, err = env.NewPortForwarder(pod, 0, ports[httpPortName])
	if err != nil {
		return nil, err
	}
	if err = c.httpForwarder.Start(); err != nil {
		return nil, err
	}

	var grpcAddr, httpAddr *net.TCPAddr
	grpcAddr, err = net.ResolveTCPAddr("tcp", c.grpcForwarder.Address())
	if err != nil {
		return nil, err
	}
	httpAddr, err = net.ResolveTCPAddr("tcp", c.httpForwarder.Address())
	if err != nil {
		return nil, err
	}

	c.client, err = newClient(grpcAddr, httpAddr)
	if err != nil {
		return nil, err
	}

	return c, nil
}

type kubeComponent struct {
	id resource.ID

	*client

	grpcForwarder testKube.PortForwarder
	httpForwarder testKube.PortForwarder
}

func (c *kubeComponent) ID() resource.ID {
	return c.id
}

//func (c *kubeComponent) Start(ctx resource.Context) (err error) {
//
//
//}

// Close stops the kube pilot server.
func (c *kubeComponent) Close() (err error) {
	if c.client != nil {
		err = multierror.Append(err, c.client.Close()).ErrorOrNil()
		c.client = nil
	}

	if c.grpcForwarder != nil {
		err = multierror.Append(err, c.grpcForwarder.Close()).ErrorOrNil()
		c.grpcForwarder = nil
	}
	if c.httpForwarder != nil {
		err = multierror.Append(err, c.httpForwarder.Close()).ErrorOrNil()
		c.httpForwarder = nil
	}
	return
}

func getPorts(e *kube.Environment, ns string) (map[string]uint16, error) {
	svc, err := e.Accessor.GetService(ns, pilotService)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve service %s: %v", pilotService, err)
	}
	ret := make(map[string]uint16)
	for _, portInfo := range svc.Spec.Ports {
		ret[portInfo.Name] = uint16(portInfo.TargetPort.IntValue())
	}
	if len(ret) == 0 {
		return nil, fmt.Errorf("no ports defined in service %s", pilotService)
	}
	return ret, nil
}
