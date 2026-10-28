// A deliberately separate process fixture for JSON-lines protocol and lifecycle tests.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	if path := os.Getenv("CF_FAKE_PID_RECORD"); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			os.Exit(5)
		}
		fmt.Fprintln(f, os.Getpid())
		_ = f.Close()
	}

	if path := os.Getenv("CF_FAKE_ARGS_RECORD"); path != "" && len(os.Args) > 1 && os.Args[1] == "app-server" {
		b, _ := json.Marshal(os.Args[1:])
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			os.Exit(5)
		}
		_, err = f.Write(append(b, '\n'))
		_ = f.Close()
		if err != nil {
			os.Exit(5)
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "plugin" {
		pluginCLI()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		version := os.Getenv("CF_FAKE_VERSION")
		if version == "" {
			version = "0.161.0"
		}
		fmt.Println("codex-cli " + version)
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	enc := json.NewEncoder(os.Stdout)
	var sendMu sync.Mutex
	send := func(v any) {
		sendMu.Lock()
		defer sendMu.Unlock()
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
	var activeTool *exec.Cmd
	commands := map[string]*exec.Cmd{}
	var commandMu sync.Mutex
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
		if delay, _ := strconv.Atoi(os.Getenv("CF_FAKE_OPERATION_DELAY_MS")); delay > 0 {
			start := time.Now()
			time.Sleep(time.Duration(delay) * time.Millisecond)
			if path := os.Getenv("CF_FAKE_DELAY_RECORD"); path != "" {
				f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
				if err == nil {
					fmt.Fprintf(f, "%s %d\n", m.Method, time.Since(start).Microseconds())
					f.Close()
				}
			}
		}
		if method := os.Getenv("CF_FAKE_BLOCK_RPC"); method != "" && m.Method == method {
			if err := os.WriteFile(os.Getenv("CF_FAKE_RPC_ENTERED"), []byte(method), 0600); err != nil {
				os.Exit(5)
			}
			for {
				if _, err := os.Stat(os.Getenv("CF_FAKE_RPC_RELEASE")); err == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		if path := os.Getenv("CF_FAKE_RECORD"); path != "" && (m.Method == "initialize" || m.Method == "thread/start" || m.Method == "thread/resume" || m.Method == "turn/start" || ((os.Getenv("CF_FAKE_REVIEW_RESPONSES") != "" || mode == "retained-tool") && (m.Method == "command/exec" || m.Method == "command/exec/terminate" || m.Method == "turn/interrupt"))) {
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
			if m.Method == "thread/resume" && mode == "resume-missing" {
				send(map[string]any{"id": m.ID, "error": map[string]any{"code": -1, "message": "retained thread unavailable"}})
				continue
			}
			thread := map[string]any{"id": "thread-1"}
			if m.Method == "thread/resume" {
				if status := os.Getenv("CF_FAKE_RESUME_STATUS"); status != "" {
					thread["turns"] = []any{map[string]any{"id": "turn-1", "status": status}}
				}
			}
			reply(map[string]any{"thread": thread})
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
			if root := os.Getenv("CF_FAKE_SKILL_ROOT"); root != "" {
				name := os.Getenv("CF_FAKE_SKILL_NAME")
				if name == "" {
					name = "fixture-plugin-skill"
				}
				reply(map[string]any{"data": []any{map[string]any{"skills": []any{map[string]any{"name": name, "path": filepath.Join(root, "SKILL.md"), "enabled": true}}}}})
			} else {
				reply(map[string]any{"data": []any{}})
			}

		case "command/exec":
			if os.Getenv("CF_FAKE_REVIEW_RESPONSES") == "" {
				reply(map[string]any{})
				continue
			}
			var argv []string
			var env map[string]string
			var cwd string
			_ = json.Unmarshal(m.Params["command"], &argv)
			_ = json.Unmarshal(m.Params["env"], &env)
			_ = json.Unmarshal(m.Params["cwd"], &cwd)
			if len(argv) == 0 {
				os.Exit(6)
			}
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Dir = cwd
			cmd.Env = os.Environ()
			for key, value := range env {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
			var processID string
			_ = json.Unmarshal(m.Params["processId"], &processID)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			var output bytes.Buffer
			cmd.Stdout = &output
			if err := cmd.Start(); err != nil {
				reply(map[string]any{"exitCode": 1, "stdout": "", "stderr": ""})
				continue
			}
			commandMu.Lock()
			commands[processID] = cmd
			commandMu.Unlock()
			responseID := append(json.RawMessage{}, m.ID...)
			go func() {
				err := cmd.Wait()
				status := 0
				if err != nil {
					status = 1
				}
				commandMu.Lock()
				delete(commands, processID)
				commandMu.Unlock()
				send(map[string]any{"id": responseID, "result": map[string]any{"exitCode": status, "stdout": output.String(), "stderr": ""}})
			}()
		case "command/exec/terminate":
			var processID string
			_ = json.Unmarshal(m.Params["processId"], &processID)
			commandMu.Lock()
			cmd := commands[processID]
			commandMu.Unlock()
			if cmd == nil {
				send(map[string]any{"id": m.ID, "error": map[string]any{"code": -1, "message": "unknown process"}})
				continue
			}
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			reply(map[string]any{})
		case "turn/start":
			turnNumber++
			if strings.HasPrefix(mode, "recover-") || mode == "text-question" || strings.HasPrefix(mode, "async-question") || os.Getenv("CF_FAKE_COMPLETE_STEER") == "1" {
				turnID = fmt.Sprintf("turn-%d", turnNumber)
			}
			var inputs []struct{ Text string }
			_ = json.Unmarshal(m.Params["input"], &inputs)
			inputText := ""
			for _, in := range inputs {
				inputText += in.Text
			}

			if mode == "retained-tool" {
				activeTool = exec.Command("python3", "-c", `import os,signal,sys,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); open(sys.argv[1],'w').write(str(os.getpid())); time.sleep(3600)`, os.Getenv("CF_FAKE_TOOL_PID"))
				activeTool.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
				if err := activeTool.Start(); err != nil {
					os.Exit(7)
				}
				reply(map[string]any{"turn": map[string]string{"id": turnID}})
				continue
			}
			if root := os.Getenv("CF_FAKE_REVIEW_RESPONSES"); root != "" && strings.HasPrefix(inputText, "Code Factory retained review stage: ") {
				stage, _, _ := strings.Cut(strings.TrimPrefix(inputText, "Code Factory retained review stage: "), "\n")
				if stage != "preparation" && stage != "material-choice" && stage != "skill-review" && stage != "completion" {
					os.Exit(6)
				}
				response, err := os.ReadFile(filepath.Join(root, stage+".json"))
				if err != nil {
					os.Exit(6)
				}
				reply(map[string]any{"turn": map[string]string{"id": turnID}})
				notify("item/completed", map[string]any{"turnId": turnID, "item": map[string]string{"type": "agentMessage", "text": string(response)}})
				complete("completed")
				continue
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
			if strings.Contains(string(m.Params["outputSchema"]), "decisions") {
				reply(map[string]any{"turn": map[string]string{"id": turnID}})
				text := os.Getenv("CF_FAKE_ATTENTION_DECISIONS")
				if text == "" {
					var data struct {
						Evidence []struct {
							Seq        int64
							Role, Text string
						}
					}
					if i := strings.Index(inputText, "\n{"); i >= 0 {
						_ = json.Unmarshal([]byte(inputText[i+1:]), &data)
					}
					decisions := []map[string]any{}
					for _, user := range data.Evidence {
						if user.Role != "user" {
							continue
						}
						for _, agent := range data.Evidence {
							if agent.Role != "agent" || agent.Seq <= user.Seq {
								continue
							}
							ids := []string{}
							if strings.Contains(user.Text, "Q1: use stable IDs") && agent.Text == "Q1 accepted. Q2 remains open." {
								ids = []string{"q1"}
							}
							if strings.Contains(user.Text, "Q1: stable IDs; Q2: include PREPARE") && agent.Text == "Both decisions accepted. Review published." {
								ids = []string{"q1", "q2"}
							}
							for _, id := range ids {
								decisions = append(decisions, map[string]any{"questionId": id, "status": "resolved", "userEvent": user.Seq, "agentEvent": agent.Seq, "userQuote": user.Text, "agentQuote": agent.Text})
							}
						}
					}
					b, _ := json.Marshal(map[string]any{"decisions": decisions})
					text = string(b)
				}
				notify("item/completed", map[string]any{"item": map[string]string{"id": "attention-helper", "type": "agentMessage", "text": text}})
				complete("completed")
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
			if strings.HasPrefix(mode, "recover-") || mode == "text-question" || strings.HasPrefix(mode, "async-question") {
				notify("turn/started", map[string]any{"turn": map[string]string{"id": turnID}})
				reply(map[string]any{"turn": map[string]string{"id": turnID}})
				notify("item/completed", map[string]any{"turnId": turnID, "item": map[string]any{"id": "input-" + turnID, "type": "userMessage", "content": []any{map[string]string{"type": "text", "text": inputText}}}})
				if strings.HasPrefix(mode, "async-question") && turnNumber == 1 {
					questions := []any{map[string]any{"title": "Which target?", "options": []string{"One", "Two"}}}
					if mode == "async-question-multi" {
						questions = []any{map[string]any{"title": "Which ID contract?"}, map[string]any{"title": "Which PREPARE scope?"}}
					}
					notify("item/completed", map[string]any{"turnId": turnID, "item": map[string]any{"id": "async-1", "type": "agentMessage", "delivery": "async", "text": "Which target?", "questions": questions}})
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
				if mode == "async-question-multi" {
					if strings.Contains(inputText, "Q1: use stable IDs") {
						text = "Q1 accepted. Q2 remains open."
					}
					if strings.Contains(inputText, "Q1: stable IDs; Q2: include PREPARE") {
						text = "Both decisions accepted. Review published."
					}
				}
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
				if mode == "async-question-multi" {
					if strings.Contains(inputText, "Q1: use stable IDs") {
						text = "Q1 accepted. Q2 remains open."
					}
					if strings.Contains(inputText, "Q1: stable IDs; Q2: include PREPARE") {
						text = "Both decisions accepted. Review published."
					}
				}
				if mode == "inference" {
					text = os.Getenv("CF_FAKE_INFERENCE")
					if text == "" {
						text = `{"priority":1,"rationale":"Critical regression","confidence":0.9}`
					}
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
			if activeTool != nil {
				_ = syscall.Kill(-activeTool.Process.Pid, syscall.SIGKILL)
				_ = activeTool.Wait()
				activeTool = nil
			}
			if entered := os.Getenv("CF_FAKE_INTERRUPT_ENTERED"); entered != "" {
				params, _ := json.Marshal(m.Params)
				if err := os.WriteFile(entered, params, 0600); err != nil {
					os.Exit(5)
				}
				for {
					if _, err := os.Stat(os.Getenv("CF_FAKE_INTERRUPT_RELEASE")); err == nil {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
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
			if os.Getenv("CF_PLUGIN_GITMARKETS") != "" {
				reply(map[string]any{"marketplaces": []any{map[string]any{"name": "unavailable", "root": root, "marketplaceSource": map[string]string{"sourceType": "git"}}, map[string]any{"name": "fixture", "root": root, "marketplaceSource": map[string]string{"sourceType": "git"}}}})
			} else {
				reply(map[string]any{"marketplaces": []any{map[string]any{"name": "fixture", "root": root, "marketplaceSource": map[string]string{"sourceType": "local"}}}})
			}
		} else {
			if os.Getenv("CF_PLUGIN_GITMARKETS") != "" && args[len(args)-1] == "unavailable" {
				fmt.Fprint(os.Stderr, "private-marketplace-secret")
				os.Exit(3)
			}
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
