// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package project

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/boggycreek/sandbox/pkg/config"
	"github.com/boggycreek/sandbox/pkg/forgejo"
	"github.com/boggycreek/sandbox/pkg/sonar"
)

// Project represents metadata and linkage for a project managed across Forgejo and SonarQube.
type Project struct {
	Name          string    `json:"name"`
	Org           string    `json:"org"`
	SonarKey      string    `json:"sonar_key"`
	Description   string    `json:"description,omitempty"`
	ForgejoURL    string    `json:"forgejo_url,omitempty"`
	GiteaURL      string    `json:"gitea_url,omitempty"` // Retained for backward compatibility
	SonarURL      string    `json:"sonarqube_url"`
	AnalysisToken string    `json:"analysis_token,omitempty"`
	ScanStatus    string    `json:"scan_status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// CheckItem represents an individual diagnostic check evaluation.
type CheckItem struct {
	Name         string `json:"name"`
	Status       string `json:"status"` // "ok", "healed", "warning", "error"
	Message      string `json:"message"`
	Healed       bool   `json:"healed,omitempty"`
	Unrepairable bool   `json:"unrepairable,omitempty"`
}

// DoctorReport contains diagnostic evaluations and auto-healing counts for a project.
type DoctorReport struct {
	ProjectName       string      `json:"project_name"`
	Org               string      `json:"org"`
	Checks            []CheckItem `json:"checks"`
	WarningCount      int         `json:"warning_count"`
	ErrorCount        int         `json:"error_count"`
	HealedCount       int         `json:"healed_count"`
	UnrepairableCount int         `json:"unrepairable_count"`
}

// Manager orchestrates project operations across Forgejo and SonarQube.
type Manager struct {
	paths         config.Paths
	forgejoClient *forgejo.Client
	sonarClient   *sonar.Client
	forgejoBase   string
	sonarBase     string
}

// NewManager constructs a project Manager using standard environment and paths.
func NewManager(paths config.Paths) *Manager {
	forgejoURL := os.Getenv("FORGEJO_URL")
	if forgejoURL == "" {
		forgejoURL = os.Getenv("FORGEJO_HOST_URL")
	}
	if forgejoURL == "" {
		forgejoURL = os.Getenv("GITEA_URL")
	}
	if forgejoURL == "" {
		forgejoURL = os.Getenv("GITEA_HOST_URL")
	}
	if forgejoURL == "" {
		forgejoURL = "http://127.0.0.1:3000"
	}
	forgejoURL = strings.TrimRight(forgejoURL, "/")

	adminPass := os.Getenv("ADMIN_BACKPLANE_PASSWORD")
	if adminPass == "" {
		adminPass = "admin_backplane_pass"
	}

	adminUser := os.Getenv("FORGEJO_ADMIN_USER")
	if adminUser == "" {
		adminUser = os.Getenv("GITEA_ADMIN_USER")
	}
	if adminUser == "" {
		adminUser = "forgejoadmin"
	}

	sonarURL := os.Getenv("SONAR_HOST_URL")
	if sonarURL == "" {
		sonarURL = os.Getenv("SONARQUBE_URL")
	}
	if sonarURL == "" {
		sonarURL = "http://127.0.0.1:9000"
	}
	sonarURL = strings.TrimRight(sonarURL, "/")

	sonarUser := os.Getenv("SONAR_ADMIN_USER")
	if sonarUser == "" {
		sonarUser = "admin"
	}
	sonarPass := config.GetSonarAdminPassword()

	forgejoClient := forgejo.NewClient(forgejo.ClientConfig{
		BaseURL:   forgejoURL,
		AdminUser: adminUser,
		AdminPass: adminPass,
		Timeout:   10 * time.Second,
	})

	sonarClient := sonar.NewClient(sonar.ClientConfig{
		BaseURL:   sonarURL,
		AdminUser: sonarUser,
		AdminPass: sonarPass,
		Timeout:   10 * time.Second,
	})

	return NewManagerWithClients(paths, forgejoClient, sonarClient, forgejoURL, sonarURL)
}

// NewManagerWithClients constructs a Manager with custom clients (used in tests and custom routing).
func NewManagerWithClients(paths config.Paths, forgejoClient *forgejo.Client, sonarClient *sonar.Client, forgejoBase, sonarBase string) *Manager {
	return &Manager{
		paths:         paths,
		forgejoClient: forgejoClient,
		sonarClient:   sonarClient,
		forgejoBase:   strings.TrimRight(forgejoBase, "/"),
		sonarBase:     strings.TrimRight(sonarBase, "/"),
	}
}

// ValidateName verifies that a project name or organization does not contain path traversal or invalid characters.
func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("invalid name %q: path separators and directory traversal are not allowed", name)
	}
	return nil
}

// metadataFilename returns the filename for project storage.
func metadataFilename(org, name string) string {
	cleanOrg := strings.ToLower(strings.TrimSpace(org))
	cleanName := strings.ToLower(strings.TrimSpace(name))
	return fmt.Sprintf("%s__%s.json", cleanOrg, cleanName)
}

// Create provisions a project in Gitea and SonarQube, saves metadata, and returns the project.
func (m *Manager) Create(ctx context.Context, name, org, sonarKey, description string) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("project name cannot be empty")
	}

	if strings.Contains(name, "/") {
		parts := strings.SplitN(name, "/", 2)
		if org == "" || org == config.GetDefaultProjectOrg() {
			org = parts[0]
		}
		name = parts[1]
	}

	org = strings.TrimSpace(org)
	if org == "" {
		org = config.GetDefaultProjectOrg()
	}

	if err := ValidateName(name); err != nil {
		return nil, fmt.Errorf("invalid project name: %w", err)
	}
	if err := ValidateName(org); err != nil {
		return nil, fmt.Errorf("invalid organization name: %w", err)
	}

	sonarKey = strings.TrimSpace(sonarKey)
	if sonarKey == "" {
		sonarKey = fmt.Sprintf("%s-%s", org, name)
	}

	// 1. Ensure Organization and Repository in Forgejo
	if err := m.forgejoClient.EnsureOrg(ctx, org); err != nil {
		return nil, fmt.Errorf("failed ensuring Forgejo organization %q: %w", org, err)
	}
	if err := m.forgejoClient.EnsureRepo(ctx, org, name, description, true); err != nil {
		return nil, fmt.Errorf("failed creating Forgejo repository %s/%s: %w", org, name, err)
	}

	// 2. Ensure Project in SonarQube
	if err := m.sonarClient.CreateProject(ctx, sonarKey, sonarKey); err != nil {
		return nil, fmt.Errorf("failed creating SonarQube project %q: %w", sonarKey, err)
	}

	// 3. Generate Analysis Token
	tokenName := fmt.Sprintf("%s-analysis-token", sonarKey)
	token, _ := m.sonarClient.GenerateProjectToken(ctx, sonarKey, tokenName)

	now := time.Now().UTC()
	forgejoURL := fmt.Sprintf("%s/%s/%s", m.forgejoBase, org, name)
	sonarURL := fmt.Sprintf("%s/dashboard?id=%s", m.sonarBase, sonarKey)

	// 4. Initial Scan Status
	scanStatus := m.resolveScanStatus(ctx, sonarKey)

	proj := &Project{
		Name:          name,
		Org:           org,
		SonarKey:      sonarKey,
		Description:   description,
		ForgejoURL:    forgejoURL,
		GiteaURL:      forgejoURL,
		SonarURL:      sonarURL,
		AnalysisToken: token,
		ScanStatus:    scanStatus,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := m.Save(proj); err != nil {
		return nil, err
	}

	return proj, nil
}

// Save persists the project metadata to disk atomically.
func (m *Manager) Save(p *Project) error {
	if err := m.paths.EnsureDirectories(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	filePath := filepath.Join(m.paths.ProjectsDir, metadataFilename(p.Org, p.Name))
	tmpFile, err := os.CreateTemp(m.paths.ProjectsDir, "proj-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, filePath)
}

// List returns all registered projects with refreshed live scan statuses.
func (m *Manager) List(ctx context.Context) ([]*Project, error) {
	if err := m.paths.EnsureDirectories(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(m.paths.ProjectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Project{}, nil
		}
		return nil, err
	}

	var projects []*Project
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		filePath := filepath.Join(m.paths.ProjectsDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		var p Project
		if err := json.Unmarshal(data, &p); err != nil {
			continue
		}

		if p.ForgejoURL == "" && p.GiteaURL != "" {
			p.ForgejoURL = p.GiteaURL
		}
		if p.GiteaURL == "" && p.ForgejoURL != "" {
			p.GiteaURL = p.ForgejoURL
		}

		p.ScanStatus = m.resolveScanStatus(ctx, p.SonarKey)
		projects = append(projects, &p)
	}

	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Org == projects[j].Org {
			return projects[i].Name < projects[j].Name
		}
		return projects[i].Org < projects[j].Org
	})

	return projects, nil
}

// Get retrieves a project by org and name.
func (m *Manager) Get(ctx context.Context, org, name string) (*Project, error) {
	if strings.Contains(name, "/") {
		parts := strings.SplitN(name, "/", 2)
		if org == "" || org == config.GetDefaultProjectOrg() {
			org = parts[0]
		}
		name = parts[1]
	}
	org = strings.TrimSpace(org)
	if org == "" {
		org = config.GetDefaultProjectOrg()
	}
	name = strings.TrimSpace(name)

	if err := ValidateName(name); err != nil {
		return nil, fmt.Errorf("invalid project name: %w", err)
	}
	if err := ValidateName(org); err != nil {
		return nil, fmt.Errorf("invalid organization name: %w", err)
	}

	filePath := filepath.Join(m.paths.ProjectsDir, metadataFilename(org, name))
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("project %s/%s not found: %w", org, name, err)
	}

	var p Project
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("failed decoding project %s/%s metadata: %w", org, name, err)
	}

	if p.ForgejoURL == "" && p.GiteaURL != "" {
		p.ForgejoURL = p.GiteaURL
	}
	if p.GiteaURL == "" && p.ForgejoURL != "" {
		p.GiteaURL = p.ForgejoURL
	}

	p.ScanStatus = m.resolveScanStatus(ctx, p.SonarKey)
	return &p, nil
}

// Link links an existing Forgejo/Gitea repository with a SonarQube project.
func (m *Manager) Link(ctx context.Context, repoRef, sonarProject string) (*Project, error) {
	repoRef = strings.TrimSpace(repoRef)
	if repoRef == "" {
		return nil, fmt.Errorf("repository cannot be empty")
	}

	var org, name string
	if parts := strings.Split(repoRef, "/"); len(parts) >= 2 {
		org = parts[0]
		name = parts[1]
	} else {
		org = config.GetDefaultProjectOrg()
		name = repoRef
	}

	sonarKey := strings.TrimSpace(sonarProject)
	if sonarKey == "" {
		sonarKey = fmt.Sprintf("%s-%s", org, name)
	}

	return m.Create(ctx, name, org, sonarKey, "")
}

// Delete removes project metadata and optionally remote assets across Forgejo and SonarQube.
func (m *Manager) Delete(ctx context.Context, org, name string, deleteRemote bool) error {
	p, err := m.Get(ctx, org, name)
	if err != nil {
		return err
	}

	if deleteRemote {
		_ = m.sonarClient.DeleteProject(ctx, p.SonarKey)
		_ = m.forgejoClient.DeleteRepo(ctx, p.Org, p.Name)
	}

	filePath := filepath.Join(m.paths.ProjectsDir, metadataFilename(p.Org, p.Name))
	return os.Remove(filePath)
}

// Doctor inspects a project for drift or missing resources and auto-heals defects.
func (m *Manager) Doctor(ctx context.Context, org, name string) (*DoctorReport, error) {
	p, err := m.Get(ctx, org, name)
	if err != nil {
		return nil, err
	}

	report := &DoctorReport{
		ProjectName: p.Name,
		Org:         p.Org,
	}

	// 1. Audit Forgejo Repository
	repoExists, repoErr := m.forgejoClient.RepoExists(ctx, p.Org, p.Name)
	repoURL := p.ForgejoURL
	if repoURL == "" {
		repoURL = p.GiteaURL
	}
	if repoErr != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Forgejo Repository",
			Status:  "warning",
			Message: fmt.Sprintf("Forgejo service unreachable (%v)", repoErr),
		})
		report.WarningCount++
	} else if repoExists {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Forgejo Repository",
			Status:  "ok",
			Message: fmt.Sprintf("Repository %s/%s exists and is accessible (%s)", p.Org, p.Name, repoURL),
		})
	} else {
		// Auto-heal Forgejo repository
		healErr := m.forgejoClient.EnsureRepo(ctx, p.Org, p.Name, p.Description, true)
		if healErr == nil {
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Forgejo Repository",
				Status:  "healed",
				Message: fmt.Sprintf("Auto-healed missing Forgejo repository %s/%s", p.Org, p.Name),
				Healed:  true,
			})
			report.HealedCount++
		} else {
			report.Checks = append(report.Checks, CheckItem{
				Name:         "Forgejo Repository",
				Status:       "error",
				Message:      fmt.Sprintf("Forgejo repository %s/%s missing and repair failed: %v", p.Org, p.Name, healErr),
				Unrepairable: true,
			})
			report.ErrorCount++
			report.UnrepairableCount++
		}
	}

	// 2. Audit SonarQube Project
	sonarExists, sonarErr := m.sonarClient.ProjectExists(ctx, p.SonarKey)
	if sonarErr != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "SonarQube Project",
			Status:  "warning",
			Message: fmt.Sprintf("SonarQube service unreachable (%v)", sonarErr),
		})
		report.WarningCount++
	} else if sonarExists {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "SonarQube Project",
			Status:  "ok",
			Message: fmt.Sprintf("Project %q exists in SonarQube (%s)", p.SonarKey, p.SonarURL),
		})
	} else {
		// Auto-heal SonarQube project
		healErr := m.sonarClient.CreateProject(ctx, p.SonarKey, p.SonarKey)
		if healErr == nil {
			report.Checks = append(report.Checks, CheckItem{
				Name:    "SonarQube Project",
				Status:  "healed",
				Message: fmt.Sprintf("Auto-healed missing SonarQube project %q", p.SonarKey),
				Healed:  true,
			})
			report.HealedCount++
		} else {
			report.Checks = append(report.Checks, CheckItem{
				Name:         "SonarQube Project",
				Status:       "error",
				Message:      fmt.Sprintf("SonarQube project %q missing and repair failed: %v", p.SonarKey, healErr),
				Unrepairable: true,
			})
			report.ErrorCount++
			report.UnrepairableCount++
		}
	}

	// 3. Audit Analysis Token
	if p.AnalysisToken != "" {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Analysis Token",
			Status:  "ok",
			Message: "Project analysis token is configured",
		})
	} else {
		// Auto-heal token
		tokenName := fmt.Sprintf("%s-analysis-token", p.SonarKey)
		token, tokenErr := m.sonarClient.GenerateProjectToken(ctx, p.SonarKey, tokenName)
		if tokenErr == nil && token != "" {
			p.AnalysisToken = token
			_ = m.Save(p)
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Analysis Token",
				Status:  "healed",
				Message: "Auto-generated missing SonarQube project analysis token",
				Healed:  true,
			})
			report.HealedCount++
		} else {
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Analysis Token",
				Status:  "warning",
				Message: fmt.Sprintf("Analysis token missing and generation failed: %v", tokenErr),
			})
			report.WarningCount++
		}
	}

	// 4. Quality Gate Reachability
	qg, qgErr := m.sonarClient.GetProjectQualityGate(ctx, p.SonarKey)
	if qgErr == nil && qg != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Quality Gate",
			Status:  "ok",
			Message: fmt.Sprintf("Quality Gate status is %s", p.ScanStatus),
		})
	} else {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Quality Gate",
			Status:  "warning",
			Message: fmt.Sprintf("Could not retrieve Quality Gate status (%v)", qgErr),
		})
		report.WarningCount++
	}

	return report, nil
}

// DoctorAll diagnoses all registered projects.
func (m *Manager) DoctorAll(ctx context.Context) ([]*DoctorReport, error) {
	projects, err := m.List(ctx)
	if err != nil {
		return nil, err
	}

	var reports []*DoctorReport
	for _, p := range projects {
		rep, err := m.Doctor(ctx, p.Org, p.Name)
		if err == nil && rep != nil {
			reports = append(reports, rep)
		}
	}
	return reports, nil
}

// PostCommitStatus evaluates Quality Gate for sonarKey and posts the commit status to Forgejo.
func (m *Manager) PostCommitStatus(ctx context.Context, org, repo, sha, sonarKey string) (*forgejo.CommitStatus, error) {
	org = strings.TrimSpace(org)
	if org == "" {
		org = config.GetDefaultProjectOrg()
	}
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return nil, fmt.Errorf("repository name cannot be empty")
	}
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return nil, fmt.Errorf("commit sha cannot be empty")
	}

	sonarKey = strings.TrimSpace(sonarKey)
	if sonarKey == "" {
		sonarKey = fmt.Sprintf("%s-%s", org, repo)
	}

	qg, _ := m.sonarClient.GetProjectQualityGate(ctx, sonarKey)
	return sonar.DispatchQualityGateStatus(ctx, m.forgejoClient, org, repo, sha, m.sonarBase, sonarKey, qg)
}

// resolveScanStatus converts SonarQube Quality Gate status to user-facing strings.
func (m *Manager) resolveScanStatus(ctx context.Context, sonarKey string) string {
	qg, err := m.sonarClient.GetProjectQualityGate(ctx, sonarKey)
	if err != nil || qg == nil {
		return "UNANALYZED"
	}
	switch strings.ToUpper(qg.Status) {
	case "NONE":
		return "UNANALYZED"
	case "OK":
		return "PASSED"
	case "ERROR":
		return "FAILED"
	case "WARN":
		return "WARN"
	default:
		return strings.ToUpper(qg.Status)
	}
}

// FormatList formats a list of projects according to user-requested terminal width layout:
// ● <org> / <project>
//
//	Gitea:       <url>
//	SonarQube:   <url>
//	Scan Status: <status>
func FormatList(projects []*Project) string {
	if len(projects) == 0 {
		return "No projects registered. Create one with 'fleet project create <name>'."
	}

	var sb strings.Builder
	for i, p := range projects {
		if i > 0 {
			sb.WriteString("\n")
		}
		repoURL := p.ForgejoURL
		if repoURL == "" {
			repoURL = p.GiteaURL
		}
		sb.WriteString(fmt.Sprintf("● %s / %s\n", p.Org, p.Name))
		sb.WriteString(fmt.Sprintf("    Forgejo:     %s\n", repoURL))
		sb.WriteString(fmt.Sprintf("    SonarQube:   %s\n", p.SonarURL))
		sb.WriteString(fmt.Sprintf("    Scan Status: %s\n", p.ScanStatus))
	}
	return sb.String()
}

// FormatDoctorReport renders the diagnostic checks into human-readable terminal output.
func FormatDoctorReport(report *DoctorReport) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Project Diagnostics: %s / %s\n", report.Org, report.ProjectName))
	sb.WriteString(strings.Repeat("-", 60) + "\n")

	for _, check := range report.Checks {
		tag := "[ OK ]"
		switch check.Status {
		case "healed":
			tag = "[HEAL]"
		case "warning":
			tag = "[WARN]"
		case "error":
			tag = "[FAIL]"
		}
		sb.WriteString(fmt.Sprintf("%-7s %-20s %s\n", tag, check.Name, check.Message))
	}
	sb.WriteString(strings.Repeat("-", 60) + "\n")
	sb.WriteString(fmt.Sprintf("Summary: %d healed, %d warning(s), %d error(s)\n", report.HealedCount, report.WarningCount, report.ErrorCount))
	return sb.String()
}
