package apiv1

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// validateModelXML enforces the structure of OMA LWM2M.xsd and
// LWM2M-v1_1.xsd before the more tolerant registry parser runs.
func validateModelXML(data []byte) error {
	d := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	type frame struct {
		name  string
		next  int
		count int
		text  strings.Builder
	}
	stack := []*frame{}
	sequences := map[string][]string{
		"Object": {"Name", "Description1", "ObjectID", "ObjectURN", "LWM2MVersion?", "ObjectVersion?", "MultipleInstances", "Mandatory", "Resources", "Description2"},
		"Item":   {"Name", "Operations", "MultipleInstances", "Mandatory", "Type", "RangeEnumeration", "Units", "Description"},
	}
	roots := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space != "" {
				return fmt.Errorf("unexpected namespace on %s", t.Name.Local)
			}
			if len(stack) == 0 {
				roots++
				if roots != 1 || t.Name.Local != "LWM2M" {
					return fmt.Errorf("expected LWM2M root")
				}
			} else {
				p := stack[len(stack)-1]
				switch p.name {
				case "LWM2M":
					if t.Name.Local != "Object" {
						return fmt.Errorf("unexpected %s in LWM2M", t.Name.Local)
					}
					p.count++
				case "Resources":
					if t.Name.Local != "Item" {
						return fmt.Errorf("unexpected %s in Resources", t.Name.Local)
					}
					p.count++
				case "Object", "Item":
					seq := sequences[p.name]
					for p.next < len(seq) && strings.HasSuffix(seq[p.next], "?") && strings.TrimSuffix(seq[p.next], "?") != t.Name.Local {
						p.next++
					}
					if p.next >= len(seq) || strings.TrimSuffix(seq[p.next], "?") != t.Name.Local {
						return fmt.Errorf("unexpected %s in %s", t.Name.Local, p.name)
					}
					p.next++
				default:
					return fmt.Errorf("unexpected child of %s", p.name)
				}
			}
			required := ""
			if t.Name.Local == "Object" {
				required = "ObjectType"
			}
			if t.Name.Local == "Item" {
				required = "ID"
			}
			found := false
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || a.Name.Space == "" && a.Name.Local == "xmlns" {
					continue
				}
				if len(stack) == 0 && a.Name.Space == "http://www.w3.org/2001/XMLSchema-instance" && a.Name.Local == "noNamespaceSchemaLocation" {
					continue
				}
				if a.Name.Space != "" || a.Name.Local != required || found {
					return fmt.Errorf("unexpected attribute %s on %s", a.Name.Local, t.Name.Local)
				}
				found = true
			}
			if required != "" && !found {
				return fmt.Errorf("missing %s on %s", required, t.Name.Local)
			}
			stack = append(stack, &frame{name: t.Name.Local})
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(t)) != "" {
					return fmt.Errorf("unexpected text outside root")
				}
				continue
			}
			f := stack[len(stack)-1]
			if f.name == "LWM2M" || f.name == "Object" || f.name == "Resources" || f.name == "Item" {
				if strings.TrimSpace(string(t)) != "" {
					return fmt.Errorf("unexpected text in %s", f.name)
				}
			} else {
				f.text.Write(t)
			}
		case xml.EndElement:
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if (f.name == "LWM2M" || f.name == "Resources") && f.count == 0 {
				return fmt.Errorf("empty %s", f.name)
			}
			if seq, ok := sequences[f.name]; ok && f.next != len(seq) {
				return fmt.Errorf("missing field in %s", f.name)
			}
			value := f.text.String()
			switch f.name {
			case "MultipleInstances":
				if value != "Single" && value != "Multiple" {
					return fmt.Errorf("invalid MultipleInstances")
				}
			case "Mandatory":
				if value != "Mandatory" && value != "Optional" {
					return fmt.Errorf("invalid Mandatory")
				}
			case "Operations":
				if value != "" && value != "R" && value != "W" && value != "RW" && value != "E" {
					return fmt.Errorf("invalid Operations")
				}
			case "Type":
				switch value {
				case "", "String", "Integer", "Unsigned Integer", "Float", "Boolean", "Opaque", "Time", "Objlnk", "Corelnk":
				default:
					return fmt.Errorf("invalid Type")
				}
			}
		case xml.Directive:
			return fmt.Errorf("XML directives are not supported")
		}
	}
	if roots != 1 || len(stack) != 0 {
		return fmt.Errorf("incomplete XML document")
	}
	return nil
}
