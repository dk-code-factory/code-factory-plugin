package factory

import (
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigurationCommunicationMessagesAndQuestions(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx := context.Background()
	v, err := s.Chat(ctx, "configuration", "Inspect configuration", nil)
	must(t, err)
	record := &communicationRPC{RPC: rpc}
	s.clients[v.ID] = record
	if !v.Communication || !strings.Contains(string(rpc.tools[0]), `"code_factory_communicate"`) {
		t.Fatal("new configuration thread lacks the communication tool")
	}
	call := func(id int, args Communication) {
		s.handle(v.ID, v.Epoch, codex.Message{ID: bytes(id), Method: "item/tool/call", Params: bytes(map[string]any{"threadId": v.ThreadID, "turnId": v.TurnID, "tool": "code_factory_communicate", "arguments": args})})
	}
	s.secrets[v.ID] = []string{"fixture-secret"}
	message := Communication{Kind: "message", Title: "Repositories", Body: "Five repositories. fixture-secret"}
	call(101, message)
	call(101, message) // The same native request must not display a second card.
	page, err := s.Store.SessionEvents(ctx, v.ID, 0, 100)
	must(t, err)
	data := string(bytes(page))
	if strings.Contains(data, "fixture-secret") || strings.Count(data, `"interaction"`) != 1 || !strings.Contains(data, "[redacted]") {
		t.Fatalf("communication was duplicated or not redacted: %s", data)
	}
	before := len(rpc.replies)
	call(102, Communication{Kind: "question", Title: "Repository scope", Body: "Which repositories?", Options: []CommunicationOption{{Label: "Drivers", Description: "CQL drivers only"}, {Label: "Connectors"}}})
	call(102, Communication{Kind: "question", Title: "Repository scope", Body: "Which repositories?", Options: []CommunicationOption{{Label: "Drivers", Description: "CQL drivers only"}, {Label: "Connectors"}}})
	if len(rpc.replies) != before {
		t.Fatal("question call replied before the owner answered")
	}
	questions, err := s.Questions(ctx)
	must(t, err)
	if len(questions) != 1 || questions[0].Method != "code_factory_communicate" || !questions[0].Blocking {
		t.Fatal(questions)
	}
	q := questions[0]
	current, err := s.Session(ctx, v.ID)
	must(t, err)
	if current.Execution != "waiting" {
		t.Fatal("question did not suspend the session")
	}
	if _, err = s.Chat(ctx, v.ID, "bypass question", nil); err == nil {
		t.Fatal("ordinary chat bypassed the pending question")
	}
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.QuietSeconds = 5
	proposal, err := s.Propose(ctx, &settings, nil)
	must(t, err)
	for _, invalid := range []string{`{}`, `{"answer":" "}`, `{"confirm":true}`, `{"answer":"Drivers","confirm":true}`} {
		if err = s.Answer(ctx, q.ID, json.RawMessage(invalid)); err == nil {
			t.Fatalf("invalid answer accepted: %s", invalid)
		}
	}
	must(t, s.Answer(ctx, q.ID, bytes(map[string]string{"answer": "Drivers and CDC"})))
	if len(rpc.replies) != before+1 || string(record.replyID) != "102" || !strings.Contains(string(rpc.replies[before]), `"success":true`) || !strings.Contains(string(rpc.replies[before]), `Drivers and CDC`) {
		t.Fatal("owner answer was not delivered as a dynamic tool response")
	}
	if err = s.Answer(ctx, q.ID, bytes(map[string]string{"answer": "Drivers"})); err == nil || len(rpc.replies) != before+1 {
		t.Fatal("duplicate answer replayed")
	}
	must(t, s.Store.Get(ctx, "factory-proposal", proposal.ID, &proposal))
	actual, err := s.Settings(ctx)
	must(t, err)
	if proposal.State != "proposed" || actual.QuietSeconds != 120 {
		t.Fatal("answering a communication question confirmed a configuration proposal")
	}
	page, err = s.Store.SessionEvents(ctx, v.ID, 0, 100)
	must(t, err)
	if strings.Contains(string(bytes(page)), "Drivers and CDC") {
		t.Fatal("owner answer body was copied into the transcript journal")
	}
}

type communicationRPC struct {
	RPC
	resume      json.RawMessage
	resumeCount int
	replyID     json.RawMessage
}

func (r *communicationRPC) Reply(id json.RawMessage, result any) error {
	r.replyID = append(json.RawMessage(nil), id...)
	return r.RPC.Reply(id, result)
}

func (r *communicationRPC) Call(ctx context.Context, method string, params, out any) error {
	if method == "thread/resume" {
		r.resume = bytes(params)
		r.resumeCount++
	}
	return r.RPC.Call(ctx, method, params, out)
}

func TestConfigurationCommunicationRetainsNativeCatalog(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{true: "legacy", false: "communication"}[legacy], func(t *testing.T) {
			s, rpc, _ := fixture(t)
			ctx := context.Background()
			v, err := s.Chat(ctx, "configuration", "Inspect", nil)
			must(t, err)
			s.handle(v.ID, v.Epoch, codex.Message{Method: "turn/completed", Params: bytes(map[string]any{"turn": map[string]string{"id": v.TurnID, "status": "completed"}})})
			v, err = s.Session(ctx, v.ID)
			must(t, err)
			v.Communication = !legacy
			must(t, s.save(ctx, v))
			s.dropClient(v.ID)
			record := &communicationRPC{RPC: rpc}
			s.Start = func(context.Context, Agent, func(codex.Message)) (RPC, error) { return record, nil }
			restored, err := s.Chat(ctx, "configuration", "Inspect again", nil)
			must(t, err)
			var params struct {
				ThreadID     string          `json:"threadId"`
				Instructions string          `json:"developerInstructions"`
				DynamicTools json.RawMessage `json:"dynamicTools"`
			}
			must(t, json.Unmarshal(record.resume, &params))
			expected := configurationGuidance
			if legacy {
				expected = configurationLegacyGuidance
			}
			if params.ThreadID != v.ThreadID || restored.ThreadID != v.ThreadID || params.Instructions != expected || len(rpc.tools) != 1 || record.resumeCount != 1 || params.DynamicTools != nil {
				t.Fatalf("retained catalog/history changed or incompatible guidance used: %s", record.resume)
			}
		})
	}
}

func TestConfigurationCommunicationValidationAndExpiry(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	v, err := s.Chat(ctx, "configuration", "Inspect", nil)
	must(t, err)
	for _, args := range []Communication{
		{Kind: "approve", Title: "Approve", Body: "Apply now"},
		{Kind: "message", Title: " ", Body: "Missing title"},
		{Kind: "message", Title: "Too long", Body: strings.Repeat("x", 4097)},
		{Kind: "message", Title: "Options", Body: "No question", Options: []CommunicationOption{{Label: "Yes"}}},
		{Kind: "question", Title: "Duplicate options", Body: "Choose", Options: []CommunicationOption{{Label: "A"}, {Label: "A"}}},
	} {
		if _, _, err = s.dynamic(ctx, &v, "code_factory_communicate", bytes(args), codex.Message{ID: bytes(1)}); err == nil {
			t.Fatalf("invalid interaction accepted: %+v", args)
		}
	}
	item := Session{ID: "item", Kind: "item"}
	if _, _, err = s.dynamic(ctx, &item, "code_factory_communicate", bytes(Communication{Kind: "message", Title: "Title", Body: "Body"}), codex.Message{ID: bytes(1)}); err == nil {
		t.Fatal("item session used configuration communication")
	}
	_, blocked, err := s.dynamic(ctx, &v, "code_factory_communicate", bytes(Communication{Kind: "question", Title: "Scope", Body: "Which scope?"}), codex.Message{ID: bytes(2)})
	must(t, err)
	if !blocked {
		t.Fatal("question not blocked")
	}
	questions, err := s.Questions(ctx)
	must(t, err)
	must(t, s.expire(ctx, v.ID, v.Epoch))
	if err = s.Answer(ctx, questions[0].ID, bytes(map[string]string{"answer": "Drivers"})); err == nil {
		t.Fatal("expired communication question was answered")
	}
}

func TestConfigurationLegacyCommunicationAndCompactDiff(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	v := Session{ID: "configuration", Kind: "configuration", Epoch: "legacy", TurnID: "turn"}
	_, _, err := s.dynamic(ctx, &v, "code_factory_report_status", bytes(map[string]string{"state": "done", "summary": "Five configured repositories"}), codex.Message{ID: bytes(1)})
	must(t, err)
	page, err := s.Store.SessionEvents(ctx, v.ID, 0, 100)
	must(t, err)
	events := page["events"].([]domain.Event)
	if len(events) != 1 || !strings.Contains(string(events[0].Data), `"interaction"`) {
		t.Fatal("retained legacy thread cannot display an intentional tool result")
	}
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.QuietSeconds = 7
	settings.PrioritySkill = "installed-skill"
	p, err := s.Propose(ctx, &settings, nil)
	must(t, err)
	if !strings.Contains(p.Diff, "quietSeconds\n- 120\n+ 7") || !strings.Contains(p.Diff, `+ "installed-skill"`) || strings.Contains(p.Diff, "repositories") || strings.Contains(p.Diff, "priorityInstructions") {
		t.Fatalf("diff includes unchanged settings or omits changes: %s", p.Diff)
	}
	settings.PrioritySkill = ""
	p, err = s.Propose(ctx, &settings, nil)
	must(t, err)
	if strings.Contains(p.Diff, "prioritySkill") {
		t.Fatal("unchanged optional field included")
	}
	_, _, err = s.dynamic(ctx, &v, "code_factory_report_status", bytes(map[string]string{"state": "done", "summary": strings.Repeat("x", 4097)}), codex.Message{ID: bytes(2)})
	if err == nil {
		t.Fatal("unbounded legacy result was displayed")
	}
}
