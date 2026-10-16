package main

import (
	"embed"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
)

// IPSO and test objects from the OMNA LwM2M registry (commit 7d5204dd, root
// files, unmodified, OMA BSD-3-Clause; see THIRD_PARTY_NOTICES.md), added
// to the core objects 0-28 of package model so the page can name them and
// TLV values decode with the right types.
//
//go:embed objects/*.xml
var objectsFS embed.FS

func demoRegistry() *model.Registry {
	r := model.Default()
	sub, _ := fs.Sub(objectsFS, "objects")
	if err := r.LoadFS(sub); err != nil {
		panic(err) // embedded data; TestRegistryLoads covers it
	}
	return r
}

// blockedObject: Security and OSCORE hold credentials; the page never reads them.
func blockedObject(oid uint16) bool { return oid == 0 || oid == 21 }

type resourceJSON struct {
	ID          uint16            `json:"id"`
	Name        string            `json:"name"`
	Ops         string            `json:"ops"`
	Type        string            `json:"type"`
	Units       string            `json:"units,omitempty"`
	Multiple    bool              `json:"multiple,omitempty"`
	Mandatory   bool              `json:"mandatory,omitempty"`
	Range       string            `json:"range,omitempty"`
	Description string            `json:"description,omitempty"`
	Enum        map[string]string `json:"enum,omitempty"`
}

type objectDefJSON struct {
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	URN         string         `json:"urn,omitempty"`
	Description string         `json:"description,omitempty"`
	Multiple    bool           `json:"multiple"`
	Mandatory   bool           `json:"mandatory"`
	Resources   []resourceJSON `json:"resources"`
}

// objectJSON is one registered object with its model, if known.
type objectJSON struct {
	ID        uint16         `json:"id"`
	Version   string         `json:"version,omitempty"` // the ver= attribute, as registered
	Instances []uint16       `json:"instances"`
	Blocked   bool           `json:"blocked,omitempty"`
	Model     *objectDefJSON `json:"model,omitempty"`
}

var defCache sync.Map // *model.Object -> *objectDefJSON

func objectDef(o *model.Object) *objectDefJSON {
	if v, ok := defCache.Load(o); ok {
		return v.(*objectDefJSON)
	}
	d := &objectDefJSON{Name: o.Name, Version: o.Version.String(), URN: o.URN,
		Description: shortText(o.Description, 320), Multiple: o.Multiple, Mandatory: o.Mandatory}
	ids := make([]int, 0, len(o.Resources))
	for id := range o.Resources {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	for _, id := range ids {
		r := o.Resources[uint16(id)]
		ops := r.Operations.String()
		if ops == "" {
			ops = "-"
		}
		d.Resources = append(d.Resources, resourceJSON{ID: r.ID, Name: r.Name, Ops: ops, Type: r.Type.String(),
			Units: strings.TrimSpace(r.Units), Multiple: r.Multiple, Mandatory: r.Mandatory, Range: strings.TrimSpace(r.Range),
			Description: shortText(r.Description, 320), Enum: enumOf(r)})
	}
	defCache.Store(o, d)
	return d
}

// shortText keeps the first paragraph, cut at a word boundary.
func shortText(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) <= max {
		return s
	}
	cut := strings.LastIndex(s[:max], " ")
	if cut < max/2 {
		cut = max
	}
	return s[:cut] + "…"
}

// enumLine matches "0: DC power" and the tab tables of the registry
// ("0\tNormal\tThe battery ..."), not ranges like "10 - 20: Reserved".
var enumLine = regexp.MustCompile(`^\s*(\d{1,3})\s*(?::|\t)\s*([^\t]+)`)

// enumOf reads value labels from the description of an integer resource
// with a numeric range, as the OMA registry writes them. Only lists of two
// or more labels count, so prose that starts with a number does not.
func enumOf(r *model.Resource) map[string]string {
	if r.Type.String() != "integer" && r.Type.String() != "unsigned" {
		return nil
	}
	if !strings.Contains(r.Range, "..") && !strings.Contains(r.Range, "-") {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(r.Description, "\n") {
		m := enumLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		label := strings.TrimSpace(m[2])
		if i := strings.Index(label, ". "); i > 0 {
			label = label[:i]
		}
		label = strings.TrimSuffix(label, ".")
		if p := strings.Index(label, " ("); p > 0 && len(label) > 40 {
			label = label[:p]
		}
		if len(label) > 60 || label == "" {
			continue
		}
		if _, err := strconv.Atoi(label); err == nil {
			continue
		}
		out[m[1]] = label
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// objectsOf lists the registered objects of reg with their models at the
// versions the server resolved for this registration.
func objectsOf(models *server.Models, reg *server.Registration) []objectJSON {
	schema, _ := models.Schema(reg).(*model.Schema)
	out := make([]objectJSON, 0, len(reg.Objects))
	for _, o := range reg.Objects {
		j := objectJSON{ID: o.ID, Version: o.Version, Instances: o.Instances, Blocked: blockedObject(o.ID)}
		if j.Instances == nil {
			j.Instances = []uint16{}
		}
		if schema != nil {
			if m, ok := schema.Object(o.ID); ok {
				j.Model = objectDef(m)
			}
		}
		out = append(out, j)
	}
	return out
}
