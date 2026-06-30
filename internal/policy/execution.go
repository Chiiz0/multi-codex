package policy

import "strings"

var DependencyPolicyFiles = []string{
	"go.mod",
	"go.sum",
	"package.json",
	"package-lock.json",
	"pnpm-lock.yaml",
	"yarn.lock",
	"bun.lock",
	"Cargo.toml",
	"Cargo.lock",
	"requirements.txt",
	"pyproject.toml",
	"poetry.lock",
	"uv.lock",
	"Pipfile",
	"Pipfile.lock",
	"Gemfile",
	"Gemfile.lock",
}

type CommandPolicyResult struct {
	Status          string   `json:"status"`
	AllowedCommands []string `json:"allowed_commands"`
	Violations      []string `json:"violations"`
	AllowlistActive bool     `json:"allowlist_active"`
}

type DependencyPolicyResult struct {
	Status                string   `json:"status"`
	AllowDependencyChange bool     `json:"allow_dependency_change"`
	ChangedFiles          []string `json:"changed_files"`
	Violations            []string `json:"violations"`
}

func CheckCommandPolicy(commands []string, allowlist []string, denylist []string) CommandPolicyResult {
	result := CommandPolicyResult{
		Status:          "passed",
		AllowedCommands: append([]string(nil), commands...),
		AllowlistActive: len(trimmedNonEmpty(allowlist)) > 0,
	}
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		if matched, pattern := matchesCommandPattern(command, denylist); matched {
			result.Violations = append(result.Violations, command+" matches denied command pattern "+pattern)
			continue
		}
		if result.AllowlistActive {
			if matched, _ := matchesCommandPattern(command, allowlist); !matched {
				result.Violations = append(result.Violations, command+" is outside worker command allowlist")
			}
		}
	}
	if len(result.Violations) > 0 {
		result.Status = "blocked"
	}
	return result
}

func CheckDependencyPolicy(changedFiles []string, allowDependencyChange bool) DependencyPolicyResult {
	result := DependencyPolicyResult{
		Status:                "passed",
		AllowDependencyChange: allowDependencyChange,
	}
	for _, file := range changedFiles {
		if !isDependencyPolicyFile(file) {
			continue
		}
		result.ChangedFiles = append(result.ChangedFiles, file)
		if !allowDependencyChange {
			result.Violations = append(result.Violations, file+" changes dependency or lockfile state while allow_dependency_change=false")
		}
	}
	if len(result.Violations) > 0 {
		result.Status = "blocked"
	}
	return result
}

func isDependencyPolicyFile(path string) bool {
	normalized := strings.Trim(strings.ReplaceAll(path, "\\", "/"), "/")
	if normalized == "" {
		return false
	}
	base := normalized
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	for _, name := range DependencyPolicyFiles {
		if strings.EqualFold(base, name) {
			return true
		}
	}
	return false
}

func matchesCommandPattern(command string, patterns []string) (bool, string) {
	normalizedCommand := normalizeCommand(command)
	for _, pattern := range trimmedNonEmpty(patterns) {
		normalizedPattern := normalizeCommand(pattern)
		if normalizedPattern == "" {
			continue
		}
		if normalizedCommand == normalizedPattern ||
			strings.HasPrefix(normalizedCommand, normalizedPattern+" ") ||
			strings.Contains(normalizedCommand, " "+normalizedPattern+" ") ||
			strings.HasSuffix(normalizedCommand, " "+normalizedPattern) {
			return true, pattern
		}
	}
	return false, ""
}

func normalizeCommand(command string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(command))), " ")
}

func trimmedNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
