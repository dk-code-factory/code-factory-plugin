// A deliberately separate process fixture for JSON-lines protocol and lifecycle tests.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "plugin" {
		pluginCLI()
		return
	}
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
		case "config/read":
			reply(map[string]any{"config": map[string]any{}})
		case "model/list":
			reply(map[string]any{"data": []any{}})
		case "skills/list":
			if mode == "slow" {
				time.Sleep(150 * time.Millisecond)
			}
			if root := os.Getenv("CF_FAKE_SKILL_ROOT"); root != "" {
				reply(map[string]any{"data": []any{map[string]any{"skills": []any{map[string]any{"name": "fixture-plugin-skill", "path": filepath.Join(root, "SKILL.md"), "enabled": true}}}}})
			} else {
				reply(map[string]any{"data": []any{}})
			}
		case "turn/start":
			turnNumber++
			if strings.HasPrefix(mode, "recover-") || mode == "text-question" || mode == "async-question" {
				turnID = fmt.Sprintf("turn-%d", turnNumber)
			}
			var inputs []struct{ Text string }
			_ = json.Unmarshal(m.Params["input"], &inputs)
			inputText := ""
			for _, in := range inputs {
				inputText += in.Text
			}
			if strings.Contains(string(m.Params["outputSchema"]), "needsInput") {
				reply(map[string]any{"turn": map[string]string{"id": turnID}})
				needs := strings.Contains(inputText, "Which target") || strings.Contains(inputText, "Please pick")
				text, _ := json.Marshal(map[string]any{"needsInput": needs, "question": "Which target should I use?"})
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

func pluginCLI() {
	root := os.Getenv("CF_FAKE_PLUGIN_ROOT")
	if record := os.Getenv("CF_PLUGIN_RECORD"); record != "" {
		f, err := os.OpenFile(record, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			os.Exit(5)
		}
		_ = json.NewEncoder(f).Encode(os.Args[1:])
		_ = f.Close()
	}
	if os.Getenv("CF_PLUGIN_SLOW") != "" {
		time.Sleep(400 * time.Millisecond)
	}
	args := os.Args[2:]
	if len(args) == 0 {
		os.Exit(2)
	}
	reply := func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }
	if args[0] == "marketplace" {
		if args[1] == "list" {
			reply(map[string]any{"marketplaces": []any{map[string]any{"name": "fixture", "root": root, "marketplaceSource": map[string]string{"sourceType": "local"}}}})
		} else {
			reply(map[string]string{"marketplaceName": "fixture"})
		}
		return
	}
	installed, _ := os.ReadFile(filepath.Join(root, "installed"))
	version := strings.TrimSpace(string(installed))
	var manifest struct{ Version string }
	data, _ := os.ReadFile(filepath.Join(root, ".codex-plugin/plugin.json"))
	_ = json.Unmarshal(data, &manifest)
	switch args[0] {
	case "list":
		if os.Getenv("CF_PLUGIN_BAD_JSON") != "" {
			fmt.Print(`{"installed":null,"available":null}`)
			return
		}
		row := map[string]any{"pluginId": "fixture@fixture", "name": "fixture", "marketplaceName": "fixture", "version": manifest.Version, "installed": version != "", "enabled": version != "", "source": map[string]string{"source": "local", "path": root}, "installPolicy": "AVAILABLE", "authPolicy": "ON_USE"}
		installedRows, available := []any{}, []any{}
		if version != "" {
			row["version"] = version
			installedRows = append(installedRows, row)
		} else {
			available = append(available, row)
		}
		reply(map[string]any{"installed": installedRows, "available": available})
	case "add":
		_ = os.WriteFile(filepath.Join(root, "installed"), []byte(manifest.Version), 0600)
		if os.Getenv("CF_PLUGIN_UNCERTAIN") != "" {
			fmt.Fprint(os.Stderr, "private-plugin-secret-value")
			os.Exit(4)
		}
		reply(map[string]string{"pluginId": "fixture@fixture", "version": manifest.Version, "installedPath": root})
	case "remove":
		_ = os.Remove(filepath.Join(root, "installed"))
		reply(map[string]string{"pluginId": "fixture@fixture"})
	default:
		os.Exit(2)
	}
}
