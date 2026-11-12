// Package mail sends transactional email. Brevo is the production sender;
// LogMail is the development fallback that prints codes to the server log.
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// Sender delivers a verification code to an email address.
type Sender interface {
	Name() string
	SendVerificationCode(ctx context.Context, to, code string) error
}

// FromEnv returns a Brevo sender when BREVO_API_KEY and BREVO_SENDER_EMAIL
// are set, else a LogMail fallback so signups keep working in development.
func FromEnv() Sender {
	key := os.Getenv("BREVO_API_KEY")
	sender := os.Getenv("BREVO_SENDER_EMAIL")
	if key != "" && sender != "" {
		return &Brevo{
			APIKey:      key,
			SenderEmail: sender,
			SenderName:  os.Getenv("BREVO_SENDER_NAME"),
			Client:      &http.Client{Timeout: 15 * time.Second},
		}
	}
	return LogMail{}
}

const brevoEndpoint = "https://api.brevo.com/v3/smtp/email"

// Brevo sends mail through the Brevo transactional API.
type Brevo struct {
	APIKey      string
	SenderEmail string
	SenderName  string
	Client      *http.Client
}

func (b *Brevo) Name() string { return "brevo" }

type brevoAddress struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type brevoEmail struct {
	Sender      brevoAddress   `json:"sender"`
	To          []brevoAddress `json:"to"`
	Subject     string         `json:"subject"`
	TextContent string         `json:"textContent"`
	HTMLContent string         `json:"htmlContent"`
}

func (b *Brevo) SendVerificationCode(ctx context.Context, to, code string) error {
	subject := "Dein Simple Vocabulary Bestätigungscode"
	text := fmt.Sprintf(
		"Hallo!\n\nDein Bestätigungscode für Simple Vocabulary lautet: %s\n\n"+
			"Der Code ist 30 Minuten gültig. Wenn du kein Konto erstellt hast, "+
			"kannst du diese E-Mail ignorieren.\n", code)
	html := fmt.Sprintf(
		`<p>Hallo!</p><p>Dein Bestätigungscode für Simple Vocabulary lautet:</p>`+
			`<p style="font-size:24px;font-weight:700;letter-spacing:4px">%s</p>`+
			`<p>Der Code ist 30 Minuten gültig. Wenn du kein Konto erstellt hast, `+
			`kannst du diese E-Mail ignorieren.</p>`, code)

	payload := brevoEmail{
		Sender:      brevoAddress{Email: b.SenderEmail, Name: b.SenderName},
		To:          []brevoAddress{{Email: to}},
		Subject:     subject,
		TextContent: text,
		HTMLContent: html,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, brevoEndpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", b.APIKey)

	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("brevo: status %d: %s", res.StatusCode, string(snippet))
	}
	return nil
}

// LogMail prints verification codes to the server log instead of sending
// email. It keeps signup usable when no provider is configured.
type LogMail struct{}

func (LogMail) Name() string { return "log" }

func (LogMail) SendVerificationCode(_ context.Context, to, code string) error {
	log.Printf("mail not configured — verification code for %s: %s", to, code)
	return nil
}
