package runbook

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Runbook represents a runbook definition
type Runbook struct {
	ID          string    `yaml:"id"`
	Name        string    `yaml:"name"`
	Description string    `yaml:"description,omitempty"`
	Category    string    `yaml:"category,omitempty"`
	Severity    string    `yaml:"severity,omitempty"` // P1, P2, P3, P4
	Tags        []string  `yaml:"tags,omitempty"`
	Author      string    `yaml:"author,omitempty"`
	CreatedAt   time.Time `yaml:"created_at"`
	UpdatedAt   time.Time `yaml:"updated_at"`
	Steps       []Step    `yaml:"steps"`
	OnFailure   string    `yaml:"on_failure,omitempty"` // escalate, rollback, continue
}

// Step represents a single step in a runbook
type Step struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Command     string `yaml:"command,omitempty"`
	Check       string `yaml:"check,omitempty"`    // command to verify step success
	Rollback    string `yaml:"rollback,omitempty"` // command to undo this step
	Manual      bool   `yaml:"manual,omitempty"`   // requires manual confirmation
	Timeout     string `yaml:"timeout,omitempty"`  // e.g. "30s", "5m"
	Notes       string `yaml:"notes,omitempty"`
}

// RunLog represents a runbook execution log
type RunLog struct {
	RunbookID   string        `yaml:"runbook_id"`
	RunbookName string        `yaml:"runbook_name"`
	StartedAt   time.Time     `yaml:"started_at"`
	CompletedAt time.Time     `yaml:"completed_at,omitempty"`
	Status      string        `yaml:"status"` // running, completed, failed, aborted
	Operator    string        `yaml:"operator,omitempty"`
	StepResults []StepResult  `yaml:"step_results"`
}

// StepResult records the outcome of executing a step
type StepResult struct {
	StepName  string    `yaml:"step_name"`
	Status    string    `yaml:"status"` // pending, passed, failed, skipped
	Output    string    `yaml:"output,omitempty"`
	StartedAt time.Time `yaml:"started_at"`
	Duration  string    `yaml:"duration,omitempty"`
	Error     string    `yaml:"error,omitempty"`
}

// Store manages runbook persistence
type Store struct {
	dir string
}

// NewStore creates a new runbook store
func NewStore() *Store {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".argus", "runbooks")
	return &Store{dir: dir}
}

// Dir returns the storage directory
func (s *Store) Dir() string {
	return s.dir
}

// EnsureDir creates the storage directory if needed
func (s *Store) EnsureDir() error {
	return os.MkdirAll(s.dir, 0755)
}

// Save writes a runbook to disk
func (s *Store) Save(rb *Runbook) error {
	if err := s.EnsureDir(); err != nil {
		return err
	}
	if rb.ID == "" {
		rb.ID = generateID(rb.Name)
	}
	rb.UpdatedAt = time.Now()

	data, err := yaml.Marshal(rb)
	if err != nil {
		return fmt.Errorf("marshaling runbook: %w", err)
	}

	path := filepath.Join(s.dir, rb.ID+".yaml")
	return os.WriteFile(path, data, 0644)
}

// Load reads a runbook by ID (supports partial ID matching)
func (s *Store) Load(idOrPartial string) (*Runbook, error) {
	// Try exact match first
	path := filepath.Join(s.dir, idOrPartial+".yaml")
	if data, err := os.ReadFile(path); err == nil {
		var rb Runbook
		if err := yaml.Unmarshal(data, &rb); err != nil {
			return nil, fmt.Errorf("parsing runbook: %w", err)
		}
		return &rb, nil
	}

	// Try partial ID match
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("runbooks directory not found. Run: argus runbook init")
	}

	var matches []string
	lower := strings.ToLower(idOrPartial)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			name := strings.TrimSuffix(e.Name(), ".yaml")
			if strings.HasPrefix(strings.ToLower(name), lower) {
				matches = append(matches, name)
			}
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("runbook %q not found", idOrPartial)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous ID %q matches: %s", idOrPartial, strings.Join(matches, ", "))
	}

	return s.Load(matches[0])
}

// List returns all runbooks
func (s *Store) List() ([]*Runbook, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var runbooks []*Runbook
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var rb Runbook
		if err := yaml.Unmarshal(data, &rb); err != nil {
			continue
		}
		runbooks = append(runbooks, &rb)
	}

	sort.Slice(runbooks, func(i, j int) bool {
		return runbooks[i].Name < runbooks[j].Name
	})

	return runbooks, nil
}

// Delete removes a runbook by ID
func (s *Store) Delete(id string) error {
	rb, err := s.Load(id)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, rb.ID+".yaml")
	return os.Remove(path)
}

// Search finds runbooks matching a query (searches name, description, tags, category)
func (s *Store) Search(query string) ([]*Runbook, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}

	lower := strings.ToLower(query)
	var results []*Runbook
	for _, rb := range all {
		if matchesQuery(rb, lower) {
			results = append(results, rb)
		}
	}
	return results, nil
}

func matchesQuery(rb *Runbook, query string) bool {
	if strings.Contains(strings.ToLower(rb.Name), query) {
		return true
	}
	if strings.Contains(strings.ToLower(rb.Description), query) {
		return true
	}
	if strings.Contains(strings.ToLower(rb.Category), query) {
		return true
	}
	for _, tag := range rb.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}

func generateID(name string) string {
	slug := strings.ToLower(name)
	slug = strings.ReplaceAll(slug, " ", "-")
	// Remove non-alphanumeric except hyphens
	var clean []byte
	for _, b := range []byte(slug) {
		if (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '-' {
			clean = append(clean, b)
		}
	}
	slug = string(clean)
	if len(slug) > 40 {
		slug = slug[:40]
	}
	// Add short hash for uniqueness
	h := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", name, time.Now().UnixNano())))
	return fmt.Sprintf("%s-%x", slug, h[:3])
}

// InitSamples creates sample runbooks
func InitSamples(s *Store) error {
	samples := []*Runbook{
		{
			Name:        "Pod CrashLoopBackOff",
			Description: "Diagnose and resolve a pod stuck in CrashLoopBackOff state",
			Category:    "kubernetes",
			Severity:    "P2",
			Tags:        []string{"kubernetes", "pods", "troubleshooting"},
			Author:      "argus",
			CreatedAt:   time.Now(),
			OnFailure:   "escalate",
			Steps: []Step{
				{Name: "Identify crashing pods", Command: "kubectl get pods -A --field-selector=status.phase!=Running | grep CrashLoop", Notes: "Note the namespace, pod name, and restart count"},
				{Name: "Check pod events", Command: "kubectl describe pod <POD> -n <NS> | tail -20", Notes: "Look for OOMKilled, ImagePullBackOff, or permission errors"},
				{Name: "Check container logs", Command: "kubectl logs <POD> -n <NS> --previous --tail=50", Notes: "The --previous flag shows logs from the last crashed container"},
				{Name: "Check resource limits", Command: "kubectl get pod <POD> -n <NS> -o jsonpath='{.spec.containers[*].resources}'", Notes: "If OOMKilled, increase memory limits"},
				{Name: "Check node resources", Command: "kubectl top node && kubectl top pod -n <NS>", Notes: "Verify node has enough resources"},
				{Name: "Apply fix", Manual: true, Notes: "Based on findings, apply the appropriate fix (increase limits, fix image, fix config)"},
				{Name: "Verify recovery", Command: "kubectl get pod <POD> -n <NS> -w", Check: "kubectl get pod <POD> -n <NS> -o jsonpath='{.status.phase}' | grep Running", Timeout: "5m"},
			},
		},
		{
			Name:        "High Error Rate Response",
			Description: "Investigate and mitigate a spike in HTTP 5xx errors",
			Category:    "incident-response",
			Severity:    "P1",
			Tags:        []string{"http", "errors", "incident"},
			Author:      "argus",
			CreatedAt:   time.Now(),
			OnFailure:   "escalate",
			Steps: []Step{
				{Name: "Assess scope", Command: "argus top --sort errors --duration 15", Notes: "Identify which services are affected"},
				{Name: "Check recent deployments", Command: "kubectl rollout history deployment -n <NS>", Notes: "Correlate error spike with recent deploys"},
				{Name: "Check error logs", Command: "argus logs <SERVICE> -s ERROR -d 15", Notes: "Look for common error patterns"},
				{Name: "Check dependencies", Command: "argus status", Notes: "Verify all upstream/downstream services are healthy"},
				{Name: "Rollback if deploy-related", Command: "kubectl rollout undo deployment/<DEPLOY> -n <NS>", Manual: true, Rollback: "kubectl rollout undo deployment/<DEPLOY> -n <NS>", Notes: "Only if error correlates with recent deploy"},
				{Name: "Scale up if load-related", Command: "kubectl scale deployment/<DEPLOY> -n <NS> --replicas=<N>", Manual: true, Notes: "If caused by traffic spike"},
				{Name: "Verify error rate dropping", Command: "argus diff --duration 5", Check: "argus alert check --format json", Timeout: "10m"},
			},
		},
		{
			Name:        "Certificate Renewal",
			Description: "Check and renew TLS certificates managed by cert-manager",
			Category:    "maintenance",
			Severity:    "P3",
			Tags:        []string{"tls", "certificates", "cert-manager"},
			Author:      "argus",
			CreatedAt:   time.Now(),
			OnFailure:   "continue",
			Steps: []Step{
				{Name: "List certificates", Command: "kubectl get certificates -A", Notes: "Check READY status and expiry dates"},
				{Name: "Check expiring certs", Command: "kubectl get certificates -A -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}: {.status.notAfter}{\"\\n\"}{end}'"},
				{Name: "Check cert-manager logs", Command: "kubectl logs -n cert-manager deployment/cert-manager --tail=30"},
				{Name: "Force renewal if needed", Command: "kubectl delete secret <SECRET> -n <NS>", Manual: true, Notes: "cert-manager will automatically reissue"},
				{Name: "Verify new certificate", Command: "kubectl get certificate <CERT> -n <NS> -o yaml | grep -A5 status", Timeout: "5m"},
			},
		},
		{
			Name:        "Database Connection Pool Exhaustion",
			Description: "Diagnose and fix connection pool saturation",
			Category:    "database",
			Severity:    "P1",
			Tags:        []string{"database", "postgres", "connections"},
			Author:      "argus",
			CreatedAt:   time.Now(),
			OnFailure:   "escalate",
			Steps: []Step{
				{Name: "Check active connections", Command: "kubectl exec -n <NS> <DB_POD> -- psql -U postgres -c \"SELECT count(*), state FROM pg_stat_activity GROUP BY state;\""},
				{Name: "Find long-running queries", Command: "kubectl exec -n <NS> <DB_POD> -- psql -U postgres -c \"SELECT pid, now()-query_start AS duration, query FROM pg_stat_activity WHERE state='active' ORDER BY duration DESC LIMIT 10;\""},
				{Name: "Check max connections", Command: "kubectl exec -n <NS> <DB_POD> -- psql -U postgres -c \"SHOW max_connections;\""},
				{Name: "Kill idle connections", Command: "kubectl exec -n <NS> <DB_POD> -- psql -U postgres -c \"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE state='idle' AND query_start < now() - interval '10 minutes';\"", Manual: true},
				{Name: "Restart affected services", Command: "kubectl rollout restart deployment/<APP> -n <NS>", Manual: true, Notes: "Only if app is stuck with stale connections"},
				{Name: "Verify pool recovery", Check: "kubectl exec -n <NS> <DB_POD> -- psql -U postgres -c \"SELECT count(*) FROM pg_stat_activity;\"", Timeout: "2m"},
			},
		},
		{
			Name:        "Node Not Ready",
			Description: "Investigate and recover a Kubernetes node in NotReady state",
			Category:    "kubernetes",
			Severity:    "P2",
			Tags:        []string{"kubernetes", "nodes", "infrastructure"},
			Author:      "argus",
			CreatedAt:   time.Now(),
			OnFailure:   "escalate",
			Steps: []Step{
				{Name: "Identify affected nodes", Command: "kubectl get nodes | grep -v Ready"},
				{Name: "Check node conditions", Command: "kubectl describe node <NODE> | grep -A5 Conditions"},
				{Name: "Check kubelet status", Command: "ssh <NODE> 'systemctl status kubelet'", Notes: "May need direct access or cloud console"},
				{Name: "Check disk pressure", Command: "kubectl describe node <NODE> | grep -E 'DiskPressure|MemoryPressure'"},
				{Name: "Drain if unrecoverable", Command: "kubectl drain <NODE> --ignore-daemonsets --delete-emptydir-data", Manual: true},
				{Name: "Restart kubelet", Command: "ssh <NODE> 'sudo systemctl restart kubelet'", Manual: true},
				{Name: "Verify node recovery", Command: "kubectl get node <NODE>", Check: "kubectl get node <NODE> -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}' | grep True", Timeout: "5m"},
			},
		},
	}

	for _, rb := range samples {
		if err := s.Save(rb); err != nil {
			return fmt.Errorf("saving sample %q: %w", rb.Name, err)
		}
	}

	return nil
}
