package store

import (
	"codefactory/internal/domain"
	"context"
	"time"
)

func (t *Tx) ConversationVersion(runID string) (int64, error) {
	var seq int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT coalesce(max(seq),0) FROM events
		WHERE entity=? AND kind='run.output'
		AND json_extract(data,'$.method')='item/completed'
		AND json_extract(data,'$.params.item.type') IN ('userMessage','agentMessage')`, runID).Scan(&seq)
	return seq, err
}

// ConversationEvidence excludes tool output, other runs and internal recovery
// input. Oversized individual events are omitted, never truncated into evidence.
func (s *Store) ConversationEvidence(ctx context.Context, runID string, since time.Time) ([]domain.Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,data,at FROM events
		WHERE entity=? AND kind='run.output' AND at>?
		AND length(data)<=16384
		AND json_extract(data,'$.method')='item/completed'
		AND json_extract(data,'$.params.item.type') IN ('userMessage','agentMessage')
		AND coalesce(json_extract(data,'$.params.codeFactorySource'),'')!='question-recovery'
		ORDER BY seq DESC LIMIT 200`, runID, since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Event{}
	bytes := 0
	for rows.Next() {
		var e domain.Event
		var at string
		if err := rows.Scan(&e.Seq, &e.Data, &at); err != nil {
			return nil, err
		}
		bytes += len(e.Data)
		if bytes > 64<<10 {
			break
		}
		e.At, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, err
		}
		e.Entity, e.Kind = runID, "run.output"
		out = append(out, e)
	}
	return out, rows.Err()
}
