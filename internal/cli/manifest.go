package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zenavo-App/zenavo-cli/internal/api"
)

// editableManifest is a project's configuration decoded generically, so that saving it back keeps every
// field, including ones this CLI does not know.
type editableManifest struct {
	project  *api.Project
	doc      api.ManifestDoc
	manifest map[string]any
}

func (a *App) loadManifest(p *api.Project) (*editableManifest, error) {
	m := &editableManifest{project: p}
	if err := a.get("/projects/"+api.P(p.ID)+"/manifest", &m.doc); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(m.doc.Manifest))
	dec.UseNumber()
	if err := dec.Decode(&m.manifest); err != nil {
		return nil, fmt.Errorf("unexpected manifest: %w", err)
	}
	return m, nil
}

// service returns the named service's object.
func (m *editableManifest) service(name string) (map[string]any, error) {
	list, _ := m.manifest["services"].([]any)
	for _, raw := range list {
		if svc, ok := raw.(map[string]any); ok && svc["name"] == name {
			return svc, nil
		}
	}
	return nil, fmt.Errorf("no service %q", name)
}

// env returns the service's variables, creating the map if needed.
func (m *editableManifest) env(name string) (map[string]any, error) {
	svc, err := m.service(name)
	if err != nil {
		return nil, err
	}
	env, ok := svc["env"].(map[string]any)
	if !ok {
		env = map[string]any{}
		svc["env"] = env
	}
	return env, nil
}

// port finds a port of the service by ID; with id "" it returns the only HTTP port.
func (m *editableManifest) port(service, id string) (map[string]any, error) {
	svc, err := m.service(service)
	if err != nil {
		return nil, err
	}
	ports, _ := svc["ports"].([]any)
	var http []map[string]any
	for _, raw := range ports {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id != "" && p["id"] == id {
			return p, nil
		}
		if p["type"] == "HTTP" {
			http = append(http, p)
		}
	}
	switch {
	case id != "":
		return nil, fmt.Errorf("service %s has no port %q", service, id)
	case len(http) == 1:
		return http[0], nil
	case len(http) == 0:
		return nil, fmt.Errorf("service %s has no HTTP port; add one in the console", service)
	}
	return nil, errors.New("the service has several HTTP ports: pass --port")
}

// save writes the configuration back. The revision check makes a concurrent edit fail instead of being lost.
func (a *App) saveManifest(m *editableManifest) error {
	body := map[string]any{"manifest": m.manifest, "server_id": m.doc.ServerID, "revision": m.doc.Revision}
	return a.do("PUT", "/projects/"+api.P(m.project.ID), body, nil)
}
