package apiv1

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m"
)

// profileBootstrapTemplate models the §10.1 input, rather than the smaller
// bootstrapInput used by the device bootstrap endpoint's current validator.
type profileBootstrapTemplate struct {
	Servers []struct {
		Server           string  `json:"server"`
		URI              string  `json:"uri"`
		ShortServerID    *uint16 `json:"shortServerId"`
		SecurityInstance *uint16 `json:"securityInstance"`
		ServerInstance   *uint16 `json:"serverInstance"`
		Security         *struct {
			PSK *struct {
				Identity string `json:"identity"`
				Key      string `json:"key"`
				Generate bool   `json:"generate"`
			} `json:"psk"`
			X509 *struct {
				Certificate       string `json:"certificate"`
				PrivateKey        string `json:"privateKey"`
				ServerCertificate string `json:"serverCertificate"`
			} `json:"x509"`
			RPK *struct {
				PublicKey string `json:"publicKey"`
			} `json:"rpk"`
			None   *struct{} `json:"none"`
			SNI    string    `json:"sni"`
			OSCORE *struct {
				MasterSecret  string `json:"masterSecret"`
				SenderID      string `json:"senderId"`
				RecipientID   string `json:"recipientId"`
				MasterSalt    string `json:"masterSalt"`
				IDContext     string `json:"idContext"`
				AEADAlgorithm *int   `json:"aeadAlgorithm"`
				HMACAlgorithm *int   `json:"hmacAlgorithm"`
			} `json:"oscore"`
		} `json:"security"`
		LifetimeSeconds                *uint32 `json:"lifetimeSeconds"`
		Binding                        string  `json:"binding"`
		DefaultMinPeriod               *uint32 `json:"defaultMinPeriod"`
		DefaultMaxPeriod               *uint32 `json:"defaultMaxPeriod"`
		DisableTimeoutSeconds          *uint32 `json:"disableTimeoutSeconds"`
		NotifyWhenDisabled             *bool   `json:"notifyWhenDisabled"`
		BootstrapOnRegistrationFailure *bool   `json:"bootstrapOnRegistrationFailure"`
	} `json:"servers"`
	BootstrapServer *struct {
		AccountTimeoutSeconds *uint32 `json:"accountTimeoutSeconds"`
		ClientHoldOffSeconds  *uint32 `json:"clientHoldOffSeconds"`
	} `json:"bootstrapServer"`
	ACLs []struct {
		Object   *uint16             `json:"object"`
		Instance *uint16             `json:"instance"`
		Owner    *uint16             `json:"owner"`
		Rights   map[string][]string `json:"rights"`
	} `json:"acls"`
	Delete []string `json:"delete"`
	Writes []struct {
		Path   string `json:"path"`
		Values []struct {
			Path      string          `json:"path"`
			Type      string          `json:"type"`
			Value     json.RawMessage `json:"value"`
			Timestamp string          `json:"timestamp"`
			Prefix    string          `json:"prefix"`
		} `json:"values"`
	} `json:"writes"`
	Options *struct {
		Format                    string   `json:"format"`
		RenumberSecurityInstances *bool    `json:"renumberSecurityInstances"`
		Discover                  *bool    `json:"discover"`
		Read                      []string `json:"read"`
		RefusePack                *bool    `json:"refusePack"`
	} `json:"options"`
}

func validProfileBootstrap(raw json.RawMessage) bool {
	var template profileBootstrapTemplate
	if len(raw) == 0 || raw[0] != '{' || !profileDecode(raw, &template) {
		return false
	}
	var generic any
	if json.Unmarshal(raw, &generic) != nil || bootstrapHasNull(generic) {
		return false
	}
	// Reuse uniqueness and ACL-owner validation. Generated credentials are
	// intentionally allowed in templates, unlike the current device PUT handler.
	var base bootstrapInput
	if json.Unmarshal(raw, &base) != nil {
		return false
	}
	for i := range base.Servers {
		if p := base.Servers[i].Security.PSK; p != nil && p.Generate {
			base.Servers[i].Security.PSK = nil
		}
	}
	code, fields := validateBootstrap(base)
	if code != "" || len(fields) != 0 {
		return false
	}
	pathOK := func(s string) bool {
		p, err := lwm2m.ParsePath(s)
		return err == nil && strings.HasPrefix(s, "/") && !p.IsRoot()
	}
	for _, s := range template.Servers {
		if s.Server != "" && s.Server != "self" {
			return false
		}
		if s.DefaultMinPeriod != nil && s.DefaultMaxPeriod != nil && *s.DefaultMinPeriod > *s.DefaultMaxPeriod {
			return false
		}
	}
	for _, acl := range template.ACLs {
		if acl.Object == nil || acl.Instance == nil {
			return false
		}
		for id, rights := range acl.Rights {
			if _, err := strconv.ParseUint(id, 10, 16); err != nil {
				return false
			}
			for _, right := range rights {
				switch right {
				case "read", "write", "execute", "delete", "create":
				default:
					return false
				}
			}
		}
	}
	for _, p := range template.Delete {
		if !pathOK(p) {
			return false
		}
	}
	if template.Options != nil {
		switch template.Options.Format {
		case "", "text", "link-format", "opaque", "cbor", "senml-json", "senml-cbor", "senml-etch-json", "senml-etch-cbor", "tlv", "oma-json", "lwm2m-cbor":
		default:
			return false
		}
		for _, p := range template.Options.Read {
			if !pathOK(p) {
				return false
			}
		}
	}
	for _, w := range template.Writes {
		if !pathOK(w.Path) || len(w.Values) == 0 {
			return false
		}
		for _, v := range w.Values {
			if !validProfileValue(v.Path, v.Type, v.Value) {
				return false
			}
			if v.Timestamp != "" {
				if _, err := time.Parse(time.RFC3339, v.Timestamp); err != nil {
					return false
				}
			}
		}
	}
	return true
}

func bootstrapHasNull(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case []any:
		for _, child := range x {
			if bootstrapHasNull(child) {
				return true
			}
		}
	case map[string]any:
		for _, child := range x {
			if bootstrapHasNull(child) {
				return true
			}
		}
	}
	return false
}

// validProfileValue applies the same §7.1 shape rules to both profile inputs.
func validProfileValue(valuePath, kind string, raw json.RawMessage) bool {
	path, err := lwm2m.ParsePath(valuePath)
	if err != nil || !strings.HasPrefix(valuePath, "/") || path.IsRoot() {
		return false
	}
	if len(raw) == 0 {
		return kind == "" && (path.IsInstance() || path.IsResource())
	}
	return (path.IsResource() || path.IsResourceInstance()) && string(raw) != "null" && raw[0] != '{' && raw[0] != '[' && validBootstrapValue(kind, raw)
}

func validBootstrapValue(kind string, raw json.RawMessage) bool {
	if kind == "" {
		return true
	} // The device model may supply the type.
	if len(raw) == 0 {
		return false
	}
	var s string
	switch kind {
	case "string", "corelnk":
		return json.Unmarshal(raw, &s) == nil
	case "boolean":
		var b bool
		return json.Unmarshal(raw, &b) == nil
	case "integer", "unsigned":
		text := string(raw)
		if raw[0] == '"' {
			if json.Unmarshal(raw, &text) != nil {
				return false
			}
		}
		if kind == "integer" {
			_, err := strconv.ParseInt(text, 10, 64)
			return err == nil
		}
		_, err := strconv.ParseUint(text, 10, 64)
		return err == nil
	case "float":
		if raw[0] == '"' {
			return json.Unmarshal(raw, &s) == nil && (s == "NaN" || s == "Infinity" || s == "-Infinity")
		}
		var f float64
		return json.Unmarshal(raw, &f) == nil
	case "opaque":
		if json.Unmarshal(raw, &s) != nil {
			return false
		}
		_, err := base64.StdEncoding.DecodeString(s)
		return err == nil
	case "time":
		if json.Unmarshal(raw, &s) != nil {
			return false
		}
		_, err := time.Parse(time.RFC3339, s)
		return err == nil
	case "objlnk":
		if json.Unmarshal(raw, &s) != nil {
			return false
		}
		parts := strings.Split(s, ":")
		if len(parts) != 2 {
			return false
		}
		for _, p := range parts {
			if _, err := strconv.ParseUint(p, 10, 16); err != nil {
				return false
			}
		}
		return true
	default:
		return false
	}
}
