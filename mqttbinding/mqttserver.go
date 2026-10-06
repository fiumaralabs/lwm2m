package mqttbinding

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/bootstrap"
)

// MQTTServerParams is one /24 MQTT Server instance (Core E.11): the MQTT
// CONNECT parameters of a client of the broker. The Server's own session
// takes them from Config.Session; a Bootstrap-Server writes them to a
// client with Nodes or BootstrapWrites.
type MQTTServerParams struct {
	WillRetain   bool   // res 0
	WillTopic    string // res 1; "" sends no will
	WillMessage  string // res 2
	CleanSession bool   // res 3
	WillQoS      uint8  // res 4, 0..3
	KeepAlive    uint16 // res 5, seconds, 0..65535 (0 disables keep-alive)
	ClientID     string // res 6, mandatory
	UserName     string // res 7
	Password     []byte // res 8
}

// Validate enforces the E.11 ranges and the Client Identifier rule: the
// id is mandatory and must be one every broker accepts, 1..23 bytes of
// [0-9a-zA-Z] (T §8.8, MQTT 3.1.1 §3.1.3.1).
// ponytail: brokers MAY accept longer ids; relax when a deployment needs it.
func (p MQTTServerParams) Validate() error {
	if err := ValidClientID(p.ClientID); err != nil {
		return err
	}
	if p.WillQoS > 3 {
		return fmt.Errorf("mqttbinding: /24 Will QoS %d not in 0..3", p.WillQoS)
	}
	for _, s := range []string{p.WillTopic, p.WillMessage, p.UserName} {
		if !utf8.ValidString(s) || len(s) > 65535 {
			return errors.New("mqttbinding: /24 strings must be UTF-8 of at most 65535 bytes")
		}
	}
	if len(p.Password) > 65535 {
		return errors.New("mqttbinding: /24 Password longer than 65535 bytes")
	}
	if p.WillTopic == "" && (p.WillMessage != "" || p.WillRetain) {
		return errors.New("mqttbinding: /24 Will Message or Will Retain without a Will Topic")
	}
	return nil
}

// ValidClientID reports whether id is 1..23 bytes of [0-9a-zA-Z].
func ValidClientID(id string) error {
	if len(id) < 1 || len(id) > 23 {
		return fmt.Errorf("mqttbinding: client identifier %q is %d bytes, want 1..23", id, len(id))
	}
	for _, c := range []byte(id) {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z') {
			return fmt.Errorf("mqttbinding: client identifier %q has %q, want only [0-9a-zA-Z]", id, c)
		}
	}
	return nil
}

// apply configures an MQTT CONNECT from p. MQTT 3.1.1 has QoS 0..2
// only, so the E.11 value 3 is refused here.
func (p MQTTServerParams) apply(o *mqtt.ClientOptions) error {
	if err := p.Validate(); err != nil {
		return err
	}
	o.SetClientID(p.ClientID).SetCleanSession(p.CleanSession).
		SetKeepAlive(time.Duration(p.KeepAlive) * time.Second).
		SetUsername(p.UserName).SetPassword(string(p.Password))
	if p.WillTopic != "" {
		if p.WillQoS > 2 {
			return fmt.Errorf("mqttbinding: Will QoS %d is not an MQTT QoS", p.WillQoS)
		}
		o.SetBinaryWill(p.WillTopic, []byte(p.WillMessage), p.WillQoS, p.WillRetain)
	}
	return nil
}

// Nodes returns the /24 instance at inst. Empty optional strings are left
// out; the booleans and integers are always written.
func (p MQTTServerParams) Nodes(inst uint16) ([]lwm2m.Node, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	base := lwm2m.NewPath(24, inst)
	r := func(id uint16, v lwm2m.Value) lwm2m.Node { return lwm2m.ValueNode(base.Append(id), v) }
	ns := []lwm2m.Node{r(0, lwm2m.Boolean(p.WillRetain))}
	if p.WillTopic != "" {
		ns = append(ns, r(1, lwm2m.String(p.WillTopic)), r(2, lwm2m.String(p.WillMessage)))
	}
	ns = append(ns, r(3, lwm2m.Boolean(p.CleanSession)), r(4, lwm2m.Unsigned(uint64(p.WillQoS))),
		r(5, lwm2m.Unsigned(uint64(p.KeepAlive))), r(6, lwm2m.String(p.ClientID)))
	if p.UserName != "" {
		ns = append(ns, r(7, lwm2m.String(p.UserName)))
	}
	if p.Password != nil {
		ns = append(ns, r(8, lwm2m.Opaque(p.Password)))
	}
	return ns, nil
}

// BootstrapWrites returns the raw Bootstrap-Writes (bootstrap.BootstrapConfig.Writes)
// that give the Server Account of /0/sec its MQTT configuration: the /24
// instance mqttInst linked from /0/sec/26 and, when k is non-nil, the /23
// instance coseInst linked from /0/sec/27, which obliges the client to
// use COSE with that Server (T §8.8). They follow the /0 instance write.
func BootstrapWrites(sec uint16, mqttInst uint16, p MQTTServerParams, coseInst uint16, k *COSEKey) ([]bootstrap.Write, error) {
	ns, err := p.Nodes(mqttInst)
	if err != nil {
		return nil, err
	}
	secP := lwm2m.NewPath(0, sec)
	link := func(res, obj, inst uint16) bootstrap.Write {
		return bootstrap.Write{Path: secP.Append(res), Nodes: []lwm2m.Node{lwm2m.ValueNode(secP.Append(res), lwm2m.Objlnk(obj, inst))}}
	}
	ws := []bootstrap.Write{{Path: lwm2m.NewPath(24, mqttInst), Nodes: ns}, link(26, 24, mqttInst)}
	if k != nil {
		ns, err := k.Nodes(coseInst)
		if err != nil {
			return nil, err
		}
		ws = append(ws, bootstrap.Write{Path: lwm2m.NewPath(23, coseInst), Nodes: ns}, link(27, 23, coseInst))
	}
	return ws, nil
}
