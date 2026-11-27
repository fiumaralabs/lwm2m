package model_test

import (
	"encoding/xml"
	"io/fs"
	"strconv"
	"strings"
	"testing"

	lwm2m "github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/model"
)

func FuzzParseXML(f *testing.F) {
	for _, name := range []string{"registry/26-1_0.xml", "registry/0-1_2.xml"} {
		data, err := fs.ReadFile(model.CoreFS(), name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(`<LWM2M/>`))
	f.Add([]byte(`<LWM2M><Object><ObjectID>3</ObjectID><MultipleInstances>Single</MultipleInstances><Mandatory>Mandatory</Mandatory><Resources><Item ID="0"><Operations>R</Operations><MultipleInstances>Single</MultipleInstances><Mandatory>Mandatory</Mandatory><Type>String</Type></Item></Resources></Object></LWM2M>`))
	f.Fuzz(func(t *testing.T, data []byte) {
		objects, err := model.ParseXML(data)
		if err != nil {
			return
		}
		// Inspect the source as well as the map, which cannot retain duplicate IDs.
		var doc struct {
			Objects []struct {
				Resources struct {
					Items []struct {
						ID string `xml:"ID,attr"`
					} `xml:"Item"`
				} `xml:"Resources"`
			} `xml:"Object"`
		}
		if err := xml.Unmarshal(data, &doc); err != nil || len(objects) != len(doc.Objects) || len(objects) == 0 {
			t.Fatalf("accepted XML could not be revalidated: %v", err)
		}
		for i, object := range objects {
			if object == nil || object.ID >= lwm2m.MaxID {
				t.Fatalf("invalid object: %+v", object)
			}
			seen := make(map[uint64]bool)
			for _, item := range doc.Objects[i].Resources.Items {
				id, err := strconv.ParseUint(strings.TrimSpace(item.ID), 10, 16)
				if err != nil || id >= lwm2m.MaxID || seen[id] {
					t.Fatalf("invalid or duplicate resource ID %q", item.ID)
				}
				seen[id] = true
				resource, ok := object.Resources[uint16(id)]
				if !ok || resource == nil || resource.ID != uint16(id) {
					t.Fatalf("resource %d not preserved", id)
				}
			}
			if len(seen) != len(object.Resources) {
				t.Fatal("resource count changed")
			}
		}
	})
}
