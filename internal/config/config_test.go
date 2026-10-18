package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizeAPI(t *testing.T) {
	ok := map[string]string{
		"https://zenavo.app":      "https://zenavo.app",
		"https://Zenavo.app/":     "https://zenavo.app",
		"http://localhost:4180":   "http://localhost:4180",
		"http://127.0.0.1:5173":   "http://127.0.0.1:5173",
		"http://app.localhost:80": "http://app.localhost:80",
		" https://c.example.com ": "https://c.example.com",
	}
	for in, want := range ok {
		got, err := NormalizeAPI(in)
		if err != nil || got != want {
			t.Errorf("NormalizeAPI(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"http://zenavo.app", "zenavo.app", "ftp://zenavo.app", "https://u:p@zenavo.app",
		"https://zenavo.app/api", "https://zenavo.app?x=1", "http://localhost.evil.com"} {
		if got, err := NormalizeAPI(in); err == nil {
			t.Errorf("NormalizeAPI(%q) = %q, want an error", in, got)
		}
	}
}

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zenavo", "config.json")
	c := &Config{API: DefaultAPI, Token: "zv_pat_x", Team: "team_a", Project: "proj_a", Service: "web"}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o, want 600", info.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil || *got != *c {
		t.Fatalf("Load = %+v, %v", got, err)
	}
	missing, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || *missing != (Config{}) {
		t.Fatalf("missing file: %+v, %v", missing, err)
	}
}

func TestLoadRefusesReadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permissions")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"token":"zv_pat_x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err = %v, want a permissions error", err)
	}
}

func TestContextClearing(t *testing.T) {
	c := &Config{Team: "team_a", Project: "proj_a", Service: "web"}
	c.SetProject("proj_a")
	if c.Service != "web" {
		t.Fatal("same project cleared the service")
	}
	c.SetProject("proj_b")
	if c.Service != "" {
		t.Fatal("new project kept the old service")
	}
	c.Project, c.Service = "proj_b", "api"
	c.SetTeam("team_b")
	if c.Project != "" || c.Service != "" || c.Team != "team_b" {
		t.Fatalf("SetTeam left %+v", c)
	}
}
