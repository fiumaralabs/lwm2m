package apiv1

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
)

// ObservationDeliveryStore adds atomic delivery metadata mutation to intent storage.
type ObservationDeliveryStore interface {
	store.Observations
	UpdateDelivery(string, string, func(*store.Observation)) (store.Observation, error)
}

// ObservationDelivery binds observation intent to one server tenant. All session
// transitions and token mappings share a lock; network exchanges never hold it.
type ObservationDelivery struct {
	ObservationDeliveryStore
	mu         sync.Mutex
	srv        *server.Server
	state      store.State
	events     store.Events
	tenant     string
	active     map[string]*server.Observation
	generation map[string]uint64
	busy       map[string]uint64
	early      map[string][]server.Notification
	cancelMode map[string]bool
}

func NewObservationDelivery(srv *server.Server, records ObservationDeliveryStore, state store.State, events store.Events, tenant string) *ObservationDelivery {
	return &ObservationDelivery{ObservationDeliveryStore: records, srv: srv, state: state, events: events, tenant: tenant, active: map[string]*server.Observation{}, generation: map[string]uint64{}, busy: map[string]uint64{}, early: map[string][]server.Notification{}, cancelMode: map[string]bool{}}
}
func (d *ObservationDelivery) Create(tenant string, v store.Observation) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if tenant != d.tenant {
		return store.ErrTenant
	}
	if err := d.ObservationDeliveryStore.Create(tenant, v); err != nil {
		return err
	}
	d.start(v)
	return nil
}

// start requires mu and pins both the generation and registration for delivery.
func (d *ObservationDelivery) start(v store.Observation) {
	r, ok := d.srv.Store().ByEndpoint(v.Device)
	if !ok || v.Status == "ended" {
		return
	}
	if _, ok := d.busy[v.ID]; ok {
		return
	}
	g := d.generation[v.Device]
	d.busy[v.ID] = g
	go d.deliver(v, r, g)
}
func (d *ObservationDelivery) emit(kind, ep string, data any) {
	if d.events != nil {
		if body, err := json.Marshal(data); err == nil {
			d.events.Append(d.tenant, kind, ep, body)
		}
	}
}
func (d *ObservationDelivery) update(id string, f func(*store.Observation)) store.Observation {
	before, _ := d.Get(d.tenant, id)
	v, _ := d.UpdateDelivery(d.tenant, id, f)
	if before.Status != v.Status {
		d.emit("lwm2m.observation.updated", v.Device, map[string]any{"observation": v})
	}
	return v
}
func (d *ObservationDelivery) deliver(v store.Observation, r *server.Registration, generation uint64) {
	ctx, cancel := context.WithTimeout(server.WithRegistration(context.Background(), r.ID), 30*time.Second)
	defer cancel()
	paths := make([]lwm2m.Path, len(v.Paths))
	for i, p := range v.Paths {
		paths[i], _ = lwm2m.ParsePath(p)
	}
	var ob *server.Observation
	var resp *server.Response
	var err error
	query := []string{}
	for k, val := range v.Attributes {
		query = append(query, k+"="+string(val))
	}
	if len(query) > 0 && (r.Version != "1.2" || len(paths) > 1) {
		err = fmt.Errorf("Write-Attributes before Observe is not supported")
	}
	if err == nil && (resp == nil || resp.Success()) {
		if len(paths) == 1 {
			ob, resp, err = d.srv.Observe(ctx, v.Device, paths[0], server.ObserveOptions{Prefix: v.Prefix, Query: query, Accept: observationFormat(v.Accept)})
		} else if v.Prefix != "" {
			err = fmt.Errorf("composite gateway observations are unsupported")
		} else {
			ob, resp, err = d.srv.ObserveComposite(ctx, v.Device, paths, server.CompositeOptions{Format: observationFormat(v.Format), Accept: observationFormat(v.Accept)})
		}
	}
	d.mu.Lock()
	current, _ := d.Get(d.tenant, v.ID)
	if d.generation[v.Device] != generation || current.Status == "ended" {
		active := current.Status == "ended" && d.generation[v.Device] == generation && d.cancelMode[v.ID]
		delete(d.cancelMode, v.ID)
		if ob != nil {
			delete(d.early, ob.ID)
		}
		d.clearEarly(v.Device)
		d.mu.Unlock()
		if ob != nil {
			cancelCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			d.srv.CancelObservation(cancelCtx, ob, active)
		}
		return
	}
	defer d.mu.Unlock()
	delete(d.busy, v.ID)
	d.update(v.ID, func(v *store.Observation) {
		if err != nil || ob == nil {
			v.Status = "failed"
			message := "observation refused"
			if err != nil {
				message = err.Error()
			} else if resp != nil {
				message = server.CodeString(resp.Code)
			}
			v.Error, _ = json.Marshal(map[string]string{"message": message})
			return
		}
		now := time.Now().UTC()
		v.Status = "active"
		v.EstablishedAt = &now
		v.Error = nil
		d.active[v.ID] = ob
	})
	if ob != nil {
		for _, e := range d.early[ob.ID] {
			d.notification(e)
		}
		delete(d.early, ob.ID)
	}
	d.clearEarly(v.Device)
}
func observationFormat(name string) *lwm2m.ContentFormat {
	formats := map[string]lwm2m.ContentFormat{"text": 0, "link-format": 40, "opaque": 42, "cbor": 60, "senml-json": 110, "senml-cbor": 112, "senml-etch-json": 320, "senml-etch-cbor": 322, "tlv": 11542, "oma-json": 11543, "lwm2m-cbor": 11544}
	if f, ok := formats[name]; ok {
		return &f
	}
	return nil
}
func (d *ObservationDelivery) End(tenant, id string) error { return d.EndMode(tenant, id, true) }
func (d *ObservationDelivery) EndMode(tenant, id string, active bool) error {
	d.mu.Lock()
	v, err := d.Get(tenant, id)
	if err == nil {
		err = d.ObservationDeliveryStore.End(tenant, id)
	}
	if err != nil {
		d.mu.Unlock()
		return err
	}
	ob := d.active[id]
	delete(d.active, id)
	if _, ok := d.busy[id]; ok {
		d.cancelMode[id] = active
	}
	delete(d.busy, id)
	if v.Status != "ended" {
		v.Status = "ended"
		d.emit("lwm2m.observation.updated", v.Device, map[string]any{"observation": v})
	}
	d.mu.Unlock()
	if ob != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = d.srv.CancelObservation(ctx, ob, active)
	}
	return err
}
func (d *ObservationDelivery) HandleEvent(event server.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch e := event.(type) {
	case server.Registered:
		if e.Replaced != nil {
			d.drop(e.Replaced.Endpoint)
		}
		records, _ := d.List(d.tenant, e.Registration.Endpoint)
		for _, v := range records {
			d.start(v)
		}
	case server.Deregistered:
		// Ignore a late deregistration for an already replaced session.
		if r, ok := d.srv.Store().ByEndpoint(e.Registration.Endpoint); ok && r.ID != e.Registration.ID {
			return
		}
		d.drop(e.Registration.Endpoint)
	case server.Notification:
		d.notification(e)
	}
}

// notification and clearEarly require mu. Pending tokens are correlated after
// the exchange returns; token identity, not paths, distinguishes overlaps.
func (d *ObservationDelivery) notification(e server.Notification) {
	for id, ob := range d.active {
		if ob.ID != e.Observation.ID || ob.RegistrationID != e.Registration.ID {
			continue
		}
		v := d.update(id, func(v *store.Observation) {
			now := time.Now().UTC()
			v.LastNotificationAt = &now
			v.NotificationCount++
		})
		if v.Status != "active" {
			return
		}
		var prefix any
		if v.Prefix != "" {
			prefix = v.Prefix
		}
		data := map[string]any{"observation": id, "prefix": prefix, "values": operations.ObservationValues(e.Response.Nodes)}
		if d.state != nil {
			body, _ := json.Marshal(data)
			now := time.Now().UTC()
			d.state.Record(operations.Operation{Tenant: d.tenant, Device: v.Device, Prefix: v.Prefix, Type: "notification", Status: operations.Succeeded, CompletedAt: &now, Result: body})
		}
		d.emit("lwm2m.notification", v.Device, data)
		return
	}
	records, _ := d.List(d.tenant, e.Registration.Endpoint)
	for _, v := range records {
		if _, ok := d.busy[v.ID]; ok {
			d.early[e.Observation.ID] = append(d.early[e.Observation.ID], e)
			return
		}
	}
}

func (d *ObservationDelivery) clearEarly(ep string) {
	records, _ := d.List(d.tenant, ep)
	for _, v := range records {
		if _, ok := d.busy[v.ID]; ok {
			return
		}
	}
	for token, events := range d.early {
		if len(events) > 0 && events[0].Registration.Endpoint == ep {
			delete(d.early, token)
		}
	}
}
func (d *ObservationDelivery) drop(ep string) {
	d.generation[ep]++
	records, _ := d.List(d.tenant, ep)
	for _, v := range records {
		delete(d.active, v.ID)
		delete(d.busy, v.ID)
		d.update(v.ID, func(v *store.Observation) {
			v.EstablishedAt = nil
			if v.Persistent {
				v.Status = "pending"
			} else {
				v.Status = "ended"
			}
		})
	}
}
