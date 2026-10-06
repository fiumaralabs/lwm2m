package gateway

import (
	"context"
	"errors"
	"fmt"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Exchange sends one request to the gateway and returns its response: a
// server.Peer's Exchange method, until the core exposes a queued raw
// request per endpoint.
type Exchange func(ctx context.Context, req *server.Message) (*server.Message, error)

// Client issues Device Management and Information Reporting operations to
// a gateway, on its own objects or, with a prefix, on end-device objects
// (GW §8.3).
type Client struct {
	Exchange Exchange
	Version  string // the gateway's LwM2M version: 1.1 or later (GW §8)
	RootPath string // the gateway's alternate path, "" for none
	// Devices, if set, is the known registry: a prefix outside it is
	// refused before anything is sent.
	Devices *Registry
	Schema  lwm2m.Schema // types untyped values; may be nil
}

// Response is a decoded gateway response.
type Response struct {
	Code    codes.Code
	Format  *lwm2m.ContentFormat
	Payload []byte
	Nodes   []Node
	Links   []link.Entry // Discover
	Token   []byte       // Observe: the observation token
}

var ErrVersion = errors.New("gateway: the gateway must use LwM2M 1.1 or later")

func (c *Client) check(paths ...Path) error {
	if c.Version == "1.0" || c.Version == "" {
		return ErrVersion
	}
	for _, p := range paths {
		if p.Prefix == "" {
			continue
		}
		if p.IsRoot() {
			return fmt.Errorf("%w: %s needs an object ID", ErrPath, p)
		}
		if c.Devices != nil {
			if _, ok := c.Devices.ByPrefix(p.Prefix); !ok {
				return fmt.Errorf("%w: %q", ErrUnknownPrefix, p.Prefix)
			}
		}
	}
	return nil
}

func cfPtr(f lwm2m.ContentFormat) *lwm2m.ContentFormat { return &f }

// defaultFormat is LwM2M CBOR for 1.2 gateways, SenML CBOR for 1.1: both
// carry prefixes in names.
func (c *Client) defaultFormat() lwm2m.ContentFormat {
	if c.Version == "1.1" {
		return lwm2m.FormatSenMLCBOR
	}
	return lwm2m.FormatLwM2MCBOR
}

func (c *Client) do(ctx context.Context, base Path, req *server.Message) (*Response, error) {
	if req.Token == nil {
		tok, err := message.GetToken()
		if err != nil {
			return nil, err
		}
		req.Token = tok
	}
	res, err := c.Exchange(ctx, req)
	if err != nil {
		return nil, err
	}
	out := &Response{Code: res.Code, Format: res.Format, Payload: res.Payload, Token: req.Token}
	if res.Code < 64 || res.Code >= 96 || res.Format == nil || len(res.Payload) == 0 {
		return out, nil
	}
	if *res.Format == lwm2m.FormatLinkFormat {
		links, err := link.Parse(string(res.Payload))
		if err == nil {
			// The links of a prefixed Discover carry no prefix (GW §8.3.1);
			// one that does is stripped.
			out.Links, err = link.ParseDiscover(links, Path{Prefix: base.Prefix}.URI(c.RootPath))
		}
		return out, err
	}
	out.Nodes, err = Decode(*res.Format, base, res.Payload, c.Schema)
	return out, err
}

// Read reads p (Read with Prefix maps to GET /{prefix}/{o}/..., GW §10).
func (c *Client) Read(ctx context.Context, p Path, accept *lwm2m.ContentFormat) (*Response, error) {
	if err := c.check(p); err != nil {
		return nil, err
	}
	if accept == nil {
		accept = cfPtr(c.defaultFormat())
	}
	return c.do(ctx, p, &server.Message{Code: codes.GET, Path: p.URI(c.RootPath), Accept: accept})
}

// Discover returns the links under p, without the prefix.
func (c *Client) Discover(ctx context.Context, p Path) (*Response, error) {
	if err := c.check(p); err != nil {
		return nil, err
	}
	return c.do(ctx, p, &server.Message{Code: codes.GET, Path: p.URI(c.RootPath), Accept: cfPtr(lwm2m.FormatLinkFormat)})
}

// Write writes nodes to p: PUT (replace) or POST (partial update).
func (c *Client) Write(ctx context.Context, p Path, nodes []Node, replace bool, cf *lwm2m.ContentFormat) (*Response, error) {
	if err := c.check(p); err != nil {
		return nil, err
	}
	return c.send(ctx, p, map[bool]codes.Code{true: codes.PUT, false: codes.POST}[replace], nodes, cf)
}

func (c *Client) send(ctx context.Context, p Path, method codes.Code, nodes []Node, cf *lwm2m.ContentFormat) (*Response, error) {
	f := c.defaultFormat()
	if cf != nil {
		f = *cf
	}
	body, err := Encode(f, p, nodes)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, p, &server.Message{Code: method, Path: p.URI(c.RootPath), Format: &f, Payload: body})
}

// WriteAttributes sets notification attributes on p (query "pmin=10"...).
func (c *Client) WriteAttributes(ctx context.Context, p Path, query []string) (*Response, error) {
	if err := c.check(p); err != nil {
		return nil, err
	}
	return c.do(ctx, p, &server.Message{Code: codes.PUT, Path: p.URI(c.RootPath), Query: query})
}

// Execute executes the resource p with optional arguments.
func (c *Client) Execute(ctx context.Context, p Path, args string) (*Response, error) {
	if err := c.check(p); err != nil {
		return nil, err
	}
	if !p.IsResource() {
		return nil, fmt.Errorf("%w: Execute needs a resource path", ErrPath)
	}
	m := &server.Message{Code: codes.POST, Path: p.URI(c.RootPath)}
	if args != "" {
		m.Format, m.Payload = cfPtr(lwm2m.FormatText), []byte(args)
	}
	return c.do(ctx, p, m)
}

// Create creates an instance of object p.
func (c *Client) Create(ctx context.Context, p Path, nodes []Node, cf *lwm2m.ContentFormat) (*Response, error) {
	if err := c.check(p); err != nil {
		return nil, err
	}
	if !p.IsObject() {
		return nil, fmt.Errorf("%w: Create needs an object path", ErrPath)
	}
	return c.send(ctx, p, codes.POST, nodes, cf)
}

// Delete deletes the object instance p.
func (c *Client) Delete(ctx context.Context, p Path) (*Response, error) {
	if err := c.check(p); err != nil {
		return nil, err
	}
	return c.do(ctx, p, &server.Message{Code: codes.DELETE, Path: p.URI(c.RootPath)})
}

// Observe starts observing p; the response is the first notification.
func (c *Client) Observe(ctx context.Context, p Path, accept *lwm2m.ContentFormat) (*Response, error) {
	if err := c.check(p); err != nil {
		return nil, err
	}
	if accept == nil {
		accept = cfPtr(c.defaultFormat())
	}
	zero := uint32(0)
	return c.do(ctx, p, &server.Message{Code: codes.GET, Path: p.URI(c.RootPath), Accept: accept, Observe: &zero})
}

const codeFETCH, codeIPATCH codes.Code = 5, 7

// ReadComposite reads paths of the gateway and of several devices in one
// FETCH (GW §8.3.2). The request body is SenML CBOR, the response accept.
func (c *Client) ReadComposite(ctx context.Context, paths []Path, accept *lwm2m.ContentFormat) (*Response, error) {
	return c.composite(ctx, paths, accept, nil)
}

// ObserveComposite is ReadComposite with Observe=0.
func (c *Client) ObserveComposite(ctx context.Context, paths []Path, accept *lwm2m.ContentFormat) (*Response, error) {
	zero := uint32(0)
	return c.composite(ctx, paths, accept, &zero)
}

func (c *Client) composite(ctx context.Context, paths []Path, accept *lwm2m.ContentFormat, obs *uint32) (*Response, error) {
	if err := c.check(paths...); err != nil {
		return nil, err
	}
	if accept == nil {
		accept = cfPtr(c.defaultFormat())
	}
	bf := lwm2m.FormatSenMLCBOR
	if _, ok := senmlCodec(*accept); ok {
		bf = *accept // Accept equal to Content-Format when possible (C10)
	}
	body, err := EncodePaths(bf, paths)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, Path{}, &server.Message{Code: codeFETCH, Path: Path{}.URI(c.RootPath),
		Format: &bf, Accept: accept, Observe: obs, Payload: body})
}

// WriteComposite writes nodes of the gateway and of several devices in one
// iPATCH (GW §8.3.2).
func (c *Client) WriteComposite(ctx context.Context, nodes []Node, cf *lwm2m.ContentFormat) (*Response, error) {
	var ps []Path
	for _, n := range nodes {
		ps = append(ps, Path{n.Prefix, n.Path})
	}
	if err := c.check(ps...); err != nil {
		return nil, err
	}
	return c.send(ctx, Path{}, codeIPATCH, nodes, cf)
}
