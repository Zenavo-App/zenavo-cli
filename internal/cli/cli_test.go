package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zenavo-App/zenavo-cli/internal/config"
)

const token = "zv_pat_test"

// fakeAPI is a minimal control plane: one personal team and one shared team, a server, one project.
type fakeAPI struct {
	*httptest.Server
	mu       sync.Mutex
	manifest map[string]any
	revision int
	jobPolls int
	jobFinal string
	// device sign-in: the token poll answers with these statuses in order, then the token.
	devicePolls []int
	deviceStart map[string]any    // the last device code request
	deviceGrant string            // extra JSON fields of the token response, e.g. `"scope":"read"`
	forbid      map[string]string // method and path → 403 detail
	calls       []string
	bodies      map[string]map[string]any
	usedIDs     []string // used_service_ids of the manifest
	conflict    bool     // the next save fails as if someone else saved first
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{jobFinal: "succeeded", revision: 3, bodies: map[string]map[string]any{},
		manifest: map[string]any{"version": 1, "name": "shop", "x_unknown": "keep me", "services": []any{
			map[string]any{"name": "web", "source": map[string]any{"git": "https://github.com/a/shop"}, "env": map[string]any{"SECRET": "hunter2", "DB": "${POSTGRES_HOST}"},
				"ports": []any{map[string]any{"id": "web", "port": 8080, "type": "HTTP", "domain": nil}}},
			map[string]any{"name": "db", "source": map[string]any{"image": "postgres:16"}},
		}}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve(t)))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPI) serve(t *testing.T) func(http.ResponseWriter, *http.Request) {
	project := func() map[string]any {
		return map[string]any{"id": "proj_shop", "team_id": "team_me", "name": "shop", "server_id": "srv_1", "revision": f.revision,
			"active_job": "job_old", "running": true, "created": 1700000000, "ready": true, "blockers": []string{},
			"services": []any{map[string]any{"name": "web", "source": "Git", "ports": []any{map[string]any{"id": "web", "port": 8080, "type": "HTTP"}}},
				map[string]any{"name": "db", "source": "postgres:16", "icon": "postgresql", "ports": []any{}}}}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		key := r.Method + " " + r.URL.Path
		f.calls = append(f.calls, key+"?"+r.URL.RawQuery)
		if strings.HasPrefix(r.URL.Path, "/api/auth/device/") {
			if r.Header.Get("Authorization") != "" || r.Header.Get("X-Zenavo-Request") != "1" {
				t.Errorf("device request headers: %v", r.Header)
			}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/api/auth/device/code" {
				f.deviceStart = nil
				json.NewDecoder(r.Body).Decode(&f.deviceStart)
				fmt.Fprint(w, `{"device_code":"dc_secret","user_code":"BCDF-GHJK","verification_uri":"https://evil.example/device","expires_in":600,"interval":5}`)
				return
			}
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["device_code"] != "dc_secret" {
				t.Errorf("device_code %q", body["device_code"])
			}
			if len(f.devicePolls) > 0 {
				status := f.devicePolls[0]
				f.devicePolls = f.devicePolls[1:]
				w.WriteHeader(status)
				switch status {
				case 202:
					fmt.Fprint(w, `{"status":"pending","interval":5}`)
				case 429:
					fmt.Fprint(w, `{"detail":"slow_down"}`)
				default:
					fmt.Fprint(w, `{"detail":"Sign-in was denied in the console"}`)
				}
				return
			}
			grant := ""
			if f.deviceGrant != "" {
				grant = "," + f.deviceGrant
			}
			fmt.Fprintf(w, `{"token":%q,"name":"CLI on test","expires":1%s}`, token, grant)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"detail":"Invalid or expired access token"}`)
			return
		}
		if r.Method != "GET" {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f.bodies[key] = body
		}
		reply := func(status int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(v)
		}
		if detail, ok := f.forbid[key]; ok {
			reply(403, map[string]string{"detail": detail})
			return
		}
		if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/projects/proj_shop/services/svc-") && strings.HasSuffix(r.URL.Path, "/deploy") {
			reply(202, map[string]string{"id": "job_1"})
			return
		}
		switch key {
		case "GET /api/images/ports":
			reply(200, map[string]any{"ports": []any{map[string]any{"port": 5432, "protocol": "tcp"}, map[string]any{"port": 8080, "protocol": "tcp"}, map[string]any{"port": 9000, "protocol": "udp"}}})
		case "POST /api/git/refs":
			reply(200, map[string]any{"url": f.bodies[key]["url"], "name": "api", "default_branch": "trunk"})
		case "POST /api/git/plan":
			reply(200, map[string]any{"dockerfile": "Dockerfile", "items": []any{map[string]any{"key": "port", "value": "3000"}}})
		case "GET /api/auth/me":
			reply(200, map[string]any{"user": map[string]any{"id": "user_1", "email": "dev@example.com"},
				"teams": []any{map[string]any{"id": "team_me", "name": "Dev", "personal": true, "role": "owner"},
					map[string]any{"id": "team_acme", "name": "Acme", "personal": false, "role": "member"}}})
		case "GET /api/projects":
			if r.URL.Query().Get("team_id") == "team_acme" {
				reply(200, []any{})
				return
			}
			reply(200, []any{project()})
		case "GET /api/projects/proj_shop":
			reply(200, project())
		case "GET /api/servers":
			reply(200, []any{map[string]any{"id": "srv_1", "name": "tokyo", "host": "203.0.113.5", "status": "ready"}})
		case "POST /api/projects":
			reply(201, map[string]any{"id": "proj_new", "team_id": "team_me", "name": "blog", "services": []any{}})
		case "POST /api/projects/proj_shop/deploy", "POST /api/projects/proj_shop/services/web/deploy", "POST /api/projects/proj_shop/services/web/restart":
			if r.URL.Path == "/api/projects/proj_shop/deploy" && f.bodies[key]["acknowledge_init"] != true {
				reply(409, map[string]any{"detail": "Initialization commands can change data; acknowledge them before deployment"})
				return
			}
			reply(202, map[string]string{"id": "job_1"})
		case "GET /api/jobs/job_1":
			f.jobPolls++
			status, log := "running", "step 1\n"
			if f.jobPolls > 1 {
				status, log = f.jobFinal, "step 1\nstep 2\n"
			}
			reply(200, map[string]any{"id": "job_1", "kind": "deploy", "status": status, "log": log, "progress": map[string]string{"web": "building"}, "created": 1})
		case "GET /api/jobs":
			reply(200, []any{map[string]any{"id": "job_1", "kind": "deploy", "status": "succeeded", "created": 1}})
		case "GET /api/projects/proj_shop/manifest":
			reply(200, map[string]any{"manifest": f.manifest, "revision": f.revision, "server_id": "srv_1", "used_service_ids": f.usedIDs})
		case "PUT /api/projects/proj_shop":
			body := f.bodies[key]
			if f.conflict {
				f.conflict = false
				f.revision++
			}
			if int(body["revision"].(float64)) != f.revision {
				reply(409, map[string]any{"detail": "Configuration changed; reload before saving"})
				return
			}
			f.manifest = body["manifest"].(map[string]any)
			f.revision++
			reply(200, project())
		case "GET /api/projects/proj_shop/logs/web/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "retry: 3000\n\ndata: \"hello\"\n\ndata: \"world\"\n\nevent: end\ndata: {}\n\n")
		case "POST /api/projects/proj_shop/services/web/exec":
			reply(200, map[string]any{"output": "boom", "exit_code": 3})
		default:
			reply(404, map[string]string{"detail": "Not found"})
		}
	}
}

type run struct {
	code           int
	stdout, stderr string
}

// zenavo runs the CLI against the fake API with a private config file.
func zenavo(t *testing.T, cfgPath, stdin string, env map[string]string, args ...string) run {
	t.Helper()
	var out, errb bytes.Buffer
	a := &App{In: strings.NewReader(stdin), Out: &out, Err: &errb, Env: func(k string) string {
		if k == "ZENAVO_CONFIG" {
			return cfgPath
		}
		return env[k]
	}}
	code := a.Run(context.Background(), args)
	return run{code, out.String(), errb.String()}
}

func setup(t *testing.T) (*fakeAPI, string) {
	pollInterval = time.Millisecond
	f := newFakeAPI(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	if r := zenavo(t, cfg, token+"\n", nil, "auth", "login", "--with-token", "--api", f.URL); r.code != 0 {
		t.Fatalf("login: %+v", r)
	}
	return f, cfg
}

func TestLoginSavesTokenPrivately(t *testing.T) {
	f, cfgPath := setup(t)
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %o", info.Mode().Perm())
	}
	c, _ := config.Load(cfgPath)
	if c.Token != token || c.API != f.URL {
		t.Fatalf("saved %+v", c)
	}
	r := zenavo(t, cfgPath, "", nil, "auth", "status")
	if r.code != 0 || !strings.Contains(r.stdout, "dev@example.com") {
		t.Fatalf("status: %+v", r)
	}
	// A bad key is refused and not saved.
	r = zenavo(t, filepath.Join(t.TempDir(), "c.json"), "zv_pat_wrong\n", nil, "auth", "login", "--with-token", "--api", f.URL)
	if r.code == 0 || !strings.Contains(r.stderr, "rejected") {
		t.Fatalf("bad key: %+v", r)
	}
	r = zenavo(t, filepath.Join(t.TempDir(), "c.json"), "not-a-key\n", nil, "auth", "login", "--with-token", "--api", f.URL)
	if r.code == 0 || !strings.Contains(r.stderr, "zv_pat_") {
		t.Fatalf("non-key: %+v", r)
	}
}

func TestTokenStaysWithItsAPI(t *testing.T) {
	f, cfg := setup(t)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("saved token sent to another API")
		}
		w.WriteHeader(401)
	}))
	defer other.Close()
	r := zenavo(t, cfg, "", nil, "--api", other.URL, "project", "ls")
	if r.code == 0 || !strings.Contains(r.stderr, "not signed in to "+other.URL) {
		t.Fatalf("got %+v", r)
	}
	// ZENAVO_TOKEN works without a saved key.
	r = zenavo(t, filepath.Join(t.TempDir(), "c.json"), "", map[string]string{"ZENAVO_TOKEN": token, "ZENAVO_API_URL": f.URL}, "project", "ls")
	if r.code != 0 || !strings.Contains(r.stdout, "proj_shop") {
		t.Fatalf("env token: %+v", r)
	}
}

func TestContextAndTeams(t *testing.T) {
	_, cfg := setup(t)
	r := zenavo(t, cfg, "", nil, "context", "set", "project", "shop")
	if r.code != 0 {
		t.Fatalf("set project: %+v", r)
	}
	r = zenavo(t, cfg, "", nil, "context", "set", "service", "web")
	if r.code != 0 {
		t.Fatalf("set service: %+v", r)
	}
	c, _ := config.Load(cfg)
	if c.Project != "proj_shop" || c.Service != "web" {
		t.Fatalf("context %+v", c)
	}
	r = zenavo(t, cfg, "", nil, "workspace", "switch", "acme")
	c, _ = config.Load(cfg)
	if r.code != 0 || c.Team != "team_acme" || c.Project != "" || c.Service != "" {
		t.Fatalf("switch: %+v %+v", r, c)
	}
	r = zenavo(t, cfg, "", nil, "project", "ls", "--json")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "[]" {
		t.Fatalf("acme projects: %+v", r)
	}
	// --team for one command; the saved team stays.
	r = zenavo(t, cfg, "", nil, "--team", "Dev", "project", "ls")
	if r.code != 0 || !strings.Contains(r.stdout, "shop") {
		t.Fatalf("--team: %+v", r)
	}
	r = zenavo(t, cfg, "", nil, "team", "switch", "nope")
	if r.code == 0 || !strings.Contains(r.stderr, `no team "nope"`) {
		t.Fatalf("unknown team: %+v", r)
	}
}

func TestNonInteractiveNeedsSelection(t *testing.T) {
	_, cfg := setup(t)
	r := zenavo(t, cfg, "", nil, "logs", "-p", "shop")
	if r.code == 0 || !strings.Contains(r.stderr, "no service selected") {
		t.Fatalf("got %+v", r)
	}
}

func TestDeployAcknowledgesInitAndFollowsJob(t *testing.T) {
	f, cfg := setup(t)
	r := zenavo(t, cfg, "", nil, "deploy", "-p", "shop")
	if r.code == 0 || !strings.Contains(r.stderr, "--acknowledge-init") {
		t.Fatalf("without ack: %+v", r)
	}
	r = zenavo(t, cfg, "", nil, "deploy", "-p", "shop", "--yes")
	if r.code != 0 {
		t.Fatalf("deploy: %+v", r)
	}
	if strings.Count(r.stderr, "step 1") != 1 || !strings.Contains(r.stderr, "step 2") || !strings.Contains(r.stderr, "web: building") {
		t.Fatalf("log not followed once: %q", r.stderr)
	}
	f.jobPolls, f.jobFinal = 0, "failed"
	r = zenavo(t, cfg, "", nil, "deploy", "web", "-p", "shop")
	if r.code != 1 || !strings.Contains(r.stderr, "job job_1 failed") {
		t.Fatalf("failed job: %+v", r)
	}
	r = zenavo(t, cfg, "", nil, "service", "restart", "-p", "shop", "-s", "web", "--detach")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "job_1" {
		t.Fatalf("detach: %+v", r)
	}
}

func TestEnvKeepsUnknownFieldsAndMasks(t *testing.T) {
	f, cfg := setup(t)
	r := zenavo(t, cfg, "", nil, "env", "ls", "-p", "shop", "-s", "web")
	if r.code != 0 || strings.Contains(r.stdout, "hunter2") || !strings.Contains(r.stdout, "${POSTGRES_HOST}") {
		t.Fatalf("ls: %+v", r)
	}
	r = zenavo(t, cfg, "", nil, "env", "ls", "-p", "shop", "-s", "web", "--reveal")
	if !strings.Contains(r.stdout, "hunter2") {
		t.Fatalf("reveal: %+v", r)
	}
	// A bare KEY reads its value from stdin, so it never appears in argv.
	r = zenavo(t, cfg, "s3cret\n", nil, "env", "set", "-p", "shop", "-s", "web", "PORT=8080", "API_KEY")
	if r.code != 0 {
		t.Fatalf("set: %+v", r)
	}
	if strings.Contains(r.stderr, "s3cret") {
		t.Fatal("secret echoed")
	}
	env := f.manifest["services"].([]any)[0].(map[string]any)["env"].(map[string]any)
	if env["PORT"] != "8080" || env["API_KEY"] != "s3cret" || env["SECRET"] != "hunter2" || f.manifest["x_unknown"] != "keep me" || f.revision != 4 {
		t.Fatalf("manifest after set: %v rev %d", f.manifest, f.revision)
	}
	r = zenavo(t, cfg, "", nil, "env", "unset", "-p", "shop", "-s", "web", "MISSING")
	if r.code == 0 || f.revision != 4 {
		t.Fatalf("unset missing: %+v", r)
	}
	r = zenavo(t, cfg, "", nil, "env", "unset", "-p", "shop", "-s", "web", "PORT")
	env = f.manifest["services"].([]any)[0].(map[string]any)["env"].(map[string]any)
	if _, ok := env["PORT"]; r.code != 0 || ok || f.revision != 5 {
		t.Fatalf("unset: %+v, env %v", r, env)
	}
}

func TestDomainCustomEditsManifest(t *testing.T) {
	f, cfg := setup(t)
	r := zenavo(t, cfg, "", nil, "domain", "add", "www.example.com", "-p", "shop", "-s", "web")
	if r.code != 0 {
		t.Fatalf("add: %+v", r)
	}
	port := f.manifest["services"].([]any)[0].(map[string]any)["ports"].([]any)[0].(map[string]any)
	if port["domain"] != "www.example.com" {
		t.Fatalf("port %v", port)
	}
}

func TestLogsFollowAndExecExitCode(t *testing.T) {
	_, cfg := setup(t)
	r := zenavo(t, cfg, "", nil, "logs", "-f", "-p", "shop", "-s", "web")
	if r.code != 0 || r.stdout != "hello\nworld\n" {
		t.Fatalf("logs: %+v", r)
	}
	r = zenavo(t, cfg, "", nil, "service", "exec", "-p", "shop", "-s", "web", "--", "ls", "-la")
	if r.code != 3 || r.stdout != "boom\n" {
		t.Fatalf("exec: %+v", r)
	}
}

func TestProjectCreate(t *testing.T) {
	f, cfg := setup(t)
	r := zenavo(t, cfg, "", nil, "project", "create", "--git", "https://github.com/acme/blog.git")
	if r.code != 0 {
		t.Fatalf("create: %+v", r)
	}
	body := f.bodies["POST /api/projects"]
	m := body["manifest"].(map[string]any)
	svc := m["services"].([]any)[0].(map[string]any)
	if body["server_id"] != "srv_1" || m["name"] != "blog" || svc["name"] != "blog" || svc["source"].(map[string]any)["git"] != "https://github.com/acme/blog.git" {
		t.Fatalf("body %v", body)
	}
	c, _ := config.Load(cfg)
	if c.Project != "proj_new" {
		t.Fatalf("context %+v", c)
	}
}

func TestUsageErrors(t *testing.T) {
	_, cfg := setup(t)
	for _, args := range [][]string{{"bogus"}, {"project", "get", "a", "b"}, {"auth", "login", "extra"}} {
		r := zenavo(t, cfg, "", nil, args...)
		if r.code != 2 {
			t.Errorf("%v: code %d, stderr %q", args, r.code, r.stderr)
		}
	}
	r := zenavo(t, cfg, "", nil, "deploy", "--help")
	if r.code != 0 || !strings.Contains(r.stderr, "--acknowledge-init") || !strings.Contains(r.stderr, "Global flags") {
		t.Fatalf("help: %+v", r)
	}
}

func TestNameFrom(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/My_Blog.git": "my-blog",
		"ghcr.io/acme/api:1.2":                "api",
		"nginx":                               "nginx",
		"postgres@sha256:abc":                 "postgres",
	}
	for in, want := range cases {
		if got := nameFrom(in); got != want {
			t.Errorf("nameFrom(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeviceLogin(t *testing.T) {
	deviceUnit = time.Millisecond
	f := newFakeAPI(t)
	f.devicePolls = []int{202, 429, 202}
	opened := ""
	openBrowser = func(url string) error { opened = url; return nil }
	cfg := filepath.Join(t.TempDir(), "config.json")
	r := zenavo(t, cfg, "", nil, "auth", "login", "--api", f.URL)
	if r.code != 0 {
		t.Fatalf("login: %+v", r)
	}
	if !strings.Contains(r.stderr, "BCDF-GHJK") || !strings.Contains(r.stderr, f.URL+"/device") || strings.Contains(r.stderr, "evil.example") {
		t.Fatalf("prompt: %q", r.stderr)
	}
	if opened != "" {
		t.Fatal("opened a browser without a terminal")
	}
	if strings.Contains(r.stderr+r.stdout, "dc_secret") || strings.Contains(r.stderr+r.stdout, token) {
		t.Fatal("printed a secret")
	}
	c, _ := config.Load(cfg)
	if c.Token != token || c.API != f.URL {
		t.Fatalf("saved %+v", c)
	}

	f.devicePolls = []int{202, 403}
	r = zenavo(t, filepath.Join(t.TempDir(), "c.json"), "", nil, "auth", "login", "--api", f.URL)
	if r.code != 1 || !strings.Contains(r.stderr, "denied") {
		t.Fatalf("denied: %+v", r)
	}
}

func TestDeviceLoginScope(t *testing.T) {
	deviceUnit = time.Millisecond
	f := newFakeAPI(t)
	openBrowser = func(string) error { return nil }

	// Default: full, as before, and the CLI says which client it is.
	cfg := filepath.Join(t.TempDir(), "config.json")
	f.deviceGrant = `"scope":"full","project_ids":null`
	r := zenavo(t, cfg, "", nil, "auth", "login", "--api", f.URL)
	if r.code != 0 || f.deviceStart["scope"] != "full" || f.deviceStart["client"] != "cli" || f.deviceStart["client_name"] == "" {
		t.Fatalf("default: %+v, body %v", r, f.deviceStart)
	}
	if c, _ := config.Load(cfg); c.Scope != "full" || c.ProjectIDs != nil {
		t.Fatalf("saved %+v", c)
	}

	// The approver narrowed deploy to read and limited the key to one project: the CLI saves what was granted.
	f.deviceGrant = `"scope":"read","project_ids":["proj_shop"]`
	r = zenavo(t, cfg, "", nil, "auth", "login", "--scope", "deploy", "--api", f.URL)
	if r.code != 0 || f.deviceStart["scope"] != "deploy" || !strings.Contains(r.stderr, "Requesting a deploy key") {
		t.Fatalf("deploy: %+v, body %v", r, f.deviceStart)
	}
	c, _ := config.Load(cfg)
	if c.Scope != "read" || len(c.ProjectIDs) != 1 || c.ProjectIDs[0] != "proj_shop" || c.Token != token {
		t.Fatalf("saved %+v", c)
	}
	if !strings.Contains(r.stderr, "Key access: read") || !strings.Contains(r.stderr, "proj_shop") {
		t.Fatalf("login output: %q", r.stderr)
	}

	// An API from before scopes returns none: the key is full and the CLI says so.
	f.deviceGrant = ""
	cfg2 := filepath.Join(t.TempDir(), "config.json")
	r = zenavo(t, cfg2, "", nil, "auth", "login", "--scope", "read", "--api", f.URL)
	if c, _ := config.Load(cfg2); r.code != 0 || c.Scope != "full" || !strings.Contains(r.stderr, "full access, not read") {
		t.Fatalf("old API: %+v, saved %+v", r, c)
	}

	// Bad values and --scope with --with-token are usage errors; nothing is requested.
	f.deviceStart = nil
	for _, args := range [][]string{{"--scope", "admin"}, {"--scope", "read", "--with-token"}} {
		r = zenavo(t, filepath.Join(t.TempDir(), "c.json"), token+"\n", nil, append([]string{"auth", "login", "--api", f.URL}, args...)...)
		if r.code != 2 || f.deviceStart != nil {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestAuthStatusShowsScope(t *testing.T) {
	deviceUnit = time.Millisecond
	f := newFakeAPI(t)
	openBrowser = func(string) error { return nil }
	cfg := filepath.Join(t.TempDir(), "config.json")
	f.deviceGrant = `"scope":"deploy","project_ids":["proj_shop","proj_blog"]`
	if r := zenavo(t, cfg, "", nil, "auth", "login", "--scope", "deploy", "--api", f.URL); r.code != 0 {
		t.Fatalf("login: %+v", r)
	}
	r := zenavo(t, cfg, "", nil, "auth", "status")
	if r.code != 0 || !strings.Contains(r.stdout, "Access: deploy (deploy and configure existing projects), limited to 2 project(s): proj_shop, proj_blog") {
		t.Fatalf("status: %+v", r)
	}
	r = zenavo(t, cfg, "", nil, "auth", "status", "--json")
	var out struct {
		Scope      string   `json:"scope"`
		ProjectIDs []string `json:"project_ids"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || out.Scope != "deploy" || len(out.ProjectIDs) != 2 {
		t.Fatalf("status --json: %+v, %v", r, err)
	}
	// ZENAVO_TOKEN is another key: the saved scope does not describe it.
	r = zenavo(t, cfg, "", map[string]string{"ZENAVO_TOKEN": token}, "auth", "status")
	if r.code != 0 || strings.Contains(r.stdout, "deploy") || !strings.Contains(r.stdout, "set when the key was created") {
		t.Fatalf("env status: %+v", r)
	}
	// A pasted key has no known scope; logging in with one clears the old values.
	if r := zenavo(t, cfg, token+"\n", nil, "auth", "login", "--with-token", "--api", f.URL); r.code != 0 {
		t.Fatalf("with-token: %+v", r)
	}
	if c, _ := config.Load(cfg); c.Scope != "" || c.ProjectIDs != nil {
		t.Fatalf("stale scope kept: %+v", c)
	}
	// Signing in with full access shows "all projects".
	f.deviceGrant = `"scope":"full","project_ids":null`
	zenavo(t, cfg, "", nil, "auth", "login", "--api", f.URL)
	if r := zenavo(t, cfg, "", nil, "auth", "status"); !strings.Contains(r.stdout, "Access: full, all projects") {
		t.Fatalf("full status: %+v", r)
	}
	zenavo(t, cfg, "", nil, "auth", "logout")
	if c, _ := config.Load(cfg); c.Token != "" || c.Scope != "" || c.ProjectIDs != nil {
		t.Fatalf("logout kept %+v", c)
	}
}

func TestForbiddenScopeHints(t *testing.T) {
	f, cfg := setup(t)
	cases := []struct{ detail, want string }{
		{"This access token has the read scope; this needs deploy", "zenavo auth login --scope deploy"},
		{"This access token has the deploy scope; this needs full", "zenavo auth login --scope full"},
		{"This access token is limited to some projects and cannot do this", "limited to some projects"},
		{"Access tokens cannot do this; use the Zenavo console", "do it in the console: " + f.URL},
	}
	for _, c := range cases {
		f.forbid = map[string]string{"POST /api/projects/proj_shop/services/web/restart": c.detail}
		r := zenavo(t, cfg, "", nil, "service", "restart", "-p", "shop", "-s", "web", "--detach")
		if r.code != 1 || !strings.Contains(r.stderr, c.detail) || !strings.Contains(r.stderr, c.want) {
			t.Errorf("%q: %+v", c.detail, r)
		}
	}
	// A 403 for another reason (a team role) gets no scope hint.
	f.forbid = map[string]string{"POST /api/projects/proj_shop/services/web/restart": "Only owners and admins can do this"}
	r := zenavo(t, cfg, "", nil, "service", "restart", "-p", "shop", "-s", "web", "--detach")
	if r.code != 1 || strings.Contains(r.stderr, "auth login") || strings.Contains(r.stderr, "console") {
		t.Fatalf("other 403: %+v", r)
	}
}
