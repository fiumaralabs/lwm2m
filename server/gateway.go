package server

import (
	"fmt"

	"github.com/fiumaralabs/lwm2m"
)

// objectGateway is the LwM2M Gateway object (Core E.12, GW §6). A client
// that registers it is a gateway, and its traffic may address end-device
// objects by prefix (GW §8.3). The registry of prefixes lives in the
// gateway package; the core only checks the client is a gateway.
const objectGateway = 25

func isGateway(reg *Registration) bool {
	_, ok := reg.Object(objectGateway)
	return ok
}

// checkEndDevice accepts a downlink on prefix: none, or a valid prefix
// (GW §8.3.1) of a gateway on LwM2M 1.1 or later (GW §8).
func checkEndDevice(reg *Registration, prefix string) error {
	switch {
	case prefix == "":
		return nil
	case !isGateway(reg):
		return fmt.Errorf("%w: prefix %q: %s registered no /25", ErrBadRequest, prefix, reg.Endpoint)
	case reg.Version == "1.0":
		return fmt.Errorf("%w: prefix %q: a gateway uses LwM2M 1.1 or later", ErrBadRequest, prefix)
	}
	if err := lwm2m.ValidPrefix(prefix); err != nil {
		return fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	return nil
}

// ownPrefix gives the nodes of a response to a request on prefix that
// prefix (a format without names, or a name without it) and refuses nodes
// of another end device.
func ownPrefix(nodes []lwm2m.Node, prefix string) error {
	if prefix == "" {
		return nil
	}
	for i := range nodes {
		switch nodes[i].Prefix {
		case "":
			nodes[i].Prefix = prefix
		case prefix:
		default:
			return fmt.Errorf("server: node %s in a response for prefix %q", nodes[i].PathString(), prefix)
		}
	}
	return nil
}
