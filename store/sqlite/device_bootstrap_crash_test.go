package sqlite

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
)

func TestDeviceBootstrapKillRestart(t *testing.T) {
	if path := os.Getenv("BOOTSTRAP_CRASH_DB"); path != "" {
		d, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if os.Getenv("BOOTSTRAP_CRASH_POINT") == "before" {
			d.deviceCreateHook = func(p string) error {
				if p == "before-commit" {
					fmt.Println("READY")
					select {}
				}
				return nil
			}
		}
		_, _, err = d.CreateDeviceWithSecurityAndBootstrap(context.Background(), "a", "op", "fp", store.Device{Endpoint: "pump"}, server.SecurityInfo{Endpoint: "pump", PSKIdentity: "id", PSKKey: []byte("key")}, []byte(`{"a":true}`))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("READY")
		select {}
	}
	for _, point := range []string{"before", "after"} {
		t.Run(point, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.db")
			d, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = d.PutBootstrapConfig(context.Background(), "b", "pump", []byte(`{"b":true}`)); err != nil {
				t.Fatal(err)
			}
			if err = d.Close(); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestDeviceBootstrapKillRestart$")
			cmd.Env = append(os.Environ(), "BOOTSTRAP_CRASH_DB="+path, "BOOTSTRAP_CRASH_POINT="+point)
			pipe, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			ready := make(chan string, 1)
			go func() {
				scanner := bufio.NewScanner(pipe)
				if scanner.Scan() {
					ready <- scanner.Text()
				} else {
					ready <- ""
				}
			}()
			select {
			case line := <-ready:
				if line != "READY" {
					t.Fatal("child exited before ready")
				}
			case <-time.After(15 * time.Second):
				t.Fatal("child did not reach crash point")
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			d, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			_, devErr := d.Get("a", "pump")
			_, sec := d.Security().ByEndpoint("pump")
			_, cfg, err := d.GetBootstrapConfig(context.Background(), "a", "pump")
			if err != nil {
				t.Fatal(err)
			}
			if point == "before" {
				if !errors.Is(devErr, store.ErrDeviceNotFound) || sec || cfg {
					t.Fatalf("partial rollback: %v %v %v", devErr, sec, cfg)
				}
			} else if devErr != nil || !sec || !cfg {
				t.Fatalf("partial commit: %v %v %v", devErr, sec, cfg)
			}
			b, ok, err := d.GetBootstrapConfig(context.Background(), "b", "pump")
			if err != nil || !ok || string(b) != `{"b":true}` {
				t.Fatalf("other tenant: %s %v %v", b, ok, err)
			}
		})
	}
}
