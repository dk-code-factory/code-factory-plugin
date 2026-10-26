// A deliberately separate process fixture for JSON-lines protocol and lifecycle tests.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
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
	turnID := "turn-1"
	turnNumber := 0
	repairAttempts := 0
	complete := func(status string) {
		notify("turn/completed", map[string]any{"turn": map[string]any{"id": turnID, "status": status}})
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
		if path := os.Getenv("CF_FAKE_RECORD"); path != "" && (m.Method == "initialize" || m.Method == "thread/start" || m.Method == "thread/resume" || m.Method == "turn/start") {
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
		if method := os.Getenv("CF_FAKE_GATE_METHOD"); method != "" && m.Method == method {
			path := os.Getenv("CF_FAKE_GATE_FILE")
			if err := os.WriteFile(path+".waiting", []byte(m.Method), 0600); err != nil {
				os.Exit(5)
			}
			for {
				if _, err := os.Stat(path); err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		if method := os.Getenv("CF_FAKE_SETUP_FAILURE"); method != "" && m.Method == method {
			send(map[string]any{"id": m.ID, "error": map[string]any{"code": -1, "message": "fixture retained-agent setup failure"}})
			continue
		}
		reply := func(v any) { send(map[string]any{"id": m.ID, "result": v}) }
		switch m.Method {
		case "initialize":
			reply(map[string]any{"userAgent": "fixture"})
		case "initialized":
		case "thread/start", "thread/resume":
			reply(map[string]any{"thread": map[string]string{"id": "thread-1"}})
		case "config/read":
			if os.Getenv("CF_FAKE_HELPER_FAILURE") == "startup" {
				send(map[string]any{"id": m.ID, "error": map[string]any{"code": -1, "message": "fixture classifier startup failure"}})
				continue
			}
			reply(map[string]any{"config": map[string]any{}})
		case "model/list":
			reply(map[string]any{"data": []any{}})
		case "skills/list":
			if mode == "slow" {
				time.Sleep(150 * time.Millisecond)
			}
			reply(map[string]any{"data": []any{}})
		case "turn/start":
			turnNumber++
			if strings.HasPrefix(mode, "recover-") || mode == "text-question" || mode == "async-question" || os.Getenv("CF_FAKE_COMPLETE_STEER") == "1" {
				turnID = fmt.Sprintf("turn-%d", turnNumber)
			}
			var inputs []struct{ Text string }
			_ = json.Unmarshal(m.Params["input"], &inputs)
			inputText := ""
			for _, in := range inputs {
				inputText += in.Text
			}
			if strings.Contains(inputText, "question-only recovery clarification") {
				turnID = fmt.Sprintf("clarify-%d", turnNumber)
				reply(map[string]any{"turn": map[string]string{"id": turnID}})
				notify("turn/started", map[string]any{"turn": map[string]string{"id": turnID}})
				notify("item/completed", map[string]any{"turnId": turnID, "item": map[string]any{"type": "userMessage", "content": []any{map[string]string{"type": "text", "text": inputText}}}})
				switch os.Getenv("CF_FAKE_CLARIFICATION") {
				case "native":
					send(map[string]any{"id": 7, "method": "item/tool/requestUserInput", "params": map[string]any{"turnId": turnID, "questions": []any{map[string]any{"id": "target", "question": "Which target?", "isOther": true}}}})
					continue
				case "disconnect":
					os.Exit(4)
				case "hold":
					continue
				}
				text := `{"needsInput":false,"question":""}`
				if os.Getenv("CF_FAKE_CLARIFICATION") == "question" || os.Getenv("CF_FAKE_CLARIFICATION") == "failed" {
					text = `{"needsInput":true,"question":"Which target?"}`
				}
				if os.Getenv("CF_FAKE_CLARIFICATION") == "malformed" {
					text = "Task complete (not a structured result)"
				}
				notify("item/completed", map[string]any{"item": map[string]string{"type": "agentMessage", "text": text}})
				if os.Getenv("CF_FAKE_CLARIFICATION") == "failed" {
					complete("failed")
				} else {
					complete("completed")
				}
				continue
			}
			if strings.Contains(string(m.Params["outputSchema"]), "needsInput") {
				if delay, _ := strconv.Atoi(os.Getenv("CF_FAKE_HELPER_DELAY_MS")); delay > 0 {
					time.Sleep(time.Duration(delay) * time.Millisecond)
				}
				if os.Getenv("CF_FAKE_HELPER_FAILURE") == "disconnect" {
					os.Exit(4)
				}
				if os.Getenv("CF_FAKE_HELPER_FAILURE") == "timeout" {
					time.Sleep(time.Minute)
					continue
				}
				reply(map[string]any{"turn": map[string]string{"id": turnID}})
				needs := strings.Contains(inputText, "Which target") || strings.Contains(inputText, "Please pick")
				text, _ := json.Marshal(map[string]any{"needsInput": needs, "question": "Which target should I use?"})
				if os.Getenv("CF_FAKE_HELPER_FAILURE") == "invalid" {
					text = []byte(`{"needsInput":"false","question":""}`)
				}
				if strings.Contains(inputText, "superseded completion") {
					text = []byte(`{"needsInput":false,"question":""}`)
				}
				notify("item/completed", map[string]any{"item": map[string]string{"id": "helper", "type": "agentMessage", "text": string(text)}})
				complete("completed")
				continue
			}
			if strings.HasPrefix(mode, "recover-") || mode == "text-question" || mode == "async-question" {
				notify("turn/started", map[string]any{"turn": map[string]string{"id": turnID}})
				reply(map[string]any{"turn": map[string]string{"id": turnID}})
				notify("item/completed", map[string]any{"turnId": turnID, "item": map[string]any{"id": "input-" + turnID, "type": "userMessage", "content": []any{map[string]string{"type": "text", "text": inputText}}}})
				if mode == "async-question" && turnNumber == 1 {
					notify("item/completed", map[string]any{"turnId": turnID, "item": map[string]any{"id": "async-1", "type": "agentMessage", "delivery": "async", "text": "Which target?", "questions": []any{map[string]any{"title": "Which target?", "options": []string{"One", "Two"}}}}})
					complete("completed")
					continue
				}
				if strings.Contains(inputText, "question-only recovery") {
					repairAttempts++
					if mode == "recover-first" || mode == "recover-second" && repairAttempts == 2 {
						send(map[string]any{"id": 7, "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "thread-1", "turnId": turnID, "itemId": "repaired", "isBlocking": true, "questions": []any{map[string]any{"id": "target", "header": "Target", "question": "Which target?", "isOther": true}}}})
						continue
					}
				}
				text := "Task complete"
				if turnNumber == 1 || strings.Contains(inputText, "question-only recovery") {
					text = "Which target should I use?"
				}
				notify("item/completed", map[string]any{"item": map[string]string{"id": "final-" + turnID, "type": "agentMessage", "text": text}})
				complete("completed")
				continue
			}
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
			if expected != turnID {
				send(map[string]any{"id": m.ID, "error": map[string]any{"code": -1, "message": "wrong turn"}})
			} else {
				reply(map[string]string{"turnId": turnID})
				if os.Getenv("CF_FAKE_COMPLETE_STEER") == "1" {
					notify("item/completed", map[string]any{"item": map[string]string{"type": "agentMessage", "text": "Answer received."}})
					complete("completed")
				}
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
