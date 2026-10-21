package store

import (
	"context"
	"strings"
)

func (v *Vault) Secrets(ctx context.Context, extra []string) []string {
	out := append([]string{}, extra...)
	for _, name := range []string{"jira", "tunnel-key"} {
		if secret, e := v.Get(ctx, name); e == nil && secret != "" {
			out = append(out, secret)
		}
	}
	return out
}

// RedactStream withholds any suffix that could start a known secret, so a
// credential split over arbitrary delta messages never reaches the journal.
// Callers keep independent tails for independent output streams.
func RedactStream(text, tail string, secrets []string) (string, string) {
	text = tail + text
	cut := len(text)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		// A full match at the end is replaced now, never withheld.
		for n := 1; n < len(secret) && n <= len(text); n++ {
			if strings.HasSuffix(text, secret[:n]) && len(text)-n < cut {
				cut = len(text) - n
			}
		}
	}
	// Keep an entire match that overlaps the candidate tail in the output region.
	for _, secret := range secrets {
		if secret != "" {
			for offset := 0; offset < len(text); {
				i := strings.Index(text[offset:], secret)
				if i < 0 {
					break
				}
				i += offset
				if i < cut && i+len(secret) > cut {
					cut = i + len(secret)
				}
				offset = i + len(secret)
			}
		}
	}
	tail = text[cut:]
	text = text[:cut]
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return text, tail
}
