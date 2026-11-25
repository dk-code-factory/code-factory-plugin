package factory

import (
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WebhookStatus contains no callback credentials or raw provider diagnostics.
type WebhookStatus struct {
	Repository string    `json:"repository"`
	State      string    `json:"state"`
	Error      string    `json:"error,omitempty"`
	At         time.Time `json:"at,omitzero"`
	RetryAt    time.Time `json:"retryAt,omitzero"`
}
type webhookRecord struct {
	WebhookStatus
	Input  string `json:"input"`
	HookID int64  `json:"hookId,omitempty"`
}

// ReadWebhookSecret is shared by provisioning and signature verification.
func ReadWebhookSecret(dir string) (string, error) {
	path := filepath.Join(dir, "github-webhook-secret")
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 {
		return "", fmt.Errorf("webhook secret must be an owner-only regular file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	secret := strings.TrimSpace(string(b))
	if secret == "" {
		return "", fmt.Errorf("webhook secret is empty")
	}
	return secret, nil
}

func ensureWebhookSecret(dir string) (string, error) {
	secret, err := ReadWebhookSecret(dir)
	if !errors.Is(err, os.ErrNotExist) {
		return secret, err
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return "", err
	}
	secret = hex.EncodeToString(random[:])
	file, err := os.CreateTemp(dir, ".webhook-secret-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if _, err = file.WriteString(secret + "\n"); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	// Publish complete bytes without replacing another process's secret.
	if err = os.Link(file.Name(), filepath.Join(dir, "github-webhook-secret")); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	return ReadWebhookSecret(dir)
}

func (s *Service) webhookCallback() (string, error) {
	b, err := os.ReadFile(filepath.Join(s.Dir, "tailscale.json"))
	var cfg struct {
		PublicURL string `json:"publicUrl"`
	}
	if err != nil || json.Unmarshal(b, &cfg) != nil {
		return "", fmt.Errorf("Configure the public gateway to enable automatic webhooks")
	}
	u, err := url.Parse(cfg.PublicURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("Configure a valid HTTPS public gateway to enable automatic webhooks")
	}
	return strings.TrimRight(u.String(), "/") + "/github/webhook", nil
}

// ReconcileWebhooks runs separately from API requests and inbox polling. A setup
// failure is retained per repository and never disables the existing API scans.
func (s *Service) ReconcileWebhooks(ctx context.Context) error {
	// Bound authentication and the complete paginated reconciliation together,
	// rather than relying on the transport's per-HTTP-request timeout. A stalled
	// CLI authentication must release the shared provider for API polling.
	return s.reconcileWebhooks(ctx, 30*time.Second)
}

func (s *Service) reconcileWebhooks(ctx context.Context, attemptTimeout time.Duration) error {
	release := s.lockOperations("webhook-provisioning")
	defer release()
	c, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	if len(c.Repositories) == 0 {
		return nil
	}
	callback, callbackErr := s.webhookCallback()
	var secret string
	if callbackErr == nil {
		secret, err = ensureWebhookSecret(s.Dir)
		if err != nil {
			callbackErr = fmt.Errorf("Webhook secret is unavailable; restore an owner-only secret file")
		}
	}
	input := digest([]string{callback, secret, fmt.Sprint(callbackErr)})
	for _, repo := range c.Repositories {
		if err = ctx.Err(); err != nil {
			return err
		}
		if repo.Paused {
			continue
		}
		var old webhookRecord
		if err = s.Store.Get(ctx, "factory-webhook", repo.ID, &old); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if old.Input == input && s.Now().Before(old.RetryAt) {
			continue
		}
		record := webhookRecord{WebhookStatus: WebhookStatus{Repository: repo.ID, State: "polling", At: s.Now(), RetryAt: s.Now().Add(15 * time.Minute)}, Input: input}
		failure := callbackErr
		if failure == nil {
			if github := providers.LiveGitHub(s.App.GitHub); github != nil {
				attempt, cancel := context.WithTimeout(ctx, attemptTimeout)
				record.HookID, failure = github.EnsureRepositoryWebhook(attempt, repo.ID, callback, secret)
				cancel()
			} else {
				failure = fmt.Errorf("GitHub webhook provisioning is unavailable")
			}
		}
		if failure == nil {
			record.State, record.RetryAt = "configured", s.Now().Add(24*time.Hour)
		} else {
			record.Error = "Webhook setup failed; API polling remains active. Check GitHub CLI authentication and repository Webhooks write permission."
			var request *providers.RequestError
			if errors.As(failure, &request) && request.Status != 0 {
				record.Error = fmt.Sprintf("Webhook setup failed (GitHub HTTP %d); API polling remains active. Check repository access and Webhooks write permission.", request.Status)
			}
			if callbackErr != nil {
				record.Error = callbackErr.Error() + "; API polling remains active."
			}
			var retry interface{ RetryTime() time.Time }
			if errors.As(failure, &retry) && retry.RetryTime().After(record.RetryAt) {
				record.RetryAt = retry.RetryTime()
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = s.Store.Put(ctx, "factory-webhook", repo.ID, record, "webhook.changed"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) webhookLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.ReconcileWebhooks(ctx); err != nil && ctx.Err() == nil {
			_ = s.Store.Put(ctx, "factory-health", "webhooks", map[string]any{"error": "Webhook setup state could not be reconciled; API polling remains active", "at": s.Now()}, "webhook.error")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
