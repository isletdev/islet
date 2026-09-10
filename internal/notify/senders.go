package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

// Types lists the supported channel types and the config keys each needs.
var Types = map[string][]string{
	"telegram": {"token", "chatId"},
	"discord":  {"webhookUrl"},
	"slack":    {"webhookUrl"},
	"email":    {"host", "port", "username", "password", "from", "to"},
	"ntfy":     {"url", "topic", "token"},
	"gotify":   {"url", "token"},
	"pushover": {"appToken", "userKey"},
	"webhook":  {"url", "secret"},
}

// ValidateConfig checks the keys a channel type needs.
func ValidateConfig(typ string, cfg map[string]string) error {
	keys, ok := Types[typ]
	if !ok {
		return fmt.Errorf("unknown channel type %q", typ)
	}
	optional := map[string]bool{"token": typ == "ntfy", "username": typ == "email", "password": typ == "email", "secret": true, "port": true}
	for _, k := range keys {
		if strings.TrimSpace(cfg[k]) == "" && !optional[k] {
			return fmt.Errorf("%s is required", k)
		}
	}
	for _, k := range []string{"webhookUrl", "url"} {
		if v := cfg[k]; v != "" {
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
				return fmt.Errorf("%s must be an http(s) URL", k)
			}
		}
	}
	return nil
}

func emoji(sev string) string {
	switch sev {
	case Critical:
		return "🔴"
	case Warning:
		return "🟠"
	default:
		return "🟢"
	}
}

func plain(m Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s\n", emoji(m.Severity), m.Server, m.Title)
	if m.Body != "" {
		b.WriteString(m.Body + "\n")
	}
	if m.Link != "" {
		b.WriteString(m.Link + "\n")
	}
	return strings.TrimSpace(b.String())
}

// Send delivers one message to one channel.
func Send(ctx context.Context, c Channel, m Message) error {
	cfg := c.Config
	switch c.Type {
	case "telegram":
		text := fmt.Sprintf("%s <b>%s</b>: %s", emoji(m.Severity), html(m.Server), html(m.Title))
		if m.Body != "" {
			text += "\n" + html(m.Body)
		}
		if m.Link != "" {
			text += fmt.Sprintf("\n<a href=\"%s\">Open in Islet</a>", m.Link)
		}
		return postJSON(ctx, "https://api.telegram.org/bot"+cfg["token"]+"/sendMessage", map[string]any{"chat_id": cfg["chatId"], "text": text, "parse_mode": "HTML", "disable_web_page_preview": true}, nil)
	case "discord":
		color := map[string]int{Info: 0x4ADE80, Warning: 0xFBBF24, Critical: 0xF87171}[m.Severity]
		embed := map[string]any{"title": m.Title, "description": m.Body, "color": color, "footer": map[string]string{"text": m.Server + " · " + m.Category}, "timestamp": m.Time.UTC().Format(time.RFC3339)}
		if m.Link != "" {
			embed["url"] = m.Link
		}
		return postJSON(ctx, cfg["webhookUrl"], map[string]any{"embeds": []any{embed}}, nil)
	case "slack":
		blocks := []any{map[string]any{"type": "section", "text": map[string]string{"type": "mrkdwn", "text": fmt.Sprintf("%s *%s*: %s\n%s", emoji(m.Severity), m.Server, m.Title, m.Body)}}}
		if m.Link != "" {
			blocks = append(blocks, map[string]any{"type": "context", "elements": []any{map[string]string{"type": "mrkdwn", "text": "<" + m.Link + "|Open in Islet>"}}})
		}
		return postJSON(ctx, cfg["webhookUrl"], map[string]any{"text": plain(m), "blocks": blocks}, nil)
	case "ntfy":
		base := strings.TrimRight(cfg["url"], "/")
		if base == "" {
			base = "https://ntfy.sh"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/"+cfg["topic"], strings.NewReader(m.Body))
		if err != nil {
			return err
		}
		req.Header.Set("Title", m.Server+": "+m.Title)
		req.Header.Set("Priority", map[string]string{Info: "3", Warning: "4", Critical: "5"}[m.Severity])
		req.Header.Set("Tags", map[string]string{Info: "white_check_mark", Warning: "warning", Critical: "rotating_light"}[m.Severity])
		if m.Link != "" {
			req.Header.Set("Click", m.Link)
		}
		if cfg["token"] != "" {
			req.Header.Set("Authorization", "Bearer "+cfg["token"])
		}
		return do(req)
	case "gotify":
		return postJSON(ctx, strings.TrimRight(cfg["url"], "/")+"/message?token="+url.QueryEscape(cfg["token"]),
			map[string]any{"title": m.Server + ": " + m.Title, "message": m.Body + "\n" + m.Link, "priority": map[string]int{Info: 4, Warning: 6, Critical: 8}[m.Severity]}, nil)
	case "pushover":
		form := url.Values{"token": {cfg["appToken"]}, "user": {cfg["userKey"]}, "title": {m.Server + ": " + m.Title}, "message": {m.Body}, "priority": {map[string]string{Info: "-1", Warning: "0", Critical: "1"}[m.Severity]}}
		if m.Link != "" {
			form.Set("url", m.Link)
			form.Set("url_title", "Open in Islet")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.pushover.net/1/messages.json", strings.NewReader(form.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return do(req)
	case "webhook":
		body, _ := json.Marshal(map[string]any{"server": m.Server, "category": m.Category, "severity": m.Severity, "title": m.Title, "message": m.Body, "link": m.Link, "time": m.Time.UTC().Format(time.RFC3339)})
		headers := map[string]string{}
		if cfg["secret"] != "" {
			mac := hmac.New(sha256.New, []byte(cfg["secret"]))
			mac.Write(body)
			headers["X-Islet-Signature"] = "sha256=" + hex.EncodeToString(mac.Sum(nil))
		}
		return postRaw(ctx, cfg["url"], body, headers)
	case "email":
		return sendMail(cfg, m)
	default:
		return fmt.Errorf("unknown channel type %q", c.Type)
	}
}

func html(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func postJSON(ctx context.Context, u string, v any, headers map[string]string) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return postRaw(ctx, u, b, headers)
}

func postRaw(ctx context.Context, u string, body []byte, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "islet-notify")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return do(req)
}

func do(req *http.Request) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func sendMail(cfg map[string]string, m Message) error {
	host, port := cfg["host"], cfg["port"]
	if port == "" {
		port = "587"
	}
	addr := net.JoinHostPort(host, port)
	subject := fmt.Sprintf("[%s] %s: %s", strings.ToUpper(m.Severity), m.Server, m.Title)
	var msg strings.Builder
	fmt.Fprintf(&msg, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", cfg["from"], cfg["to"], subject, plain(m))
	var auth smtp.Auth
	if cfg["username"] != "" {
		auth = smtp.PlainAuth("", cfg["username"], cfg["password"], host)
	}
	to := strings.Split(strings.ReplaceAll(cfg["to"], " ", ""), ",")
	if port == "465" {
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return err
		}
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		defer c.Close()
		if auth != nil {
			if err := c.Auth(auth); err != nil {
				return err
			}
		}
		if err := c.Mail(cfg["from"]); err != nil {
			return err
		}
		for _, t := range to {
			if err := c.Rcpt(t); err != nil {
				return err
			}
		}
		w, err := c.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(msg.String())); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		return c.Quit()
	}
	// STARTTLS path (587) or plain (25): smtp.SendMail upgrades when the server offers STARTTLS.
	if err := smtp.SendMail(addr, auth, cfg["from"], to, []byte(msg.String())); err != nil {
		return err
	}
	return nil
}

// TelegramDetect returns the chats that recently messaged the bot, so the
// setup wizard can fill in the chat id after the user sends /start.
func TelegramDetect(ctx context.Context, token string) ([]map[string]string, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("token is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.telegram.org/bot"+token+"/getUpdates?limit=20", nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r struct {
		OK     bool `json:"ok"`
		Result []struct {
			Message struct {
				Chat struct {
					ID    int64  `json:"id"`
					Type  string `json:"type"`
					Title string `json:"title"`
					First string `json:"first_name"`
					User  string `json:"username"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if !r.OK {
		return nil, errors.New("telegram: " + r.Description)
	}
	seen := map[int64]bool{}
	out := []map[string]string{}
	for _, u := range r.Result {
		c := u.Message.Chat
		if c.ID == 0 || seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		name := c.Title
		if name == "" {
			name = strings.TrimSpace(c.First + " @" + c.User)
		}
		out = append(out, map[string]string{"chatId": fmt.Sprint(c.ID), "name": name, "type": c.Type})
	}
	return out, nil
}
