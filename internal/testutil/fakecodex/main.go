// A deliberately separate process fixture for JSON-lines protocol and lifecycle tests.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("codex-cli 0.161.0")
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	enc := json.NewEncoder(os.Stdout)
	send := func(v any) {
		if e := enc.Encode(v); e != nil {
			os.Exit(2)
		}
	}
	notify := func(method string, p any) { send(map[string]any{"method": method, "params": p}) }
	complete := func(status string) {
		notify("turn/completed", map[string]any{"turn": map[string]any{"id": "turn-1", "status": status}})
	}
	mode := os.Getenv("CF_FAKE_MODE")
	for scanner.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string
			Params map[string]json.RawMessage
			Result json.RawMessage
		}
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(3)
		}
		if path := os.Getenv("CF_FAKE_RECORD"); path != "" && (m.Method == "thread/start" || m.Method == "thread/resume" || m.Method == "turn/start") {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				os.Exit(5)
			}
			_, err = f.Write(append(append([]byte{}, scanner.Bytes()...), '\n'))
			_ = f.Close()
			if err != nil {
				os.Exit(5)
			}
		}
		reply := func(v any) { send(map[string]any{"id": m.ID, "result": v}) }
		switch m.Method {
		case "initialize":
			reply(map[string]any{"userAgent": "fixture"})
		case "initialized":
		case "thread/start", "thread/resume":
			reply(map[string]any{"thread": map[string]string{"id": "thread-1"}})
		case "skills/list":
			if mode == "slow" {
				time.Sleep(150 * time.Millisecond)
			}
			reply(map[string]any{"data": []any{}})
		case "turn/start":
			if mode == "burst" {
				for n := 0; n < 2048; n++ {
					notify("item/agentMessage/delta", map[string]any{"itemId": "burst", "delta": "output burst"})
				}
			}
			if mode == "crash" {
				os.Exit(4)
			}
			notify("turn/started", map[string]any{"turn": map[string]string{"id": "turn-1"}})
			reply(map[string]any{"turn": map[string]string{"id": "turn-1"}})
			notify("item/agentMessage/delta", map[string]string{"threadId": "thread-1", "turnId": "turn-1", "delta": "Inspecting repository"})
			if mode == "complete" || mode == "inference" {
				text := "Task complete"
				if mode == "inference" {
					text = `{"priority":1,"rationale":"Critical regression","confidence":0.9}`
				}
				notify("item/completed", map[string]any{"item": map[string]string{"type": "agentMessage", "text": text}})
				complete("completed")
				continue
			}
			if mode == "approval" {
				send(map[string]any{"id": 7, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "itemId": "item-1", "command": "git status", "availableDecisions": []string{"accept", "decline"}}})
			} else if mode != "hold" {
				send(map[string]any{"id": 7, "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "itemId": "item-1", "isBlocking": mode != "nonblocking", "questions": []any{map[string]any{"id": "scope", "header": "Scope", "question": "Which scope?", "isOther": true, "options": []any{map[string]string{"label": "Focused", "description": "One fix"}, map[string]string{"label": "Broad", "description": "All fixes"}}}}}})
			}
		case "turn/steer":
			var expected string
			_ = json.Unmarshal(m.Params["expectedTurnId"], &expected)
			if expected != "turn-1" {
				send(map[string]any{"id": m.ID, "error": map[string]any{"code": -1, "message": "wrong turn"}})
			} else {
				reply(map[string]string{"turnId": "turn-1"})
			}
		case "turn/interrupt":
			reply(map[string]any{})
			complete("interrupted")
		default:
			if len(m.Result) > 0 {
				notify("serverRequest/resolved", map[string]any{"requestId": 7})
				notify("item/completed", map[string]any{"item": map[string]string{"type": "agentMessage", "text": "Answer received. Task complete."}})
				complete("completed")
			} else {
				reply(map[string]any{})
			}
		}
	}
}
