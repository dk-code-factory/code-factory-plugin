package factory

import (
	"codefactory/internal/app"
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type RPC interface {
	Call(context.Context, string, any, any) error
	Reply(json.RawMessage, any) error
	Reject(json.RawMessage, string) error
	Close() error
}
type Starter func(context.Context, Agent, func(codex.Message)) (RPC, error)
type Service struct {
	Store      *store.Store
	App        *app.App
	Dir        string
	Binary     string
	Now        func() time.Time
	Start      Starter
	Workspace  func(context.Context, Session) (string, error)
	mu         sync.Mutex
	clients    map[string]RPC
	ctx        context.Context
	cancel     context.CancelFunc
	closed     atomic.Bool
	configMu   sync.RWMutex
	operations sync.Map
	tickSlots  chan struct{}
	priorityMu sync.Mutex
	secretsMu  sync.Mutex
	secrets    map[string][]string
	tails      map[string]string
}

func New(ctx context.Context, a *app.App, binary string) *Service {
	ctx, cancel := context.WithCancel(ctx)
	s := &Service{Store: a.Store, App: a, Dir: a.DataDir, Binary: binary, Now: time.Now, clients: map[string]RPC{}, ctx: ctx, cancel: cancel, tickSlots: make(chan struct{}, 8), secrets: map[string][]string{}, tails: map[string]string{}}
	s.Start = nativeStarter(binary)
	s.Workspace = s.workspace
	return s
}
func bytes(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func digest(v any) string         { h := sha256.Sum256(bytes(v)); return hex.EncodeToString(h[:]) }
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var c Settings
	err := s.Store.Get(ctx, "factory-settings", "effective", &c)
	return c, err
}
func (s *Service) Init(ctx context.Context) error {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if _, err := s.Settings(ctx); errors.Is(err, store.ErrNotFound) {
		if err = s.Store.Put(ctx, "factory-settings", "effective", Defaults(), ""); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	// RPC identities belong to a process connection, never retained history.
	qs, err := store.List[Question](ctx, s.Store, "factory-question")
	if err != nil {
		return err
	}
	for _, q := range qs {
		if q.Status == "pending" || q.Status == "sending" {
			q.Status = "stale"
			if err = s.Store.Put(ctx, "factory-question", q.ID, q, ""); err != nil {
				return err
			}
		}
	}
	ss, err := store.List[Session](ctx, s.Store, "factory-session")
	if err != nil {
		return err
	}
	for _, v := range ss {
		if v.Execution == "running" || v.Execution == "starting" || v.Execution == "waiting" {
			v.Execution = "interrupted"
			v.Hold = true
			if err = s.save(ctx, v); err != nil {
				return err
			}
		}
	}
	bs, err := store.List[Batch](ctx, s.Store, "factory-batch")
	if err != nil {
		return err
	}
	for _, b := range bs {
		if b.State == "claimed" {
			b.State = "uncertain"
			if err = s.Store.Put(ctx, "factory-batch", b.ID, b, ""); err != nil {
				return err
			}
		}
	}
	ps, err := store.List[Proposal](ctx, s.Store, "factory-proposal")
	if err != nil {
		return err
	}
	for _, p := range ps {
		before := string(bytes(p))
		sanitizeNativeProposal(&p)
		if p.State == "applying" {
			p.State = "uncertain"
		}
		if string(bytes(p)) != before {
			if err = s.Store.Put(ctx, "factory-proposal", p.ID, p, ""); err != nil {
				return err
			}
		}
	}
	if err = s.Store.RedactNativeProposalEvents(ctx, sanitizeNativeProposalEvent); err != nil {
		return err
	}
	return s.projectSettings(ctx)
}

// Context is the daemon lifetime for accepted asynchronous owner operations.
func (s *Service) Context() context.Context { return s.ctx }

func (s *Service) Close() {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed.Store(true)
	for _, c := range s.clients {
		_ = c.Close()
	}
	s.clients = map[string]RPC{}
}
func (s *Service) save(ctx context.Context, v Session) error {
	return s.Store.Put(ctx, "factory-session", v.ID, v, "session.changed")
}
func (s *Service) Session(ctx context.Context, id string) (Session, error) {
	var v Session
	err := s.Store.Get(ctx, "factory-session", id, &v)
	return v, err
}
func (s *Service) Sessions(ctx context.Context) ([]Session, error) {
	return store.List[Session](ctx, s.Store, "factory-session")
}
func (s *Service) Questions(ctx context.Context) ([]Question, error) {
	return store.List[Question](ctx, s.Store, "factory-question")
}
func repository(c Settings, id string) (Repository, bool) {
	for _, r := range c.Repositories {
		if r.ID == id {
			return r, true
		}
	}
	return Repository{}, false
}
func chosen(c Settings, r Repository) Agent {
	if r.Agent.Profile != "" || r.Agent.Definition != "" {
		return r.Agent
	}
	return c.Agent
}
func quiet(c Settings, r Repository) time.Duration {
	n := c.QuietSeconds
	if r.QuietSeconds != nil {
		n = *r.QuietSeconds
	}
	return time.Duration(n) * time.Second
}
func match(rule Rule, e Event) bool {
	if rule.Disabled || (!slices.Contains(rule.Repositories, "*") && !slices.Contains(rule.Repositories, e.Repository)) || !slices.Contains(rule.Types, e.Type) {
		return false
	}
	if len(rule.Actions) > 0 && !slices.Contains(rule.Actions, e.Action) {
		return false
	}
	if rule.Author != "" && !strings.EqualFold(rule.Author, e.Context.Author) {
		return false
	}
	if rule.Draft != nil && *rule.Draft != e.Context.Draft {
		return false
	}
	for _, l := range rule.Labels {
		if !slices.Contains(e.Context.Labels, l) {
			return false
		}
	}
	return true
}
func newSession(id, kind, item, repo string, agent Agent, at time.Time) Session {
	return Session{ID: id, Kind: kind, ItemID: item, Repository: repo, Agent: agent, Execution: "idle", Pending: []Pending{}, Created: at}
}

// observe is serialized across processes by Store.Write. The facet's last value
// unifies webhook and polling observations without fabricating missing history.
type observationWriter struct {
	service        *Service
	tx             *store.Tx
	settings       Settings
	sessions       map[string]*Session
	dirty          map[string]bool
	pendingIndexes map[string]map[string]int
}

func (s *Service) observationWriter(t *store.Tx, c Settings) *observationWriter {
	return &observationWriter{service: s, tx: t, settings: c, sessions: map[string]*Session{}, dirty: map[string]bool{}, pendingIndexes: map[string]map[string]int{}}
}
func (o *observationWriter) flush() error {
	ids := []string{}
	for id := range o.dirty {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		v := o.sessions[id]
		if err := o.tx.Put("factory-session", id, v); err != nil {
			return err
		}
		if err := o.tx.Event("session.changed", id, map[string]any{"pending": len(v.Pending)}); err != nil {
			return err
		}
	}
	return nil
}
func (o *observationWriter) observe(e Event, baseline bool) error {
	s, t, c := o.service, o.tx, o.settings
	r, ok := repository(c, e.Repository)
	if !ok {
		return nil
	}
	key := e.ItemID + ":" + e.Facet
	var old struct {
		Value   string   `json:"value"`
		EventID string   `json:"eventId"`
		Action  string   `json:"action"`
		Source  string   `json:"source"`
		Rules   []string `json:"rules"`
	}
	err := t.Get("factory-observation", key, &old)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	same := old.Value == e.Value
	// A webhook can supply an action unavailable to a poll. Enrich the one
	// canonical observation and contribute only rules not already authorized.
	enrich := same && old.EventID != "" && old.Source == "poll" && e.Source == "webhook" && old.Action != e.Action
	// A real delivery supplies history unavailable to the initial baseline. It
	// must run even when intervening unobserved transitions return to that value.
	knownAfterBaseline := same && old.EventID == "" && e.Source == "webhook" && !baseline
	if same && !enrich && !knownAfterBaseline {
		return nil
	}
	rules := []string{}
	if enrich {
		e.ID = old.EventID
		var previous Event
		if err = t.Get("factory-event", e.ID, &previous); err != nil {
			return err
		}
		e.At = previous.At
		rules = append(rules, old.Rules...)
	} else {
		e.ID = app.ID()
		e.At = s.Now()
	}
	actions := []Action{}
	if !baseline && !r.Paused {
		for _, rule := range c.Rules {
			if match(rule, e) && !contains(rules, rule.ID) {
				actions = append(actions, Action{RuleID: rule.ID, EventID: e.ID, Instructions: rule.Instructions, Skills: rule.Skills})
				rules = append(rules, rule.ID)
			}
		}
	}
	eventID := e.ID
	if baseline {
		eventID = ""
	}
	if err = t.Put("factory-observation", key, map[string]any{"value": e.Value, "itemId": e.ItemID, "type": e.Type, "facet": e.Facet, "deleted": e.Action == "deleted", "observedAt": s.Now(), "eventId": eventID, "source": e.Source, "action": e.Action, "rules": rules}); err != nil {
		return err
	}
	if baseline {
		return nil
	}
	if err = t.Put("factory-event", e.ID, e); err != nil {
		return err
	}
	if len(actions) == 0 && !enrich {
		return nil
	}
	v, loaded := o.sessions[e.ItemID]
	if !loaded {
		var current Session
		err = t.Get("factory-session", e.ItemID, &current)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err == nil {
			v = &current
		}
		o.sessions[e.ItemID] = v
	}
	if v == nil {
		if len(actions) == 0 {
			return nil
		}
		current := newSession(e.ItemID, "item", e.ItemID, e.Repository, chosen(c, r), s.Now())
		if agent, ok := c.ItemAgents[e.ItemID]; ok {
			current.Agent = agent
		}
		v = &current
		o.sessions[e.ItemID] = v
	}
	indexes, indexed := o.pendingIndexes[v.ID]
	if !indexed {
		indexes = map[string]int{}
		for index, pending := range v.Pending {
			indexes[pending.Event.ID] = index
		}
		o.pendingIndexes[v.ID] = indexes
	}
	updated := false
	if index, exists := indexes[e.ID]; exists {
		pending := &v.Pending[index]
		pending.Event = e
		pending.Actions = append(pending.Actions, actions...)
		ranks := map[string]int{}
		for n, rule := range c.Rules {
			ranks[rule.ID] = n + 1
		}
		slices.SortStableFunc(pending.Actions, func(a, b Action) int { return ranks[a.RuleID] - ranks[b.RuleID] })
		updated = true
	}
	if !updated && len(actions) > 0 {
		indexes[e.ID] = len(v.Pending)
		v.Pending = append(v.Pending, Pending{Event: e, Actions: actions})
		updated = true
	}
	if !updated {
		return nil
	}
	if len(actions) > 0 {
		v.Due = s.Now().Add(quiet(c, r))
	}
	o.dirty[v.ID] = true
	return nil
}

func (s *Service) Observe(ctx context.Context, events []Event, baseline bool) error {
	ids := []string{}
	for _, e := range events {
		ids = append(ids, e.ItemID)
	}
	release := s.lockSessions(ids...)
	defer release()
	c, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	return s.Store.Write(ctx, func(t *store.Tx) error {
		o := s.observationWriter(t, c)
		for _, e := range events {
			if err := o.observe(e, baseline); err != nil {
				return err
			}
		}
		return o.flush()
	})
}

func (s *Service) Chat(ctx context.Context, itemID, text string, override *Agent) (Session, error) {
	if strings.TrimSpace(text) == "" {
		return Session{}, fmt.Errorf("message required")
	}
	release := s.lockSessions(itemID)
	defer release()
	if s.closed.Load() {
		return Session{}, context.Canceled
	}
	v, err := s.Session(ctx, itemID)
	if errors.Is(err, store.ErrNotFound) {
		c, e := s.Settings(ctx)
		if e != nil {
			return v, e
		}
		if itemID == "configuration" {
			v = newSession(itemID, "configuration", "", "", c.ConfigurationAgent, s.Now())
		} else {
			var item domain.Item
			if e = s.Store.Get(ctx, "item", itemID, &item); e != nil {
				return v, e
			}
			r, ok := repository(c, item.Repository)
			if !ok {
				return v, fmt.Errorf("repository is not configured")
			}
			a := chosen(c, r)
			if agent, ok := c.ItemAgents[itemID]; ok {
				a = agent
			}
			if override != nil {
				a = *override
			}
			v = newSession(itemID, "item", itemID, item.Repository, a, s.Now())
		}
		if err = s.save(ctx, v); err != nil {
			return v, err
		}
	} else if err != nil {
		return v, err
	} else if override != nil && *override != v.Agent {
		return v, fmt.Errorf("session is already bound to its agent")
	}
	if err = s.flush(ctx, &v, text, true); err != nil {
		v.Error = err.Error()
		if v.ThreadPending {
			v.Hold = true
			v.Execution = "interrupted"
		}
		_ = s.save(ctx, v)
	}
	return v, err
}
func (s *Service) hasQuestions(ctx context.Context, id string) (bool, error) {
	qs, err := s.sessionQuestions(ctx, id)
	if err != nil {
		return false, err
	}
	for _, q := range qs {
		if q.SessionID == id && q.Status == "pending" {
			return true, nil
		}
	}
	return false, nil
}
func (s *Service) flush(ctx context.Context, v *Session, message string, human bool) error {
	blocked, err := s.hasQuestions(ctx, v.ID)
	if err != nil {
		return err
	}
	if blocked {
		if !human {
			return nil
		}
		qs, e := s.Questions(ctx)
		if e != nil {
			return e
		}
		for _, q := range qs {
			if q.SessionID == v.ID && q.Status == "pending" && q.Blocking {
				return fmt.Errorf("answer pending questions or approvals first")
			}
		}
	}
	if !human && (v.Hold || v.Reported == "blocked" || v.Execution == "running" || v.Execution == "waiting" || v.Execution == "starting" || s.Now().Before(v.Due)) {
		return nil
	}
	if v.BatchID != "" {
		var b Batch
		if err = s.Store.Get(ctx, "factory-batch", v.BatchID, &b); err != nil {
			return err
		}
		if b.State == "claimed" || b.State == "uncertain" {
			return fmt.Errorf("delivery is uncertain; reconcile it before continuing")
		}
	}
	if len(v.Pending) == 0 && message == "" {
		return nil
	}
	client, err := s.attach(ctx, v)
	if err != nil {
		return err
	}
	b := Batch{ID: app.ID(), SessionID: v.ID, State: "claimed", Events: []Event{}, Actions: []Action{}, Message: message, At: s.Now()}
	for _, p := range v.Pending {
		b.Events = append(b.Events, p.Event)
		b.Actions = append(b.Actions, p.Actions...)
	}
	if v.ItemID != "" {
		if err = s.Store.Get(ctx, "item", v.ItemID, &b.Context); err != nil {
			return err
		}
	}
	input, err := s.input(ctx, client, *v, b)
	if err != nil {
		return err
	}
	previousExecution := v.Execution
	if previousExecution != "running" {
		v.Reported = ""
		v.Summary = ""
	}
	v.BatchID = b.ID
	v.Hold = false
	v.Pending = []Pending{}
	v.Error = ""
	if previousExecution != "running" {
		v.Execution = "starting"
	}
	if err = s.Store.Write(ctx, func(t *store.Tx) error {
		if err := t.Put("factory-batch", b.ID, b); err != nil {
			return err
		}
		return t.Put("factory-session", v.ID, v)
	}); err != nil {
		return err
	}
	method := "turn/start"
	params := map[string]any{"threadId": v.ThreadID, "input": input}
	if previousExecution == "running" {
		method = "turn/steer"
		params["expectedTurnId"] = v.TurnID
	}
	var result struct {
		Turn   struct{ ID string }
		TurnID string `json:"turnId"`
	}
	err = client.Call(ctx, method, params, &result)
	if err != nil {
		b.State = "uncertain"
		v.Hold = true
		v.Execution = "interrupted"
		v.Error = err.Error()
	} else {
		b.State = "delivered"
		b.TurnID = result.Turn.ID
		if b.TurnID == "" {
			b.TurnID = result.TurnID
		}
		if method == "turn/steer" && b.TurnID == "" {
			b.TurnID = v.TurnID
		}
		v.TurnID = b.TurnID
		v.Execution = "running"
	}
	writeErr := s.Store.Write(ctx, func(t *store.Tx) error {
		if e := t.Put("factory-batch", b.ID, b); e != nil {
			return e
		}
		if e := t.Put("factory-session", v.ID, v); e != nil {
			return e
		}
		if b.State == "delivered" && b.Message != "" {
			if e := t.Event("session.output", v.ID, map[string]string{"text": b.Message, "role": "user"}); e != nil {
				return e
			}
		}
		return t.Event("session.changed", v.ID, map[string]string{"delivery": b.State})
	})
	if err != nil {
		return err
	}
	return writeErr
}
func (s *Service) Tick(ctx context.Context) error {
	if s.closed.Load() {
		return context.Canceled
	}
	if err := s.applyPending(ctx); err != nil {
		return err
	}
	ids, err := s.Store.RunnableSessions(ctx, s.Now())
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	var errorsMu sync.Mutex
	var firstError error
	for _, candidate := range ids {
		if repo, ok := repository(settings, candidate.Repository); ok && repo.Paused {
			continue
		}
		id := candidate.ID
		if ctx.Err() != nil {
			break
		}
		select {
		case s.tickSlots <- struct{}{}:
		default:
			continue // Retained due work is reconsidered on the next tick.
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			defer func() { <-s.tickSlots }()
			if err := s.tickSession(ctx, id); err != nil {
				errorsMu.Lock()
				if firstError == nil {
					firstError = err
				}
				errorsMu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	return firstError
}
func (s *Service) tickSession(ctx context.Context, id string) error {
	release, ok := s.tryLockSession(id)
	if !ok {
		return nil
	}
	defer release()
	v, err := s.Session(ctx, id)
	if err != nil {
		return err
	}
	if len(v.Pending) == 0 {
		return nil
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	if repo, ok := repository(settings, v.Repository); ok && repo.Paused {
		return nil
	}
	if err = s.flush(ctx, &v, "", false); err != nil {
		v.Error = err.Error()
		v.Hold = true
		return s.save(ctx, v)
	}
	return nil
}
func (s *Service) Loop(ctx context.Context) {
	var scheduling sync.WaitGroup
	defer scheduling.Wait()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			pass, cancel := context.WithTimeout(ctx, 90*time.Second)
			err := s.Poll(pass)
			cancel()
			if err != nil && ctx.Err() == nil {
				_ = s.Store.Put(ctx, "factory-health", "github", map[string]any{"error": err.Error(), "at": s.Now()}, "github.error")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { <-finished }()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// A slow session cannot stop future ticks discovering newly due
			// peers. Per-session locks and shared slots bound dispatch.
			scheduling.Add(1)
			go func() {
				defer scheduling.Done()
				pass, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				_ = s.Tick(pass)
				_ = s.AssessNext(pass)
			}()
		}
	}
}
func (s *Service) Control(ctx context.Context, id, action string) (Session, error) {
	release := s.lockSessions(id)
	defer release()
	v, err := s.Session(ctx, id)
	if err != nil {
		return v, err
	}
	switch action {
	case "interrupt":
		v.Hold = true
		if err = s.save(ctx, v); err != nil {
			return v, err
		}
		if c := s.client(id); c != nil && v.TurnID != "" {
			err = c.Call(ctx, "turn/interrupt", map[string]any{"threadId": v.ThreadID, "turnId": v.TurnID}, nil)
		}
		if err == nil {
			v.Execution = "interrupted"
		}
	case "resume", "run":
		if err = s.reconcile(ctx, &v); err != nil {
			return v, err
		}
		text := "Continue the retained conversation. Inspect current state and reconcile uncertain external effects before proceeding. Do not replay previous answers or completed work."
		err = s.flush(ctx, &v, text, true)
	case "cleanup":
		err = s.cleanup(ctx, &v)
	default:
		return v, fmt.Errorf("unknown session control")
	}
	if err != nil {
		v.Error = err.Error()
	}
	saveErr := s.save(ctx, v)
	if err != nil {
		return v, err
	}
	return v, saveErr
}

// Native operations serialize only their owning conversation. Configuration
// application takes the write gate opportunistically at a safe boundary.
func (s *Service) lockOperations(ids ...string) func() {
	ids = append([]string{}, ids...)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for {
		locks := []*sync.Mutex{}
		var blocked *sync.Mutex
		for _, id := range ids {
			value, _ := s.operations.LoadOrStore(id, &sync.Mutex{})
			lock := value.(*sync.Mutex)
			if !lock.TryLock() {
				blocked = lock
				break
			}
			locks = append(locks, lock)
		}
		release := func() {
			for i := len(locks) - 1; i >= 0; i-- {
				locks[i].Unlock()
			}
		}
		if blocked == nil {
			return release
		}
		// Wait without holding another conversation's lock. A multi-item
		// observation must not relay a slow attachment's delay to its peers.
		release()
		blocked.Lock()
		blocked.Unlock()
	}

}
func (s *Service) lockSessions(ids ...string) func() {
	s.configMu.RLock()
	release := s.lockOperations(ids...)
	return func() { release(); s.configMu.RUnlock() }
}
func (s *Service) tryLockSession(id string) (func(), bool) {
	return s.tryLockSessions(id)
}

// Polling must defer a busy observation, rather than wait for native I/O while
// holding up unrelated conversations and repositories.
func (s *Service) tryLockSessions(ids ...string) (func(), bool) {
	if !s.configMu.TryRLock() {
		return nil, false
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	locks := []*sync.Mutex{}
	release := func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Unlock()
		}
		s.configMu.RUnlock()
	}
	for _, id := range ids {
		value, _ := s.operations.LoadOrStore(id, &sync.Mutex{})
		lock := value.(*sync.Mutex)
		if !lock.TryLock() {
			release()
			return nil, false
		}
		locks = append(locks, lock)
	}
	return release, true
}

func (s *Service) client(id string) RPC       { s.mu.Lock(); defer s.mu.Unlock(); return s.clients[id] }
func (s *Service) setClient(id string, c RPC) { s.mu.Lock(); defer s.mu.Unlock(); s.clients[id] = c }
func (s *Service) dropClient(id string)       { s.mu.Lock(); defer s.mu.Unlock(); delete(s.clients, id) }

func (s *Service) sessionQuestions(ctx context.Context, id string) ([]Question, error) {
	return store.Select[Question](ctx, s.Store, "factory-question", "sessionId", id, "$.params")
}
