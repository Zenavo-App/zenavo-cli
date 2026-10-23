package cli

import (
	"crypto/rand"
	"flag"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Zenavo-App/zenavo-cli/internal/api"
)

// generatedPassword is replaced by the control plane with a random password when the configuration is saved, so a
// secret never passes through the CLI. Every occurrence within one service shares one password.
const generatedPassword = "${PASSWORD}"

type addOptions struct {
	image, git, database         string
	branch, root, dockerfile     string
	ports, volumes, env, secrets multiFlag
	envFile                      string
	deploy, detach               bool
}

func serviceAddCmd() *command {
	var o addOptions
	return &command{
		name: "add", args: "[name]", summary: "Add a service to the project",
		help: `Adds a service from a Docker image, a Git repository or a database, as the console's
Add Service does. The name defaults to one from the source.

  zenavo service add --database postgresql
  zenavo service add api --git https://github.com/acme/api --branch main --port 8080
  zenavo service add search --image elasticsearch:9.3.3 --port 9200/http \
    --volume /usr/share/elasticsearch/data --secret ELASTIC_PASSWORD --deploy

--secret KEY gives KEY a password the control plane generates when it saves the service; the CLI never
sees it. All --secret keys of one service share that password. Read it in the console, or with
zenavo env ls --reveal.

A database gets a volume, a generated password, a health check and variables the other services can
reference, such as ${POSTGRESQL_HOST}. Kinds: postgresql, mysql, mariadb, mongodb, redis, valkey;
pick a version with kind:version (postgresql:17).`,
		flags: func(fs *flag.FlagSet) {
			fs.StringVar(&o.image, "image", "", "a Docker `image`")
			fs.StringVar(&o.git, "git", "", "a Git repository `URL`")
			fs.StringVar(&o.database, "database", "", "a database `kind[:version]`")
			fs.StringVar(&o.branch, "branch", "", "Git `branch` or tag (default: the repository's default branch)")
			fs.StringVar(&o.root, "root", "", "Git root `directory` that holds the service (default: /)")
			fs.StringVar(&o.dockerfile, "dockerfile", "", "Dockerfile `path` in the root directory (default: Dockerfile)")
			fs.Var(&o.ports, "port", "`port[/http|tcp|udp]` (repeatable; default: detected)")
			fs.Var(&o.volumes, "volume", "a volume at `[id:]path` (repeatable)")
			fs.Var(&o.env, "env", "variable `KEY=value` (repeatable)")
			fs.StringVar(&o.envFile, "env-file", "", "read KEY=value lines from a dotenv `file`")
			fs.Var(&o.secrets, "secret", "variable `KEY` set to a generated password (repeatable)")
			fs.BoolVar(&o.deploy, "deploy", false, "deploy the service after adding it")
			fs.BoolVar(&o.detach, "detach", false, "with --deploy, do not wait for the job")
		},
		run: func(a *App, _ *flag.FlagSet, args []string) error {
			if err := maxArgs(args, 1); err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = strings.TrimSpace(args[0])
			}
			return a.addService(name, &o)
		},
	}
}

func (a *App) addService(name string, o *addOptions) error {
	sources := 0
	for _, s := range []string{o.image, o.git, o.database} {
		if s != "" {
			sources++
		}
	}
	if sources != 1 {
		return usagef("give one source: --image, --git or --database")
	}
	if o.git == "" && (o.branch != "" || o.root != "" || o.dockerfile != "") {
		return usagef("--branch, --root and --dockerfile apply to --git")
	}
	if o.database != "" && (len(o.ports) > 0 || len(o.volumes) > 0) {
		return usagef("a database comes with its port and volume; --port and --volume do not apply")
	}
	if o.detach && !o.deploy {
		return usagef("--detach applies to --deploy")
	}
	env, err := addEnv(o)
	if err != nil {
		return err
	}
	ports, err := parsePorts(o.ports)
	if err != nil {
		return err
	}
	volumes, err := parseVolumes(o.volumes)
	if err != nil {
		return err
	}
	var db *dbKind
	dbVersion := ""
	if o.database != "" {
		if db, dbVersion, err = parseDatabase(o.database); err != nil {
			return err
		}
	}

	p, err := a.currentProject()
	if err != nil {
		return err
	}
	base := name
	if base == "" {
		switch {
		case db != nil:
			base = db.kind
		default:
			base = nameFrom(o.image + o.git)
		}
		if base == "" {
			base = "app"
		}
	}

	// Look up what the source needs before changing anything: the default branch and the port.
	src := map[string]any{}
	switch {
	case o.image != "":
		src["image"] = o.image
		if len(o.ports) == 0 {
			ports = a.imagePorts(p.TeamID, o.image)
		}
	case o.git != "":
		repo := gitURL(o.git)
		branch := o.branch
		if branch == "" {
			var refs struct {
				URL           string `json:"url"`
				DefaultBranch string `json:"default_branch"`
			}
			if err := a.post("/git/refs", map[string]string{"url": repo}, &refs); err != nil {
				return fmt.Errorf("could not read the repository's branches (%w); pass --branch", err)
			}
			branch = refs.DefaultBranch
			if refs.URL != "" {
				repo = refs.URL
			}
		}
		context := strings.Trim(o.root, "/")
		if context == "" {
			context = "."
		}
		dockerfile := o.dockerfile
		if dockerfile == "" {
			dockerfile = "Dockerfile"
		}
		src = map[string]any{"git": repo, "ref": branch, "context": context, "dockerfile": dockerfile, "auto_deploy": false}
		if len(o.ports) == 0 {
			ports = a.gitPort(p.TeamID, src, base)
		}
	}

	m, err := a.loadManifest(p)
	if err != nil {
		return err
	}
	services, _ := m.manifest["services"].([]any)
	var existing []map[string]any
	for _, raw := range services {
		if s, ok := raw.(map[string]any); ok {
			existing = append(existing, s)
		}
	}
	if name != "" {
		for _, s := range existing {
			if n, _ := s["name"].(string); strings.EqualFold(n, name) {
				return fmt.Errorf("the project already has a service named %q", n)
			}
		}
	} else {
		name = uniqueName(base, existing)
	}
	used := map[string]bool{}
	for _, id := range m.doc.UsedServiceIDs {
		used[id] = true
	}
	for _, s := range existing {
		used[serviceKey(s)] = true
	}
	id := newServiceID(used)
	hostname := newHostname(name, existing)

	svc := map[string]any{"id": id, "name": name, "hostname": hostname, "source": src, "ports": portSpecs(ports)}
	if len(volumes) > 0 {
		svc["volumes"] = volumes
	}
	if db != nil {
		for k, v := range db.build(hostname, dbVersion) {
			svc[k] = v
		}
	}
	if len(env) > 0 {
		merged, _ := svc["env"].(map[string]any)
		if merged == nil {
			merged = map[string]any{}
		}
		for k, v := range env {
			if _, taken := merged[k]; taken {
				return fmt.Errorf("%s is set by the %s service itself", k, db.kind)
			}
			merged[k] = v
		}
		svc["env"] = merged
	}
	m.manifest["services"] = append(services, svc)
	if err := a.saveManifest(m); err != nil {
		return err
	}

	if a.jsonOut && !o.deploy {
		return a.printJSON(map[string]any{"id": id, "name": name, "hostname": hostname, "project_id": p.ID})
	}
	a.info("Added %s (%s) to %s; other services reach it at %s.zenavo.internal", name, id, p.Name, hostname)
	if len(o.secrets) > 0 {
		a.info("%s got a generated password; read it in the console or with: zenavo env ls -s %s --reveal",
			strings.Join(o.secrets, ", "), id)
	}
	if o.deploy {
		return a.startDeploy(p, id, false, o.detach)
	}
	a.info("Deploy it with: zenavo service deploy %s", id)
	return nil
}

// addEnv merges --env-file, --env and --secret; a key given twice is a mistake rather than a silent override.
func addEnv(o *addOptions) (map[string]any, error) {
	env := map[string]any{}
	set := func(k string, v any, from string) error {
		if !envKey.MatchString(k) {
			return fmt.Errorf("invalid variable name %q", k)
		}
		if _, dup := env[k]; dup {
			return fmt.Errorf("%s is given twice (%s)", k, from)
		}
		env[k] = v
		return nil
	}
	if o.envFile != "" {
		pairs, err := readDotenv(o.envFile)
		if err != nil {
			return nil, err
		}
		for _, kv := range pairs {
			if err := set(kv[0], kv[1], "--env-file"); err != nil {
				return nil, err
			}
		}
	}
	for _, kv := range o.env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, usagef("--env needs KEY=value, got %q; for a secret use --secret %s", kv, kv)
		}
		if err := set(k, v, "--env"); err != nil {
			return nil, err
		}
	}
	for _, k := range o.secrets {
		if err := set(k, generatedPassword, "--secret"); err != nil {
			return nil, err
		}
	}
	return env, nil
}

type portRow struct {
	port int
	typ  string
}

// nonHTTP are well-known ports of protocols other than HTTP, which default to TCP (as in the console).
var nonHTTP = map[int]bool{21: true, 22: true, 25: true, 53: true, 110: true, 143: true, 465: true, 587: true, 993: true,
	995: true, 1433: true, 1521: true, 1883: true, 2181: true, 3306: true, 4222: true, 5432: true, 5672: true, 6379: true,
	6380: true, 8883: true, 9042: true, 9092: true, 11211: true, 27017: true}

func defaultType(port int) string {
	if nonHTTP[port] {
		return "TCP"
	}
	return "HTTP"
}

func parsePorts(flags []string) ([]portRow, error) {
	var out []portRow
	for _, f := range flags {
		num, typ, hasType := strings.Cut(f, "/")
		n, err := strconv.Atoi(num)
		if err != nil || n < 1 || n > 65535 {
			return nil, usagef("--port needs a number from 1 to 65535, got %q", f)
		}
		t := defaultType(n)
		if hasType {
			t = strings.ToUpper(typ)
			if t != "HTTP" && t != "TCP" && t != "UDP" {
				return nil, usagef("--port type must be http, tcp or udp, got %q", typ)
			}
		}
		out = append(out, portRow{n, t})
	}
	return out, nil
}

// portSpecs names ports as the console does: the first HTTP port "web", a first non-HTTP port "main", others port<n>.
func portSpecs(rows []portRow) []any {
	used := map[string]bool{}
	out := []any{}
	for i, p := range rows {
		id := fmt.Sprintf("port%d", p.port)
		switch {
		case p.typ == "HTTP" && !used["web"]:
			id = "web"
		case i == 0 && p.typ != "HTTP":
			id = "main"
		}
		for used[id] {
			id += "x"
		}
		used[id] = true
		out = append(out, map[string]any{"id": id, "port": p.port, "type": p.typ, "publish": nil, "bind": "127.0.0.1", "domain": nil})
	}
	return out
}

var volumeID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func parseVolumes(flags []string) ([]any, error) {
	var out []any
	ids, paths := map[string]bool{}, map[string]bool{}
	for i, f := range flags {
		id, dir, named := strings.Cut(f, ":")
		if !named {
			id, dir = "data", f
			if i > 0 {
				id = fmt.Sprintf("data-%d", i+1)
			}
		}
		if !volumeID.MatchString(id) {
			return nil, usagef("volume id %q must start with a letter and use lowercase letters, digits and dashes", id)
		}
		if !strings.HasPrefix(dir, "/") || path.Clean(dir) == "/" {
			return nil, usagef("--volume needs an absolute path other than /, got %q", dir)
		}
		dir = path.Clean(dir)
		if ids[id] || paths[dir] {
			return nil, usagef("--volume %q is given twice", f)
		}
		ids[id], paths[dir] = true, true
		out = append(out, map[string]any{"id": id, "path": dir})
	}
	return out, nil
}

// imagePorts asks the control plane which ports the image declares. Failing that, the service starts without one.
func (a *App) imagePorts(teamID, image string) []portRow {
	var r struct {
		Ports []struct {
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
		} `json:"ports"`
	}
	if err := a.get("/images/ports"+api.Q("team_id", teamID, "image", image), &r); err != nil {
		a.info("Could not read the image's ports (%v); add one with --port", err)
		return nil
	}
	var out []portRow
	for _, p := range r.Ports {
		t := defaultType(p.Port)
		if p.Protocol == "udp" {
			t = "UDP"
		}
		out = append(out, portRow{p.Port, t})
	}
	if len(out) == 0 {
		a.info("The image declares no port; add one with --port if the service takes connections")
	}
	return out
}

// gitPort reads the port from the build plan. zbpack images listen on 8080, so that is the default without a Dockerfile.
func (a *App) gitPort(teamID string, src map[string]any, name string) []portRow {
	var plan struct {
		Dockerfile any `json:"dockerfile"`
		Items      []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		} `json:"items"`
	}
	body := map[string]any{"team_id": teamID, "url": src["git"], "ref": src["ref"], "context": src["context"],
		"dockerfile": src["dockerfile"], "service": name}
	if err := a.post("/git/plan", body, &plan); err != nil {
		a.info("Could not read the build plan (%v); add a port with --port if the service takes connections", err)
		return nil
	}
	for _, it := range plan.Items {
		if it.Key == "port" {
			if n, err := strconv.Atoi(fmt.Sprint(it.Value)); err == nil && n > 0 && n <= 65535 {
				return []portRow{{n, "HTTP"}}
			}
		}
	}
	if plan.Dockerfile == nil || plan.Dockerfile == false || plan.Dockerfile == "" {
		return []portRow{{8080, "HTTP"}}
	}
	a.info("The build plan names no port; add one with --port if the service takes connections")
	return nil
}

var githubShorthand = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// gitURL expands owner/repo to its GitHub URL, as the console does; other URLs are kept as given.
func gitURL(s string) string {
	s = strings.TrimSpace(s)
	if githubShorthand.MatchString(s) {
		return "https://github.com/" + s
	}
	return s
}

// serviceKey is a service's ID, or its name in configurations from before IDs.
func serviceKey(s map[string]any) string {
	if id, _ := s["id"].(string); id != "" {
		return id
	}
	n, _ := s["name"].(string)
	return n
}

// newServiceID is a permanent ID as the console makes one: "svc-" and 12 random lowercase letters and digits.
func newServiceID(used map[string]bool) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	for {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		for i := range b {
			b[i] = alphabet[int(b[i])%len(alphabet)]
		}
		if id := "svc-" + string(b); !used[id] {
			return id
		}
	}
}

var validID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

var (
	nonWord      = regexp.MustCompile(`[^a-z0-9]+`)
	leadingDigit = regexp.MustCompile(`^[0-9 ]+`)
)

// idFor makes an ID base from a display name, as the API does: the name when valid, else lowercase words with dashes.
func idFor(name string) string {
	if validID.MatchString(name) {
		return name
	}
	s := leadingDigit.ReplaceAllString(strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(name), " ")), "")
	out := ""
	for _, w := range strings.Fields(s) {
		if out == "" {
			out = w
		} else {
			out += "-" + w
		}
		if len(out) >= 40 {
			break
		}
	}
	if len(out) > 40 {
		out = out[:40]
	}
	if out = strings.TrimRight(out, "-"); out == "" {
		return "service"
	}
	return out
}

// newHostname is a private hostname from the name, unique among the project's IDs and hostnames.
func newHostname(name string, services []map[string]any) string {
	taken := map[string]bool{}
	for _, s := range services {
		taken[serviceKey(s)] = true
		if h, _ := s["hostname"].(string); h != "" {
			taken[h] = true
		}
	}
	base := idFor(name)
	host := base
	for n := 2; taken[host]; n++ {
		host = fmt.Sprintf("%s-%d", base, n)
	}
	return host
}

// uniqueName is a display name not yet in the project (names are unique ignoring case).
func uniqueName(base string, services []map[string]any) string {
	taken := map[string]bool{}
	for _, s := range services {
		n, _ := s["name"].(string)
		taken[strings.ToLower(n)] = true
	}
	name := base
	for n := 2; taken[strings.ToLower(name)]; n++ {
		name = fmt.Sprintf("%s-%d", base, n)
	}
	return name
}

// dbKind is a one-click database, the same as in the console's Add Service → Databases.
type dbKind struct {
	kind     string
	aliases  []string
	versions []string // the first is the default
	build    func(host, version string) map[string]any
}

var envPrefix = regexp.MustCompile(`[^A-Z0-9]+`)

// dbShared are the variables a database exports to the other services, named after its hostname
// ("postgresql-2" → POSTGRESQL_2_…) so two databases of one kind never export the same name.
func dbShared(host, url string, port int) (map[string]any, []any) {
	p := envPrefix.ReplaceAllString(strings.ToUpper(host), "_")
	env := map[string]any{
		p + "_CONNECTION_STRING": url,
		p + "_HOST":              "${CONTAINER_HOSTNAME}.zenavo.internal",
		p + "_PORT":              strconv.Itoa(port),
		p + "_DB_PASSWORD":       generatedPassword,
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	expose := make([]any, len(keys))
	for i, k := range keys {
		expose[i] = k
	}
	return env, expose
}

func dbService(image string, port int, dataPath, url string, env map[string]any, health []string, start int, command []string) func(host string) map[string]any {
	return func(host string) map[string]any {
		shared, expose := dbShared(host, url, port)
		for k, v := range env {
			shared[k] = v
		}
		hc := make([]any, len(health))
		for i, c := range health {
			hc[i] = c
		}
		svc := map[string]any{
			"source":      map[string]any{"image": image},
			"env":         shared,
			"expose":      expose,
			"ports":       []any{map[string]any{"id": "database", "port": port, "type": "TCP"}},
			"volumes":     []any{map[string]any{"id": "data", "path": dataPath}},
			"healthcheck": map[string]any{"command": hc, "interval": 10, "timeout": 5, "retries": 6, "start_period": start},
		}
		if command != nil {
			cmd := make([]any, len(command))
			for i, c := range command {
				cmd[i] = c
			}
			svc["command"] = cmd
		}
		return svc
	}
}

var databases = []dbKind{
	{kind: "postgresql", aliases: []string{"postgres", "pg"}, versions: []string{"18", "17", "16", "15", "14"},
		build: func(host, v string) map[string]any {
			// From 18 the image keeps data in /var/lib/postgresql/<major>/docker and expects the volume one level up.
			modern, _ := strconv.Atoi(v)
			env := map[string]any{"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": generatedPassword, "POSTGRES_DB": "zenavo"}
			data := "/var/lib/postgresql"
			if modern < 18 {
				env["PGDATA"] = "/var/lib/postgresql/data/pgdata"
				data = "/var/lib/postgresql/data"
			}
			return dbService("postgres:"+v, 5432, data, "postgresql://postgres:${PASSWORD}@${CONTAINER_HOSTNAME}.zenavo.internal:5432/zenavo",
				env, []string{"pg_isready", "-U", "postgres"}, 10, nil)(host)
		}},
	{kind: "mysql", versions: []string{"8.4", "9", "8.0"},
		build: func(host, v string) map[string]any {
			return dbService("mysql:"+v, 3306, "/var/lib/mysql", "mysql://root:${PASSWORD}@${CONTAINER_HOSTNAME}.zenavo.internal:3306/zenavo",
				map[string]any{"MYSQL_ROOT_PASSWORD": generatedPassword, "MYSQL_DATABASE": "zenavo"},
				[]string{"sh", "-c", `mysqladmin ping -h 127.0.0.1 -p"$MYSQL_ROOT_PASSWORD"`}, 20, nil)(host)
		}},
	{kind: "mariadb", versions: []string{"11.8", "11.4", "10.11"},
		build: func(host, v string) map[string]any {
			return dbService("mariadb:"+v, 3306, "/var/lib/mysql", "mysql://root:${PASSWORD}@${CONTAINER_HOSTNAME}.zenavo.internal:3306/zenavo",
				map[string]any{"MARIADB_ROOT_PASSWORD": generatedPassword, "MARIADB_DATABASE": "zenavo"},
				[]string{"healthcheck.sh", "--connect", "--innodb_initialized"}, 20, nil)(host)
		}},
	{kind: "mongodb", aliases: []string{"mongo"}, versions: []string{"8.0", "7.0", "6.0"},
		build: func(host, v string) map[string]any {
			return dbService("mongo:"+v, 27017, "/data/db", "mongodb://mongo:${PASSWORD}@${CONTAINER_HOSTNAME}.zenavo.internal:27017",
				map[string]any{"MONGO_INITDB_ROOT_USERNAME": "mongo", "MONGO_INITDB_ROOT_PASSWORD": generatedPassword},
				[]string{"mongosh", "--quiet", "--eval", `db.adminCommand("ping")`}, 20, nil)(host)
		}},
	{kind: "redis", versions: []string{"8-alpine", "7.4-alpine", "7.2-alpine"},
		build: func(host, v string) map[string]any {
			return dbService("redis:"+v, 6379, "/data", "redis://:${PASSWORD}@${CONTAINER_HOSTNAME}.zenavo.internal:6379",
				map[string]any{"REDIS_PASSWORD": generatedPassword},
				[]string{"sh", "-c", `redis-cli -a "$REDIS_PASSWORD" --no-auth-warning ping`}, 5,
				[]string{"sh", "-c", `exec redis-server --requirepass "$REDIS_PASSWORD" --appendonly yes`})(host)
		}},
	{kind: "valkey", versions: []string{"8-alpine", "7.2-alpine"},
		build: func(host, v string) map[string]any {
			return dbService("valkey/valkey:"+v, 6379, "/data", "redis://:${PASSWORD}@${CONTAINER_HOSTNAME}.zenavo.internal:6379",
				map[string]any{"VALKEY_PASSWORD": generatedPassword},
				[]string{"sh", "-c", `valkey-cli -a "$VALKEY_PASSWORD" --no-auth-warning ping`}, 5,
				[]string{"sh", "-c", `exec valkey-server --requirepass "$VALKEY_PASSWORD" --appendonly yes`})(host)
		}},
}

// parseDatabase reads kind[:version]; a version may be given with or without its -alpine suffix.
func parseDatabase(s string) (*dbKind, string, error) {
	kind, version, _ := strings.Cut(strings.ToLower(strings.TrimSpace(s)), ":")
	var kinds []string
	for i := range databases {
		d := &databases[i]
		kinds = append(kinds, d.kind)
		if d.kind != kind && !contains(d.aliases, kind) {
			continue
		}
		if version == "" {
			return d, d.versions[0], nil
		}
		for _, v := range d.versions {
			if v == version || strings.TrimSuffix(v, "-alpine") == version {
				return d, v, nil
			}
		}
		return nil, "", usagef("%s versions: %s", d.kind, strings.Join(d.versions, ", "))
	}
	return nil, "", usagef("unknown database %q; choose %s", kind, strings.Join(kinds, ", "))
}
