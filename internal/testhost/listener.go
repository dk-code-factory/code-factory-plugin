package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// The kernel owns the listener before the parent receives its actual address.
// An explicit base port keeps its adjacent-port contract and fails on collision.
func fixturePort(base int) (int, error) {
	value := os.Getenv("CODE_FACTORY_TEST_DAEMON_PORT")
	if value == "" {
		return base + 1, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || strings.Trim(value, "0123456789") != "" || port < 0 || port > 65535 {
		return 0, fmt.Errorf("CODE_FACTORY_TEST_DAEMON_PORT must be a decimal integer from 0 to 65535")
	}
	return port, nil
}

func serveFixture(server *http.Server) error {
	listener, err := net.Listen("tcp4", server.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	if ready := os.Getenv("CODE_FACTORY_TEST_READY_FILE"); ready != "" {
		data, err := json.Marshal(map[string]string{"origin": "http://" + listener.Addr().String()})
		if err != nil {
			return err
		}
		if err = os.WriteFile(ready+".tmp", data, 0600); err != nil {
			return err
		}
		if err = os.Rename(ready+".tmp", ready); err != nil {
			return err
		}
	}
	return server.Serve(listener)
}
