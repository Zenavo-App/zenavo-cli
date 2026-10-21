package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Zenavo-App/zenavo-cli/internal/api"
)

// Token scopes the CLI can ask for, from least to most; the API enforces them.
var scopes = []string{"read", "deploy", "full"}

// accessText describes a key's scope and project limit for people.
func accessText(scope string, projects []string) string {
	what := map[string]string{
		"read":   "read (look only, no changes)",
		"deploy": "deploy (deploy and configure existing projects)",
		"full":   "full",
	}[scope]
	if what == "" {
		what = scope
	}
	if len(projects) == 0 {
		return what + ", all projects"
	}
	return fmt.Sprintf("%s, limited to %d project(s): %s", what, len(projects), strings.Join(projects, ", "))
}

// scopeHint explains a 403 that the API gave because of the key's scope or project limit, or because no access
// token may do it. Other errors get no hint.
func (a *App) scopeHint(err error) string {
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Status != 403 {
		return ""
	}
	console, keys := "the Zenavo console", "the console's API keys page"
	if a.client != nil {
		console, keys = a.client.Base, a.client.Base+"/account/api-keys"
	}
	detail := strings.ToLower(ae.Detail)
	switch {
	case strings.Contains(detail, "use the zenavo console"):
		return "API keys cannot do this; do it in the console: " + console
	case strings.Contains(detail, "scope"):
		need := "full"
		if _, after, ok := strings.Cut(detail, "this needs "); ok {
			if f := strings.Fields(after); len(f) > 0 && contains(scopes, strings.Trim(f[0], ".")) {
				need = strings.Trim(f[0], ".")
			}
		}
		return fmt.Sprintf("This key's scope does not allow it. Sign in again with: zenavo auth login --scope %s\n"+
			"or create a key with that scope at %s, or do it in the console.", need, keys)
	case strings.Contains(detail, "limited to some projects"):
		return "This key is limited to some projects. Sign in again with zenavo auth login and approve it for all projects,\n" +
			"or do it in the console: " + console
	}
	return ""
}
