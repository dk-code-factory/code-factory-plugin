// Package codex isolates the Codex 0.161.0 JSON-lines app-server protocol.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("Codex RPC %d: %s", e.Code, e.Message) }

type Client struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	mu         sync.Mutex
	writeMu    sync.Mutex
	seq        int
	pending    map[string]chan Message
	done       chan struct{}
	queueMu    sync.Mutex
	queue      []Message
	queueBytes int
	wake       chan struct{}
	readerDone bool
	once       sync.Once
	OnMessage  func(Message)
}

func Version(ctx context.Context, binary string) (string, error) {
	b, e := exec.CommandContext(ctx, binary, "--version").Output()
	if e != nil {
		return "", fmt.Errorf("Codex not installed")
	}
	v := strings.TrimSpace(string(b))
	if v != "codex-cli 0.161.0" {
		return v, fmt.Errorf("unsupported Codex version %s; tested version is 0.161.0", v)
	}
	return v, nil
}
func Start(ctx context.Context, binary string, extra []string, onMessage func(Message)) (*Client, error) {
	cmd := exec.CommandContext(ctx, binary, append([]string{"app-server"}, extra...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	cmd.Stderr = io.Discard
	in, e := cmd.StdinPipe()
	if e != nil {
		return nil, e
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		return nil, fmt.Errorf("could not start Codex app-server")
	}
	c := &Client{cmd: cmd, stdin: in, pending: map[string]chan Message{}, done: make(chan struct{}), OnMessage: onMessage, wake: make(chan struct{}, 1)}
	go c.read(out)
	go c.dispatch()

	return c, nil
}
func (c *Client) read(r io.Reader) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64<<10), 8<<20)
	for s.Scan() {
		var m Message
		if json.Unmarshal(s.Bytes(), &m) != nil {
			break
		}
		if m.Method == "" && len(m.ID) > 0 {
			c.mu.Lock()
			ch := c.pending[string(m.ID)]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
		} else {
			if !c.enqueue(m) {
				break
			}
		}
	}
	_ = c.Close()
	_ = c.cmd.Wait()
	c.queueMu.Lock()
	c.readerDone = true
	c.queueMu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// The reader must keep routing RPC responses while a notification handler waits
// for one. Bound queued bytes; overload disconnects explicitly instead of hanging.
func (c *Client) enqueue(m Message) bool {
	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	size := 256 + len(m.Params) + len(m.Method) + len(m.ID)
	if c.queueBytes+size > 32<<20 {
		return false
	}
	c.queue = append(c.queue, m)
	c.queueBytes += size
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return true
}
func (c *Client) dispatch() {
	for {
		c.queueMu.Lock()
		if len(c.queue) > 0 {
			m := c.queue[0]
			c.queue[0] = Message{}
			c.queue = c.queue[1:]
			c.queueBytes -= 256 + len(m.Params) + len(m.Method) + len(m.ID)
			c.queueMu.Unlock()
			if c.OnMessage != nil {
				c.OnMessage(m)
			}
			continue
		}
		finished := c.readerDone
		c.queueMu.Unlock()
		if finished {
			if c.OnMessage != nil {
				c.OnMessage(Message{Method: "process/closed"})
			}
			return
		}
		<-c.wake
	}
}
func (c *Client) send(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return errors.New("Codex process disconnected")
	default:
	}
	return json.NewEncoder(c.stdin).Encode(v)
}
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	c.seq++
	id := "cf-" + strconv.Itoa(c.seq)
	key := strconv.Quote(id)
	ch := make(chan Message, 1)
	c.pending[key] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, key); c.mu.Unlock() }()
	if e := c.send(map[string]any{"id": id, "method": method, "params": params}); e != nil {
		return e
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errors.New("Codex process disconnected")
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	}
}
func (c *Client) Initialize(ctx context.Context) error {
	if e := c.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "code_factory", "version": "0.1.0"}, "capabilities": map[string]any{"experimentalApi": true}}, nil); e != nil {
		return e
	}
	return c.send(map[string]any{"method": "initialized"})
}
func (c *Client) Reply(id json.RawMessage, result any) error {
	return c.send(map[string]any{"id": id, "result": result})
}
func (c *Client) Reject(id json.RawMessage, message string) error {
	return c.send(map[string]any{"id": id, "error": map[string]any{"code": -32601, "message": message}})
}
func (c *Client) Close() error {
	var e error
	c.once.Do(func() { close(c.done); e = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL) })
	return e
}

type Status struct {
	Version       string `json:"version"`
	Compatible    bool   `json:"compatible"`
	Authenticated bool   `json:"authenticated"`
	Error         string `json:"error,omitempty"`
}

func Inspect(ctx context.Context, binary string) Status {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status := Status{}
	v, e := Version(ctx, binary)
	status.Version = v
	if e != nil {
		status.Error = e.Error()
		return status
	}
	status.Compatible = true
	c, e := Start(ctx, binary, nil, nil)
	if e != nil {
		status.Error = e.Error()
		return status
	}
	defer c.Close()
	if e = c.Initialize(ctx); e != nil {
		status.Error = "Codex handshake failed"
		return status
	}
	var account struct {
		Account  *struct{ Type string }
		Requires bool `json:"requiresOpenaiAuth"`
	}
	if e = c.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &account); e != nil {
		status.Error = "Could not check Codex authentication"
		return status
	}
	status.Authenticated = !account.Requires || account.Account != nil
	if !status.Authenticated {
		status.Error = "Codex sign-in is required"
	}
	return status
}
