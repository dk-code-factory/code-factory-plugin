package store

import (
	"context"
	"encoding/json"
)

// RedactNativeProposalEvents removes legacy native snapshots from event copies
// during startup. Preserve sequence, kind, entity and timestamp: this is data
// sanitization, never a new event or permission to replay an old operation.
func (s *Store) RedactNativeProposalEvents(ctx context.Context, sanitize func(json.RawMessage) (json.RawMessage, error)) error {
	return s.Write(ctx, func(t *Tx) error {
		var cursor int64
		for {
			rows, err := t.tx.QueryContext(ctx, `SELECT seq,kind,entity,data FROM events WHERE seq>? AND kind LIKE 'configuration.%' AND json_type(data,'$.native')='object' ORDER BY seq LIMIT 32`, cursor)
			if err != nil {
				return err
			}
			type entry struct {
				seq          int64
				kind, entity string
				data         json.RawMessage
			}
			page := []entry{}
			for rows.Next() {
				var e entry
				if err = rows.Scan(&e.seq, &e.kind, &e.entity, &e.data); err != nil {
					break
				}
				page = append(page, e)
			}
			if err == nil {
				err = rows.Err()
			}
			_ = rows.Close()
			if err != nil || len(page) == 0 {
				return err
			}
			for _, e := range page {
				clean, err := sanitize(e.data)
				if err != nil {
					return err
				}
				if string(clean) != string(e.data) {
					if _, err = t.tx.ExecContext(ctx, "UPDATE events SET data=? WHERE seq=?", []byte(clean), e.seq); err != nil {
						return err
					}
					t.touch("event:"+e.kind, e.entity)
				}
				cursor = e.seq
			}
		}
	})
}
