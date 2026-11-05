package factory

import (
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) managedSecrets(ctx context.Context, id string) []string {
	s.secretsMu.Lock()
	secrets := append([]string{}, s.secrets[id]...)
	s.secretsMu.Unlock()
	for _, name := range []string{"owner.token", "github-webhook-secret"} {
		if b, err := os.ReadFile(filepath.Join(s.Dir, name)); err == nil && len(b) < 65536 {
			secrets = append(secrets, strings.TrimSpace(string(b)))
		}
	}
	if s.App.Vault != nil {
		secrets = s.App.Vault.Secrets(ctx, secrets)
	}
	return secrets
}
func redactJSON(data json.RawMessage, secrets []string) json.RawMessage {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return bytes(map[string]string{"error": "invalid provider output"})
	}
	var visit func(any) any
	visit = func(v any) any {
		switch x := v.(type) {
		case string:
			for _, secret := range secrets {
				if secret != "" {
					x = strings.ReplaceAll(x, secret, "[redacted]")
				}
			}
			return x
		case map[string]any:
			for k, w := range x {
				x[k] = visit(w)
			}
			return x
		case []any:
			for i, w := range x {
				x[i] = visit(w)
			}
			return x
		default:
			return v
		}
	}
	return bytes(visit(value))
}
func (s *Service) redactOutput(ctx context.Context, id, method string, data json.RawMessage) json.RawMessage {
	secrets := s.managedSecrets(ctx, id)
	if method == "item/agentMessage/delta" {
		var p map[string]any
		if json.Unmarshal(data, &p) == nil {
			key := id + ":" + fmt.Sprint(p["itemId"])
			delta, _ := p["delta"].(string)
			s.secretsMu.Lock()
			p["delta"], s.tails[key] = store.RedactStream(delta, s.tails[key], secrets)
			s.secretsMu.Unlock()
			data = bytes(p)
		}
	}
	return redactJSON(data, secrets)
}
func (s *Service) retainSecretAnswer(ctx context.Context, q Question, answer json.RawMessage) error {
	// nativeAnswer restores private IDs as well as choices. Read the matching
	// private request before locating secret answers by their restored keys.
	if s.App.Vault != nil {
		if raw, err := s.App.Vault.Get(ctx, "factory-request:"+q.ID); err == nil {
			q.Params = json.RawMessage(raw)
		} else if err != store.ErrNotFound {
			return err
		}
	}
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	var request struct {
		Questions []struct {
			ID       string
			IsSecret bool `json:"isSecret"`
		}
	}
	if json.Unmarshal(q.Params, &request) != nil {
		return nil
	}
	var reply struct {
		Answers map[string]struct{ Answers []string }
	}
	if json.Unmarshal(answer, &reply) != nil {
		return nil
	}
	changed := false
	values := append([]string{}, s.secrets[q.SessionID]...)
	for _, question := range request.Questions {
		if question.IsSecret {
			for _, value := range reply.Answers[question.ID].Answers {
				if value != "" && !contains(values, value) {
					values = append(values, value)
					changed = true
				}
			}
		}
	}
	if !changed {
		return nil
	}
	if s.App.Vault == nil {
		return fmt.Errorf("secret-answer redaction storage unavailable")
	}
	if err := s.App.Vault.Set(ctx, "factory-redactions:"+q.SessionID, string(bytes(values))); err != nil {
		return err
	}
	s.secrets[q.SessionID] = values
	return nil
}

// Public requests are redacted. Preserve the exact offered native payload in the
// encrypted vault and translate only a uniquely identified offered choice.
func (s *Service) nativeAnswer(ctx context.Context, public Question, answer json.RawMessage) (json.RawMessage, error) {
	if s.App.Vault == nil {
		return answer, nil
	}
	raw, err := s.App.Vault.Get(ctx, "factory-request:"+public.ID)
	if err == store.ErrNotFound {
		return answer, nil
	}
	if err != nil {
		return nil, err
	}
	private := public
	private.Params = json.RawMessage(raw)
	var response map[string]any
	if err = json.Unmarshal(answer, &response); err != nil {
		return nil, err
	}
	var visible, original map[string]any
	if json.Unmarshal(public.Params, &visible) != nil || json.Unmarshal(private.Params, &original) != nil {
		return nil, fmt.Errorf("invalid retained native request")
	}
	switch public.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		choices, _ := visible["availableDecisions"].([]any)
		native, _ := original["availableDecisions"].([]any)
		matches := 0
		selectedDecision := response["decision"]
		for i, choice := range choices {
			if digest(choice) == digest(selectedDecision) && i < len(native) {
				response["decision"] = native[i]
				matches++
			}
		}
		if matches > 1 {
			return nil, fmt.Errorf("redacted approval choices are ambiguous; request fresh approval")
		}
	case "item/tool/requestUserInput":
		questions, _ := visible["questions"].([]any)
		native, _ := original["questions"].([]any)
		answers, _ := response["answers"].(map[string]any)
		for i, value := range questions {
			question, _ := value.(map[string]any)
			if i >= len(native) {
				return nil, fmt.Errorf("invalid retained native choices")
			}
			originalQuestion, _ := native[i].(map[string]any)
			id, _ := question["id"].(string)
			row, _ := answers[id].(map[string]any)
			selected, _ := row["answers"].([]any)
			options, _ := question["options"].([]any)
			originalOptions, _ := originalQuestion["options"].([]any)
			for n, selectedValue := range selected {
				matches := 0
				replacement := selectedValue
				for j, option := range options {
					visibleOption, _ := option.(map[string]any)
					if visibleOption["label"] == selectedValue && j < len(originalOptions) {
						originalOption, _ := originalOptions[j].(map[string]any)
						replacement = originalOption["label"]
						matches++
					}
				}
				if matches > 1 {
					return nil, fmt.Errorf("redacted question choices are ambiguous; request a fresh question")
				}
				selected[n] = replacement
			}
			if originalID, ok := originalQuestion["id"].(string); ok && originalID != id {
				delete(answers, id)
				answers[originalID] = row
			}
		}
	}
	result := bytes(response)
	if err = validateResponse(private, result); err != nil {
		return nil, err
	}
	return result, nil
}
