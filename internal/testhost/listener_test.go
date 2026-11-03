package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFixtureListenerOwnsEphemeralLoopbackBeforePublishing(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready.json")
	t.Setenv("CODE_FACTORY_TEST_READY_FILE", ready)
	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("owned")) })}
	done := make(chan error, 1)
	go func() { done <- serveFixture(server) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	var data []byte
	var err error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		data, err = os.ReadFile(ready)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	var state map[string]string
	if err != nil || json.Unmarshal(data, &state) != nil {
		t.Fatalf("readiness was not published: %s %v", data, err)
	}
	response, err := http.Get(state["origin"])
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	address := state["origin"][len("http://"):]
	if listener, err := net.Listen("tcp4", address); err == nil {
		_ = listener.Close()
		t.Fatal("published port was not retained")
	}
}

func TestFixtureListenerCollisionDoesNotPublishOrReuseUnknownServer(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ready := filepath.Join(t.TempDir(), "ready.json")
	t.Setenv("CODE_FACTORY_TEST_READY_FILE", ready)
	if err := serveFixture(&http.Server{Addr: listener.Addr().String()}); err == nil {
		t.Fatal("occupied explicit port was accepted")
	}
	if _, err := os.Stat(ready); !os.IsNotExist(err) {
		t.Fatal("failed startup published readiness")
	}
}

func TestFixtureDaemonPortValidation(t *testing.T) {
	for _, value := range []string{"-1", "65536", " ", "1x", "+2"} {
		t.Setenv("CODE_FACTORY_TEST_DAEMON_PORT", value)
		if _, err := fixturePort(4173); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	for _, value := range []string{"0", "1", "65535"} {
		t.Setenv("CODE_FACTORY_TEST_DAEMON_PORT", value)
		if _, err := fixturePort(4173); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CODE_FACTORY_TEST_DAEMON_PORT", "")
	if port, err := fixturePort(5678); err != nil || port != 5679 {
		t.Fatalf("explicit adjacent port: %d %v", port, err)
	}
}
