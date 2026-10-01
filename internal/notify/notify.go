// Package notify sends deployment outcomes to people.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/manaskumar3003/alror-cli/internal/domain"
)

// Notifier is told about terminal outcomes (promoted or rolled back).
type Notifier interface {
	Notify(ctx context.Context, d *domain.Deployment, summary string) error
}

// Nop discards notifications.
type Nop struct{}

func (Nop) Notify(context.Context, *domain.Deployment, string) error { return nil }

// Slack posts to an incoming webhook.
type Slack struct{ Webhook string }

// Notify implements Notifier.
func (s Slack) Notify(ctx context.Context, d *domain.Deployment, summary string) error {
	icon := ":white_check_mark:"
	title := fmt.Sprintf("Promoted %s %s to 100%%", d.Service, d.Ref)
	if d.Status == domain.StatusRolledBack {
		icon = ":rewind:"
		title = fmt.Sprintf("Rolled back %s %s at %d%% traffic", d.Service, d.Ref, d.Weight)
	}
	body, _ := json.Marshal(map[string]string{
		"text": fmt.Sprintf("%s *%s*\n%s\nRisk %d (%s) · deployment `%s`", icon, title, summary, d.Risk.Score, d.Risk.Level, d.ID),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("slack webhook: HTTP %d", res.StatusCode)
	}
	return nil
}
