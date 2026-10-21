package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Vault struct {
	s *Store
	a cipher.AEAD
}

func NewVault(s *Store, dir string) (*Vault, error) {
	p := filepath.Join(dir, "vault.key")
	k, e := os.ReadFile(p)
	if os.IsNotExist(e) {
		k = make([]byte, 32)
		if _, e = rand.Read(k); e != nil {
			return nil, e
		}
		e = os.WriteFile(p, k, 0600)
	}
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(p, 0600); e != nil {
		return nil, e
	}
	c, e := aes.NewCipher(k)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(c)
	return &Vault{s: s, a: a}, e
}
func (v *Vault) Set(ctx context.Context, name, secret string) error {
	n := make([]byte, v.a.NonceSize())
	if _, e := rand.Read(n); e != nil {
		return e
	}
	b := v.a.Seal(n, n, []byte(secret), []byte(name))
	return v.s.Put(ctx, "secret", name, base64.StdEncoding.EncodeToString(b), "")
}
func (v *Vault) Get(ctx context.Context, name string) (string, error) {
	var str string
	if e := v.s.Get(ctx, "secret", name, &str); e != nil {
		return "", e
	}
	b, e := base64.StdEncoding.DecodeString(str)
	if e != nil || len(b) < v.a.NonceSize() {
		return "", fmt.Errorf("invalid secret")
	}
	p, e := v.a.Open(nil, b[:v.a.NonceSize()], b[v.a.NonceSize():], []byte(name))
	return string(p), e
}

// RedactJSON removes service-managed credentials from data before journaling or exposing it.
func (v *Vault) RedactJSON(ctx context.Context, b json.RawMessage, extra []string) json.RawMessage {
	secrets := append([]string{}, extra...)
	for _, name := range []string{"jira", "tunnel-key"} {
		if secret, e := v.Get(ctx, name); e == nil && secret != "" {
			secrets = append(secrets, secret)
		}
	}
	if len(secrets) == 0 {
		return b
	}
	var value any
	if json.Unmarshal(b, &value) != nil {
		return b
	}
	var visit func(any) any
	visit = func(x any) any {
		switch z := x.(type) {
		case string:
			for _, secret := range secrets {
				if secret != "" {
					z = strings.ReplaceAll(z, secret, "[redacted]")
				}
			}
			return z
		case []any:
			for i := range z {
				z[i] = visit(z[i])
			}
		case map[string]any:
			for k, v := range z {
				z[k] = visit(v)
			}
		}
		return x
	}
	out, e := json.Marshal(visit(value))
	if e != nil {
		return b
	}
	return out
}
