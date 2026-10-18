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

// deviceLogin runs the OAuth 2.0 device authorization flow (RFC 8628) and returns the new API key.
func (a *App) deviceLogin() (string, error) {
	host, _ := os.Hostname()
	var start struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		ExpiresIn  int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	if err := a.client.Post(a.ctx, "/auth/device/code", map[string]string{"client_name": host}, &start); err != nil {
		if api.IsStatus(err, 404) {
			return "", errors.New("this control plane does not support browser sign-in yet; use: zenavo auth login --with-token")
		}
		return "", err
	}
	// The address is built from --api, not taken from the response, so the CLI only ever opens its own console.
	url := a.client.Base + "/device"
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
			return "", errors.New("cancelled")
		case <-time.After(interval):
		}
		var out struct {
			Status string `json:"status"`
			Token  string `json:"token"`
		}
		err := a.client.Post(ctx, "/auth/device/token", map[string]string{"device_code": start.DeviceCode}, &out)
		var ae *api.Error
		switch {
		case err == nil && out.Token != "":
			return out.Token, nil
		case err == nil:
			// still pending
		case errors.As(err, &ae) && ae.Status == 429:
			interval += 5 * deviceUnit // RFC 8628 slow_down
		case errors.Is(err, context.Canceled):
			return "", errors.New("cancelled")
		default:
			return "", fmt.Errorf("sign-in failed: %w", err)
		}
		if time.Now().After(deadline) {
			return "", errors.New("the code expired before it was approved; run zenavo auth login again")
		}
	}
}
