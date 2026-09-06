package backend

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL, Token string
	HTTP           *http.Client
}

func New(base, token string) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("enter a valid HTTP or HTTPS backend URL")
	}
	return &Client{u.String(), NormalizeToken(token), &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func NormalizeToken(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == s[len(s)-1] && (s[0] == '\'' || s[0] == '"') {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	if strings.HasPrefix(strings.ToLower(s), "bearer ") {
		s = strings.TrimSpace(s[7:])
	}
	return s
}
func (c *Client) Request(ctx context.Context, method, path string, query url.Values, payload any, result any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	endpoint := c.BaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "mares-smart-air-sync/1.0")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("backend %s %s: %s %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if result == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	err = json.NewDecoder(resp.Body).Decode(result)
	if err == io.EOF {
		return nil
	}
	return err
}
func (c *Client) Fingerprint(ctx context.Context, vendor, product string) ([]byte, error) {
	var r struct {
		Fingerprint string `json:"fingerprint_hex"`
	}
	err := c.Request(ctx, "GET", "/api/device-state", url.Values{"vendor": {vendor}, "product": {product}}, nil, &r)
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(r.Fingerprint)
}
func (c *Client) SaveFingerprint(ctx context.Context, vendor, product string, fp []byte) error {
	return c.Request(ctx, "PUT", "/api/device-state", nil, map[string]any{"vendor": vendor, "product": product, "fingerprint_hex": hex.EncodeToString(fp)}, nil)
}
func (c *Client) Insert(ctx context.Context, record map[string]any) (bool, error) {
	var r struct {
		Inserted bool `json:"inserted"`
	}
	err := c.Request(ctx, "POST", "/api/dives", nil, record, &r)
	return r.Inserted, err
}
func (c *Client) Count(ctx context.Context) *int {
	var r struct {
		Total *int `json:"total"`
	}
	if c.Request(ctx, "GET", "/api/dives", url.Values{"limit": {"1"}, "offset": {"0"}, "include_samples": {"false"}, "include_raw_data": {"false"}}, nil, &r) != nil || r.Total == nil || *r.Total < 0 {
		return nil
	}
	return r.Total
}

type Approval struct {
	Code   string `json:"code"`
	Status string `json:"status"`
	Token  string `json:"token"`
}

func (c *Client) StartLogin(ctx context.Context) (Approval, error) {
	var a Approval
	err := c.Request(ctx, "POST", "/api/cli-auth/request", nil, nil, &a)
	if err == nil && a.Code == "" {
		err = fmt.Errorf("backend returned an empty approval code")
	}
	return a, err
}
func (c *Client) ApprovalURL(code string) string {
	return c.BaseURL + "/#settings/cli-auth/" + url.PathEscape(code)
}
func (c *Client) WaitLogin(ctx context.Context, code string) (string, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			var a Approval
			if err := c.Request(ctx, "GET", "/api/cli-auth/request", url.Values{"code": {code}}, nil, &a); err != nil {
				return "", err
			}
			switch a.Status {
			case "approved":
				if token := NormalizeToken(a.Token); token != "" {
					return token, nil
				}
				return "", fmt.Errorf("approval contains no token")
			case "denied", "expired", "rejected":
				return "", fmt.Errorf("login %s", a.Status)
			}
		}
	}
}
