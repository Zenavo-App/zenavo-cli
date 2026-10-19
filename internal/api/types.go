package api

import "encoding/json"

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type Team struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Personal bool   `json:"personal"`
	Role     string `json:"role"`
}

// Me is GET /api/auth/me.
type Me struct {
	User  User   `json:"user"`
	Teams []Team `json:"teams"`
}

type Server struct {
	ID            string   `json:"id"`
	TeamID        string   `json:"team_id"`
	Name          string   `json:"name"`
	Host          string   `json:"host"`
	Status        string   `json:"status"`
	StatusMessage *string  `json:"status_message"`
	K3sVersion    *string  `json:"k3s_version"`
	Arch          *string  `json:"arch"`
	OS            *string  `json:"os"`
	LastSeen      *float64 `json:"last_seen"`
}

type Port struct {
	ID     string  `json:"id"`
	Port   int     `json:"port"`
	Type   string  `json:"type"`
	Domain *string `json:"domain"`
}

type ServicePreview struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Source    string   `json:"source"`
	Variables []string `json:"variables"`
	Ports     []Port   `json:"ports"`
}

// Key is the service's ID, which API routes take; servers from before service IDs only have the name.
func (s ServicePreview) Key() string {
	if s.ID != "" {
		return s.ID
	}
	return s.Name
}

// Project is a project summary.
type Project struct {
	ID         string           `json:"id"`
	TeamID     string           `json:"team_id"`
	Name       string           `json:"name"`
	ServerID   string           `json:"server_id"`
	Revision   int              `json:"revision"`
	ActiveJob  *string          `json:"active_job"`
	RuntimeJob *string          `json:"runtime_job"`
	Running    bool             `json:"running"`
	Created    float64          `json:"created"`
	Services   []ServicePreview `json:"services"`
	Blockers   []string         `json:"blockers"`
	Ready      bool             `json:"ready"`
}

// ServiceState is one row of GET /api/projects/{pid}/runtime.
type ServiceState struct {
	Service     string  `json:"Service"`
	State       string  `json:"State"`
	Health      *string `json:"Health"`
	Ready       int     `json:"Ready"`
	Desired     int     `json:"Desired"`
	Restarts    int     `json:"Restarts"`
	CPUMilli    *int64  `json:"CPUMilli"`
	MemoryBytes *int64  `json:"MemoryBytes"`
}

type Runtime struct {
	Services []ServiceState `json:"services"`
	Deployed bool           `json:"deployed"`
}

type Job struct {
	ID        string            `json:"id"`
	ProjectID string            `json:"project_id"`
	Kind      string            `json:"kind"`
	Status    string            `json:"status"`
	Created   float64           `json:"created"`
	Finished  *float64          `json:"finished"`
	Log       *string           `json:"log,omitempty"`
	Progress  map[string]string `json:"progress"`
}

// Done reports whether the job reached a final status.
func (j *Job) Done() bool {
	switch j.Status {
	case "queued", "running":
		return false
	}
	return true
}

type Queued struct {
	ID string `json:"id"`
}

// ManifestDoc is GET /api/projects/{pid}/manifest. The manifest stays raw so that saving it back
// keeps every field this client does not know about.
type ManifestDoc struct {
	Manifest json.RawMessage `json:"manifest"`
	Revision int             `json:"revision"`
	ServerID string          `json:"server_id"`
}

type ImportPreview struct {
	DraftID  string           `json:"draft_id"`
	Name     string           `json:"name"`
	Services []ServicePreview `json:"services"`
	Blockers []string         `json:"blockers"`
	Notes    []string         `json:"notes"`
	Ready    bool             `json:"ready"`
}

type Backup struct {
	ID        string  `json:"id"`
	ProjectID string  `json:"project_id"`
	Service   string  `json:"service"`
	Engine    string  `json:"engine"`
	Size      int64   `json:"size"`
	Created   float64 `json:"created"`
}

type ExecResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}
