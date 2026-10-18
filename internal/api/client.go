// Package api is a small client for the Zenavo REST API (https://docs.zenavo.app/api/overview).
package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client calls one control plane with one access token.
type Client struct {
	Base      string // origin, e.g. https://zenavo.app
	Token     string // zv_pat_…
	UserAgent string
	HTTP      *http.Client
}

// New returns a client that never follows redirects, so the token only ever reaches Base.
func New(base, token, userAgent string) *Client {
	return &Client{Base: base, Token: token, UserAgent: userAgent, HTTP: &http.Client{
		Timeout: 90 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("the API answered with a redirect; check --api")
		},
	}}
}

// Error is an API error response: {"detail": …}.
type Error struct {
	Status int
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("API error %d", e.Status)
	}
	return e.Detail
}

// IsStatus reports whether err is an API error with this status.
func IsStatus(err error, status int) bool {
	var ae *Error
	return errors.As(err, &ae) && ae.Status == status
}

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+"/api"+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	// Unauthenticated calls (CLI sign-in) pass the API's CSRF check with this header; browsers cannot send it
	// cross-origin without a preflight.
	req.Header.Set("X-Zenavo-Request", "1")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

// Do sends a JSON request and decodes a JSON response into out (if not nil).
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	req, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return decodeError(resp.StatusCode, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("unexpected API response (%d): %w", resp.StatusCode, err)
	}
	return nil
}

func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	if body == nil {
		body = struct{}{}
	}
	return c.Do(ctx, http.MethodPost, path, body, out)
}

// decodeError turns {"detail": "…"} or {"detail": [{"loc": […], "msg": "…"}]} into an Error.
func decodeError(status int, raw []byte) error {
	var body struct {
		Detail json.RawMessage `json:"detail"`
	}
	e := &Error{Status: status}
	if json.Unmarshal(raw, &body) != nil || len(body.Detail) == 0 {
		e.Detail = http.StatusText(status)
		return e
	}
	var text string
	if json.Unmarshal(body.Detail, &text) == nil {
		e.Detail = text
		return e
	}
	var list []json.RawMessage
	if json.Unmarshal(body.Detail, &list) == nil {
		var msgs []string
		for _, item := range list {
			var s string
			var issue struct {
				Loc []any  `json:"loc"`
				Msg string `json:"msg"`
			}
			switch {
			case json.Unmarshal(item, &s) == nil:
				msgs = append(msgs, s)
			case json.Unmarshal(item, &issue) == nil && issue.Msg != "":
				var loc []string
				for _, l := range issue.Loc {
					if l != "body" {
						loc = append(loc, fmt.Sprint(l))
					}
				}
				if len(loc) > 0 {
					msgs = append(msgs, strings.Join(loc, ".")+": "+issue.Msg)
				} else {
					msgs = append(msgs, issue.Msg)
				}
			}
		}
		e.Detail = strings.Join(msgs, "; ")
		return e
	}
	e.Detail = string(body.Detail)
	return e
}

// Stream reads a Server-Sent Events endpoint and calls onData with each data payload until the
// server sends "event: end", the stream closes or ctx is cancelled.
func (c *Client) Stream(ctx context.Context, path string, onData func(data string) error) error {
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	hc := *c.HTTP
	hc.Timeout = 0 // the stream is long-lived; ctx ends it
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return decodeError(resp.StatusCode, raw)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 2<<20)
	event, data := "", []string{}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if event == "end" {
				return nil
			}
			if len(data) > 0 {
				if err := onData(strings.Join(data, "\n")); err != nil {
					return err
				}
			}
			event, data = "", data[:0]
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return sc.Err()
}

// Q builds a query string from key/value pairs, skipping empty values.
func Q(kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			v.Set(kv[i], kv[i+1])
		}
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// P escapes one path segment.
func P(s string) string { return url.PathEscape(s) }
