package main

import (
	"context"
	"fmt"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/gateway"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/server"
)

// daemonGateway reads from the one tenant owned by the registration server.
type daemonGateway struct {
	server *server.Server
	tenant string
}

func (g daemonGateway) ReadGateway(ctx context.Context, tenant, endpoint string) ([]link.Link, []gateway.Device, error) {
	if tenant != g.tenant {
		return nil, nil, fmt.Errorf("registration belongs to another tenant")
	}
	reg, ok := g.server.Store().ByEndpoint(endpoint)
	if !ok {
		return nil, nil, server.ErrNotRegistered
	}
	links, err := link.Parse(reg.RawLinks)
	if err != nil {
		return nil, nil, err
	}
	if !reg.HasObject(gateway.ObjectGateway) {
		return links, nil, nil
	}
	read := func(object uint16) ([]lwm2m.Node, error) {
		res, err := g.server.Read(ctx, endpoint, lwm2m.NewPath(object), server.ReadOptions{})
		if err != nil {
			return nil, err
		}
		if !res.Success() {
			return nil, fmt.Errorf("gateway read /%d: %s", object, server.CodeString(res.Code))
		}
		if res.DecodeErr != nil {
			return nil, res.DecodeErr
		}
		return res.Nodes, nil
	}
	nodes, err := read(gateway.ObjectGateway)
	if err != nil {
		return nil, nil, err
	}
	if reg.HasObject(gateway.ObjectRouting) && gateway.Version(reg) == "1.0" {
		routing, err := read(gateway.ObjectRouting)
		if err != nil {
			return nil, nil, err
		}
		nodes = append(nodes, routing...)
	}
	registry, err := gateway.ParseDevices(gateway.Version(reg), nodes)
	if err != nil {
		return nil, nil, err
	}
	return links, registry.Devices, nil
}
