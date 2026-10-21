package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"time"

	"github.com/Zenavo-App/zenavo-cli/internal/api"
)

// deviceUnit is the unit of the server's poll interval (seconds); tests shorten it.
var deviceUnit = time.Second

// openBrowser opens a URL; tests replace it.
var openBrowser = func(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// deviceGrant is a key from browser sign-in and what the approver granted: they may narrow the scope or limit
// the key to some projects.
type deviceGrant struct {
	Token      string   `json:"token"`
	Scope      string   `json:"scope"`
	ProjectIDs []string `json:"project_ids"`
}

// deviceLogin runs the OAuth 2.0 device authorization flow (RFC 8628) and returns the new API key.
func (a *App) deviceLogin(scope string) (deviceGrant, error) {
	host, _ := os.Hostname()
	// Sign-in is unauthenticated: a key saved earlier is not sent (the API refuses keys on these routes).
	c := api.New(a.client.Base, "", a.client.UserAgent)
	var start struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		ExpiresIn  int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	if err := c.Post(a.ctx, "/auth/device/code", map[string]string{"client_name": host, "scope": scope, "client": "cli"}, &start); err != nil {
		if api.IsStatus(err, 404) {
			return deviceGrant{}, errors.New("this control plane does not support browser sign-in yet; use: zenavo auth login --with-token")
		}
		return deviceGrant{}, err
	}
	// The address is built from --api, not taken from the response, so the CLI only ever opens its own console.
	url := a.client.Base + "/device"
	if scope != "full" {
		a.info("Requesting a %s key.", scope)
	}
	a.info("Open %s and enter the code:\n\n    %s\n", url, start.UserCode)
	if a.TTY && a.interactive {
		if err := openBrowser(url); err == nil {
			a.info("(Opened your browser. Check that the code there matches before you approve.)")
		}
	}
	a.info("Waiting for approval… (Ctrl-C to cancel)")

	ctx, cancel := signal.NotifyContext(a.ctx, os.Interrupt)
	defer cancel()
	if start.Interval < 1 {
		start.Interval = 5
	}
	interval := time.Duration(start.Interval) * deviceUnit
	deadline := time.Now().Add(time.Duration(start.ExpiresIn) * time.Second)
	for {
		select {
		case <-ctx.Done():
			return deviceGrant{}, errors.New("cancelled")
		case <-time.After(interval):
		}
		var out deviceGrant
		err := c.Post(ctx, "/auth/device/token", map[string]string{"device_code": start.DeviceCode}, &out)
		var ae *api.Error
		switch {
		case err == nil && out.Token != "":
			if out.Scope == "" {
				// A control plane from before scopes ignores the request: its keys can do everything.
				out.Scope = "full"
				if scope != "full" {
					a.info("Warning: this control plane does not support key scopes: the new key has full access, not %s. "+
						"Delete it at %s/account/api-keys if that is not what you want.", scope, a.client.Base)
				}
			}
			return out, nil
		case err == nil:
			// still pending
		case errors.As(err, &ae) && ae.Status == 429:
			interval += 5 * deviceUnit // RFC 8628 slow_down
		case errors.Is(err, context.Canceled):
			return deviceGrant{}, errors.New("cancelled")
		default:
			return deviceGrant{}, fmt.Errorf("sign-in failed: %w", err)
		}
		if time.Now().After(deadline) {
			return deviceGrant{}, errors.New("the code expired before it was approved; run zenavo auth login again")
		}
	}
}
