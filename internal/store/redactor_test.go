package store

import (
	"strings"
	"testing"
)

func TestRedactionAtEverySplit(t *testing.T) {
	secrets := []string{"sk-test-secret-value", "answer-secret", "aaaa"}
	for _, secret := range secrets {
		for split := 1; split < len(secret); split++ {
			a, tail := RedactStream("before "+secret[:split], "", secrets)
			b, tail := RedactStream(secret[split:]+" after", tail, secrets)
			if a+b != "before [redacted] after" || tail != "" {
				t.Fatalf("split %d of %q: %q tail %q", split, secret, a+b, tail)
			}
		}
	}
	text := "before sk-test-secret-value after"
	out, tail := "", ""
	for _, ch := range text {
		var chunk string
		chunk, tail = RedactStream(string(ch), tail, secrets)
		out += chunk
	}
	if strings.Contains(out, "sk-test-secret-value") || out != "before [redacted] after" {
		t.Fatal(out)
	}
}
