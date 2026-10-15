// Command probe registers a testclient against a running lwm2m-demo,
// checks it appears in the web API, reads /3/0 through the web API, and
// deregisters. It is the deployment smoke test.
//
//	go run ./cmd/lwm2m-demo/probe -coap 178.105.125.174:5683 -web https://lwm2m.fiumaralabs.com
//	go run ./cmd/lwm2m-demo/probe -coap host:5684 -psk 000102030405060708090a0b0c0d0e0f -web ...
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/fiumaralabs/lwm2m"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/testclient"
)

func main() {
	addr := flag.String("coap", "127.0.0.1:5683", "server CoAP address host:port")
	web := flag.String("web", "http://127.0.0.1:8090", "web base URL")
	ep := flag.String("ep", fmt.Sprintf("probe-%d", time.Now().Unix()), "endpoint name")
	psk := flag.String("psk", "", "hex PSK: use DTLS with identity = endpoint")
	version := flag.String("lwm2m", "1.1", "LwM2M version")
	flag.Parse()

	cfg := testclient.Config{Endpoint: *ep, Version: *version, Lifetime: 300, Binding: "U"}
	if *psk != "" {
		k, err := hex.DecodeString(*psk)
		if err != nil {
			log.Fatal(err)
		}
		cfg.PSKIdentity, cfg.PSKKey, cfg.CID = *ep, k, true
	}
	c := testclient.New(cfg)
	c.AddObject(3)
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Fiumara Labs"))
	c.Set(lwm2m.MustParsePath("/3/0/1"), lwm2m.String("lwm2m-demo-probe"))
	c.Set(lwm2m.MustParsePath("/3/0/2"), lwm2m.String("SN-PROBE"))
	c.Set(lwm2m.MustParsePath("/3/0/16"), lwm2m.String("U"))
	if err := c.Dial(*addr); err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.Register(ctx)
	if err != nil {
		log.Fatalf("register: %v", err)
	}
	fmt.Printf("register %s (psk=%v) -> %v location=%s\n", *ep, *psk != "", resp.Code, c.Location())

	body := get(*web + "/api/clients")
	var list struct {
		Clients []map[string]any `json:"clients"`
	}
	_ = json.Unmarshal(body, &list)
	found := false
	for _, cl := range list.Clients {
		if cl["endpoint"] == *ep {
			found = true
			b, _ := json.Marshal(cl)
			fmt.Printf("listed: %s\n", b)
		}
	}
	if !found {
		fmt.Printf("NOT LISTED; /api/clients = %s\n", body)
		os.Exit(1)
	}
	fmt.Printf("web read /3/0: %s", get(*web+"/api/clients/"+url.PathEscape(*ep)+"/read?path=/3/0"))

	resp, err = c.Deregister(ctx)
	if err != nil {
		log.Fatalf("deregister: %v", err)
	}
	fmt.Printf("deregister -> %v\n", resp.Code)
	time.Sleep(300 * time.Millisecond)
	body = get(*web + "/api/clients")
	_ = json.Unmarshal(body, &list)
	for _, cl := range list.Clients {
		if cl["endpoint"] == *ep {
			fmt.Println("STILL LISTED after deregister")
			os.Exit(1)
		}
	}
	fmt.Printf("gone from list after deregister (%d clients listed)\n", len(list.Clients))
}

func get(u string) []byte {
	r, err := http.Get(u)
	if err != nil {
		log.Fatalf("GET %s: %v", u, err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return b
}
