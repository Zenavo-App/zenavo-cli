package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// added returns the last service of the saved configuration and checks the others were kept as they were.
func added(t *testing.T, f *fakeAPI) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.manifest["x_unknown"] != "keep me" {
		t.Errorf("unknown field lost: %v", f.manifest)
	}
	services := f.manifest["services"].([]any)
	if len(services) != 3 {
		t.Fatalf("services: %v", services)
	}
	web := services[0].(map[string]any)
	if web["env"].(map[string]any)["SECRET"] != "hunter2" {
		t.Errorf("existing service changed: %v", web)
	}
	return services[2].(map[string]any)
}

func called(f *fakeAPI, prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

var serviceIDFormat = regexp.MustCompile(`^svc-[a-z0-9]{12}$`)

func TestServiceAddImage(t *testing.T) {
	f, cfg := setup(t)
	f.usedIDs = []string{"svc-removed00001"}
	r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "add", "search",
		"--image", "ghcr.io/acme/search:9.3.3", "--port", "9200/http", "--port", "9300",
		"--volume", "/usr/share/elasticsearch/data/", "--volume", "logs:/var/log/es",
		"--env", "ES_JAVA_OPTS=-Xms2g -Xmx2g", "--secret", "ELASTIC_PASSWORD", "--deploy")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	svc := added(t, f)
	id := svc["id"].(string)
	if !serviceIDFormat.MatchString(id) {
		t.Errorf("id %q", id)
	}
	if svc["name"] != "search" || svc["hostname"] != "search" {
		t.Errorf("name/hostname: %v", svc)
	}
	if !reflect.DeepEqual(svc["source"], map[string]any{"image": "ghcr.io/acme/search:9.3.3"}) {
		t.Errorf("source: %v", svc["source"])
	}
	wantPorts := []any{
		map[string]any{"id": "web", "port": float64(9200), "type": "HTTP", "publish": nil, "bind": "127.0.0.1", "domain": nil},
		map[string]any{"id": "port9300", "port": float64(9300), "type": "HTTP", "publish": nil, "bind": "127.0.0.1", "domain": nil},
	}
	if !reflect.DeepEqual(svc["ports"], wantPorts) {
		t.Errorf("ports: %v", svc["ports"])
	}
	wantVolumes := []any{map[string]any{"id": "data", "path": "/usr/share/elasticsearch/data"}, map[string]any{"id": "logs", "path": "/var/log/es"}}
	if !reflect.DeepEqual(svc["volumes"], wantVolumes) {
		t.Errorf("volumes: %v", svc["volumes"])
	}
	// The secret is the placeholder the control plane fills in on save; the CLI never has a value.
	wantEnv := map[string]any{"ES_JAVA_OPTS": "-Xms2g -Xmx2g", "ELASTIC_PASSWORD": "${PASSWORD}"}
	if !reflect.DeepEqual(svc["env"], wantEnv) {
		t.Errorf("env: %v", svc["env"])
	}
	if called(f, "GET /api/images/ports") {
		t.Error("ports were given; the image should not be inspected")
	}
	if !called(f, "POST /api/projects/proj_shop/services/"+id+"/deploy") {
		t.Errorf("not deployed: %v", f.calls)
	}
	if strings.Contains(r.stdout+r.stderr, "hunter2") {
		t.Error("an existing secret was printed")
	}
	if !strings.Contains(r.stderr, "search.zenavo.internal") || !strings.Contains(r.stderr, "ELASTIC_PASSWORD got a generated password") {
		t.Errorf("stderr: %s", r.stderr)
	}
	if f.bodies["PUT /api/projects/proj_shop"]["revision"] != float64(3) {
		t.Errorf("revision: %v", f.bodies["PUT /api/projects/proj_shop"])
	}
}

func TestServiceAddImageDetectsPorts(t *testing.T) {
	f, cfg := setup(t)
	r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "--json", "service", "add", "--image", "docker.io/library/Web-App:1@sha256:abc")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	svc := added(t, f)
	// The name and hostname come from the image, lowercased.
	if svc["name"] != "web-app" || svc["hostname"] != "web-app" {
		t.Errorf("name: %v", svc)
	}
	ports := svc["ports"].([]any)
	got := []string{}
	for _, p := range ports {
		m := p.(map[string]any)
		got = append(got, m["id"].(string)+"="+m["type"].(string))
	}
	if strings.Join(got, ",") != "main=TCP,web=HTTP,port9000=UDP" {
		t.Errorf("ports: %v", got)
	}
	if _, ok := svc["volumes"]; ok {
		t.Errorf("no volume was asked for: %v", svc)
	}
	if !strings.Contains(r.stdout, `"id": "svc-`) || called(f, "POST /api/projects/proj_shop/services/svc-") {
		t.Errorf("json or deploy: %+v %v", r, f.calls)
	}
}

func TestServiceAddDefaultNameIsUnique(t *testing.T) {
	f, cfg := setup(t)
	// "db" is a name, and an existing service's hostname, so the new one gets "db-2" for both.
	if r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "add", "--image", "ghcr.io/acme/db:1", "--port", "80"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	svc := added(t, f)
	if svc["name"] != "db-2" || svc["hostname"] != "db-2" {
		t.Errorf("%v", svc)
	}
}

func TestServiceAddDatabase(t *testing.T) {
	f, cfg := setup(t)
	if r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "add", "--database", "postgres:17", "--env", "TZ=UTC"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	svc := added(t, f)
	if svc["name"] != "postgresql" || svc["hostname"] != "postgresql" {
		t.Errorf("name: %v", svc)
	}
	if !reflect.DeepEqual(svc["source"], map[string]any{"image": "postgres:17"}) {
		t.Errorf("source: %v", svc["source"])
	}
	env := svc["env"].(map[string]any)
	want := map[string]any{
		"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": "${PASSWORD}", "POSTGRES_DB": "zenavo", "PGDATA": "/var/lib/postgresql/data/pgdata",
		"POSTGRESQL_CONNECTION_STRING": "postgresql://postgres:${PASSWORD}@${CONTAINER_HOSTNAME}.zenavo.internal:5432/zenavo",
		"POSTGRESQL_HOST":              "${CONTAINER_HOSTNAME}.zenavo.internal", "POSTGRESQL_PORT": "5432", "POSTGRESQL_DB_PASSWORD": "${PASSWORD}",
		"TZ": "UTC",
	}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("env: %v", env)
	}
	if !reflect.DeepEqual(svc["expose"], []any{"POSTGRESQL_CONNECTION_STRING", "POSTGRESQL_DB_PASSWORD", "POSTGRESQL_HOST", "POSTGRESQL_PORT"}) {
		t.Errorf("expose: %v", svc["expose"])
	}
	if !reflect.DeepEqual(svc["volumes"], []any{map[string]any{"id": "data", "path": "/var/lib/postgresql/data"}}) {
		t.Errorf("volumes: %v", svc["volumes"])
	}
	if !reflect.DeepEqual(svc["ports"], []any{map[string]any{"id": "database", "port": float64(5432), "type": "TCP"}}) {
		t.Errorf("ports: %v", svc["ports"])
	}
	if svc["healthcheck"].(map[string]any)["command"].([]any)[0] != "pg_isready" {
		t.Errorf("healthcheck: %v", svc["healthcheck"])
	}
}

func TestServiceAddDatabaseDefaults(t *testing.T) {
	for _, c := range []struct{ arg, image, prefix, data string }{
		{"postgresql", "postgres:18", "POSTGRESQL", "/var/lib/postgresql"},
		{"redis:7.4", "redis:7.4-alpine", "REDIS", "/data"},
		{"MONGO", "mongo:8.0", "MONGODB", "/data/db"},
		{"valkey", "valkey/valkey:8-alpine", "VALKEY", "/data"},
		{"mariadb:10.11", "mariadb:10.11", "MARIADB", "/var/lib/mysql"},
	} {
		t.Run(c.arg, func(t *testing.T) {
			f, cfg := setup(t)
			if r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "add", "--database", c.arg); r.code != 0 {
				t.Fatalf("%+v", r)
			}
			svc := added(t, f)
			if svc["source"].(map[string]any)["image"] != c.image {
				t.Errorf("image: %v", svc["source"])
			}
			if _, ok := svc["env"].(map[string]any)[c.prefix+"_HOST"]; !ok {
				t.Errorf("env: %v", svc["env"])
			}
			if svc["volumes"].([]any)[0].(map[string]any)["path"] != c.data {
				t.Errorf("volumes: %v", svc["volumes"])
			}
			if _, ok := svc["env"].(map[string]any)["PGDATA"]; ok {
				t.Error("PGDATA is only for PostgreSQL before 18")
			}
		})
	}
}

func TestServiceAddGit(t *testing.T) {
	f, cfg := setup(t)
	r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "add", "--git", "https://github.com/acme/api.git", "--root", "/services/api/")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	svc := added(t, f)
	want := map[string]any{"git": "https://github.com/acme/api.git", "ref": "trunk", "context": "services/api", "dockerfile": "Dockerfile", "auto_deploy": false}
	if !reflect.DeepEqual(svc["source"], want) {
		t.Errorf("source: %v", svc["source"])
	}
	// The plan's Dockerfile build gives the docker icon.
	if svc["name"] != "api" || svc["ports"].([]any)[0].(map[string]any)["port"] != float64(3000) || svc["icon"] != "docker" {
		t.Errorf("%v", svc)
	}
	plan := f.bodies["POST /api/git/plan"]
	if plan["ref"] != "trunk" || plan["context"] != "services/api" || plan["team_id"] != "team_me" {
		t.Errorf("plan request: %v", plan)
	}
}

func TestServiceAddGitWithBranchAndPort(t *testing.T) {
	f, cfg := setup(t)
	r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "add", "worker", "--git", "acme/api", "--branch", "main", "--dockerfile", "Dockerfile.worker", "--port", "9000/tcp")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	svc := added(t, f)
	if src := svc["source"].(map[string]any); src["git"] != "https://github.com/acme/api" || src["ref"] != "main" || src["context"] != "." || src["dockerfile"] != "Dockerfile.worker" {
		t.Errorf("source: %v", src)
	}
	if called(f, "POST /api/git/refs") || called(f, "POST /api/git/plan") {
		t.Errorf("branch and port were given: %v", f.calls)
	}
}

func TestServiceAddGitNeedsBranchWithoutAccess(t *testing.T) {
	f, cfg := setup(t)
	f.forbid = map[string]string{"POST /api/git/refs": "This API key cannot do that"}
	r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "add", "--git", "https://github.com/acme/api")
	if r.code == 0 || !strings.Contains(r.stderr, "pass --branch") {
		t.Fatalf("%+v", r)
	}
	if called(f, "PUT /api/projects/proj_shop") {
		t.Error("saved after failing")
	}
}

func TestServiceAddGitPlanUnavailable(t *testing.T) {
	f, cfg := setup(t)
	f.forbid = map[string]string{"POST /api/git/plan": "This API key cannot do that"}
	r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "add", "--git", "acme/api", "--branch", "main")
	if r.code != 0 || !strings.Contains(r.stderr, "add a port with --port") {
		t.Fatalf("%+v", r)
	}
	if ports := added(t, f)["ports"].([]any); len(ports) != 0 {
		t.Errorf("ports: %v", ports)
	}
}

func TestServiceAddEnvFile(t *testing.T) {
	f, cfg := setup(t)
	file := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(file, []byte("# app\nexport MODE=\"prod\"\nLEVEL=info\n"), 0o600)
	if r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "add", "app", "--image", "nginx:alpine", "--port", "80", "--env-file", file, "--env", "X=1"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if env := added(t, f)["env"]; !reflect.DeepEqual(env, map[string]any{"MODE": "prod", "LEVEL": "info", "X": "1"}) {
		t.Errorf("env: %v", env)
	}
}

func TestServiceAddConflict(t *testing.T) {
	f, cfg := setup(t)
	f.conflict = true
	r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "add", "app", "--image", "nginx:alpine", "--port", "80", "--deploy")
	if r.code == 0 || !strings.Contains(r.stderr, "Configuration changed") {
		t.Fatalf("%+v", r)
	}
	if called(f, "POST /api/projects/proj_shop/services/svc-") {
		t.Error("deployed a service that was not saved")
	}
}

func TestServiceAddRejects(t *testing.T) {
	f, cfg := setup(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"x"}, "give one source"},
		{[]string{"--image", "a", "--git", "b"}, "give one source"},
		{[]string{"--image", "a", "--branch", "main"}, "apply to --git"},
		{[]string{"--database", "redis", "--port", "1"}, "do not apply"},
		{[]string{"--database", "oracle"}, "unknown database"},
		{[]string{"--database", "mysql:5.7"}, "mysql versions: 8.4, 9, 8.0"},
		{[]string{"--image", "a", "--port", "0"}, "1 to 65535"},
		{[]string{"--image", "a", "--port", "80/sctp"}, "http, tcp or udp"},
		{[]string{"--image", "a", "--volume", "relative/path"}, "absolute path"},
		{[]string{"--image", "a", "--volume", "/"}, "absolute path"},
		{[]string{"--image", "a", "--volume", "Data:/x"}, "volume id"},
		{[]string{"--image", "a", "--volume", "/x", "--volume", "/x/"}, "given twice"},
		{[]string{"--image", "a", "--env", "PASSWORD"}, "--secret PASSWORD"},
		{[]string{"--image", "a", "--env", "K=1", "--secret", "K"}, "K is given twice"},
		{[]string{"--image", "a", "--secret", "bad key"}, "invalid variable name"},
		{[]string{"--database", "redis", "--env", "REDIS_PASSWORD=x"}, "set by the redis service"},
		{[]string{"--image", "a", "--detach"}, "--detach applies to --deploy"},
		{[]string{"WEB", "--image", "a", "--port", "80"}, `already has a service named "web"`},
	} {
		r := zenavo(t, cfg, "", nil, append([]string{"-p", "proj_shop", "service", "add"}, c.args...)...)
		if r.code == 0 || !strings.Contains(r.stderr, c.want) {
			t.Errorf("%v: %+v", c.args, r)
		}
	}
	if called(f, "PUT /api/projects/proj_shop") {
		t.Error("a rejected command saved the configuration")
	}
}

func TestIDHelpers(t *testing.T) {
	for in, want := range map[string]string{"web": "web", "My API": "my-api", "2fast  api!": "fast-api", "!!!": "service", "elasticsearch": "elasticsearch",
		strings.Repeat("abcdefghij ", 6): "abcdefghij-abcdefghij-abcdefghij-abcdefg"} {
		if got := idFor(in); got != want {
			t.Errorf("idFor(%q) = %q, want %q", in, got, want)
		}
	}
	used := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := newServiceID(used)
		if !serviceIDFormat.MatchString(id) || used[id] {
			t.Fatalf("id %q", id)
		}
		used[id] = true
	}
	services := []map[string]any{{"id": "svc-1", "name": "Search", "hostname": "search"}, {"name": "search-2"}}
	if got := newHostname("search", services); got != "search-3" {
		t.Errorf("hostname %q", got)
	}
	if got := uniqueName("SEARCH", services); got != "SEARCH-3" {
		t.Errorf("name %q", got)
	}
}
