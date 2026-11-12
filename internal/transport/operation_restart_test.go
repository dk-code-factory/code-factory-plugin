package transport

import (
	"bufio"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A separate process is deliberately killed at the two acceptance boundaries.
// The fixture executes no providers, agents or installed service commands.
func TestOperationRestartChild(t *testing.T) {
	path := os.Getenv("CODE_FACTORY_RESTART_FIXTURE")
	if path == "" {
		t.Skip("subprocess-only restart fixture")
	}
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := newOperationQueue(db, &documentCache{})
	fmt.Println(`{"ready":true}`)
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	submitUntilAvailable(t, q, q.instance, "restart-request", "fixture", "fixture", false, 1, func(ctx context.Context) (any, error) {
		fmt.Println(`{"effect":true}`)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"receipt": true, "instanceId": q.instance})
	<-db.Context().Done()
}
func TestActualProcessRestartNeverReplaysMemoryOrRunningWork(t *testing.T) {
	for _, phase := range []string{"before-persistence", "during-effect"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			db, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "-test.run=^TestOperationRestartChild$")
			cmd.Env = append(os.Environ(), "CODE_FACTORY_RESTART_FIXTURE="+path)
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = io.Discard
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
			messages := make(chan map[string]any, 8)
			go func() {
				scanner := bufio.NewScanner(output)
				for scanner.Scan() {
					var value map[string]any
					if json.Unmarshal(scanner.Bytes(), &value) == nil {
						messages <- value
					}
				}
				close(messages)
			}()
			next := func() map[string]any {
				select {
				case value := <-messages:
					if value == nil {
						t.Fatal("restart child exited early")
					}
					return value
				case <-time.After(10 * time.Second):
					t.Fatal("restart child timed out")
					return nil
				}
			}
			if next()["ready"] != true {
				t.Fatal("child did not initialize")
			}
			var release func()
			var writeDone chan struct{}
			var writeErr error
			if phase == "before-persistence" {
				unlock := make(chan struct{})
				release = sync.OnceFunc(func() { close(unlock) })
				writeDone = make(chan struct{})
				t.Cleanup(func() {
					release()
					select {
					case <-writeDone:
						if writeErr != nil {
							t.Error("restart fixture writer", writeErr)
						}
					case <-time.After(5 * time.Second):
						t.Error("restart fixture writer did not exit before storage disposal")
					}
				})
				locked := make(chan struct{})
				go func() {
					defer close(writeDone)
					writeErr = db.Write(context.Background(), func(tx *store.Tx) error {
						if err := tx.Put("fixture", "writer", map[string]bool{"held": true}); err != nil {
							return err
						}
						close(locked)
						<-unlock
						return nil
					})
				}()
				select {
				case <-locked:
				case <-writeDone:
					t.Fatalf("writer failed before barrier: %v", writeErr)
				case <-time.After(5 * time.Second):
					t.Fatal("writer did not reach barrier")
				}
			}
			_, _ = io.WriteString(input, "accept\n")
			var instance string
			effect := false
			for instance == "" || (phase == "during-effect" && !effect) {
				value := next()
				if value["receipt"] == true {
					instance = value["instanceId"].(string)
				}
				if value["effect"] == true {
					effect = true
				}
			}
			if phase == "before-persistence" && effect {
				t.Fatal("effect ran before its intent could persist")
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			if release != nil {
				release()
				select {
				case <-writeDone:
					if writeErr != nil {
						t.Fatal(writeErr)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("writer did not finish")
				}
			}
			restarted := newOperationQueue(db, &documentCache{})
			expected := "lost"
			if phase == "during-effect" {
				expected = "uncertain"
			}
			waitOperation(t, restarted, instance, "restart-request", expected)
			out, err := restarted.submit(instance, "restart-request", "fixture", "fixture", false, 1, func(context.Context) (any, error) { t.Error("restart replayed effect"); return nil, nil })
			if err != nil {
				t.Fatal(err)
			}
			if value, ok := out.(operation); !ok || value.State != expected {
				t.Fatal("restart changed original identity", out)
			}
		})
	}
}
