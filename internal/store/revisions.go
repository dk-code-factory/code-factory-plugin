package store

import "encoding/json"

// Revisions describe locally committed state, independently of UI notifications.
// Immutable publication makes dispatch reads independent of the database/writer lock.
type revisionState struct {
	serial uint64
	root   *revisionNode
}

func (t *Tx) touch(kind, id string) {
	if kind == "plugin-api-operation" {
		return
	}
	if t.revisionKeys == nil {
		t.revisionKeys = map[string]bool{}
	}
	t.revisionKeys[kind] = true
	t.revisionKeys[kind+"/"+id] = true
}

// A persistent AVL tree shares untouched branches with pinned read snapshots.
// Each commit allocates O(changed keys * log retained keys), not a full index copy.
type revisionNode struct {
	key         string
	value       uint64
	height      int
	left, right *revisionNode
}

func revisionHeight(n *revisionNode) int {
	if n == nil {
		return 0
	}
	return n.height
}
func revisionBranch(key string, value uint64, left, right *revisionNode) *revisionNode {
	return &revisionNode{key: key, value: value, height: 1 + max(revisionHeight(left), revisionHeight(right)), left: left, right: right}
}
func revisionRotateLeft(n *revisionNode) *revisionNode {
	r := n.right
	return revisionBranch(r.key, r.value, revisionBranch(n.key, n.value, n.left, r.left), r.right)
}
func revisionRotateRight(n *revisionNode) *revisionNode {
	l := n.left
	return revisionBranch(l.key, l.value, l.left, revisionBranch(n.key, n.value, l.right, n.right))
}
func revisionSet(n *revisionNode, key string, value uint64) *revisionNode {
	if n == nil {
		return revisionBranch(key, value, nil, nil)
	}
	left, right := n.left, n.right
	if key < n.key {
		left = revisionSet(left, key, value)
	} else if key > n.key {
		right = revisionSet(right, key, value)
	} else {
		return revisionBranch(key, value, left, right)
	}
	next := revisionBranch(n.key, n.value, left, right)
	if revisionHeight(left)-revisionHeight(right) > 1 {
		if key > left.key {
			next = revisionBranch(next.key, next.value, revisionRotateLeft(left), right)
		}
		return revisionRotateRight(next)
	}
	if revisionHeight(right)-revisionHeight(left) > 1 {
		if key < right.key {
			next = revisionBranch(next.key, next.value, left, revisionRotateRight(right))
		}
		return revisionRotateLeft(next)
	}
	return next
}
func (s *Store) publishRevisions(keys map[string]bool) {
	if len(keys) == 0 {
		return
	}
	old := s.revisions.Load()
	next := &revisionState{serial: 1}
	if old != nil {
		next.serial = old.serial + 1
		next.root = old.root
	}
	for key := range keys {
		next.root = revisionSet(next.root, key, next.serial)
	}
	s.revisions.Store(next)
}

// Revision is the latest commit touching any dependency. Selected keys isolate
// transcript/item traffic; journal metadata has no read-projection dependencies.
func (s *Store) Revision(keys ...string) uint64 {
	r := s.revisions.Load()
	var revision uint64
	if r != nil {
		for _, key := range keys {
			for n := r.root; n != nil; {
				if key < n.key {
					n = n.left
				} else if key > n.key {
					n = n.right
				} else {
					revision = max(revision, n.value)
					break
				}
			}
		}
	}
	return revision
}

func (t *Tx) touchRelationships(kind, id string, next []byte) {
	if kind != "relationship" {
		return
	}
	var old []byte
	_ = t.tx.QueryRowContext(t.ctx, "SELECT data FROM entities WHERE kind=? AND id=?", kind, id).Scan(&old)
	for _, data := range [][]byte{old, next} {
		t.touchRelationshipEndpoints(data)
	}
}

func (t *Tx) touchRelationshipEndpoints(data []byte) {
	var link struct {
		Source json.RawMessage
		Target json.RawMessage
	}
	if json.Unmarshal(data, &link) != nil {
		return
	}
	// A malformed peer cannot suppress invalidation of the valid endpoint.
	for _, raw := range []json.RawMessage{link.Source, link.Target} {
		var id string
		if json.Unmarshal(raw, &id) == nil && id != "" {
			t.touch("related", id)
		}
	}
}
