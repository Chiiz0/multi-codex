package policy

import (
	"regexp"
	"strings"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

func CheckScope(changedFiles []string, allowedPaths []string, forbiddenPaths []string) domain.ScopeCheckResult {
	violations := []string{}
	if changedFiles == nil {
		changedFiles = []string{}
	}
	for _, file := range changedFiles {
		if !matchesAny(file, allowedPaths) {
			violations = append(violations, file+" is outside allowed_paths")
			continue
		}
		if matchesAny(file, forbiddenPaths) {
			violations = append(violations, file+" matches forbidden_paths")
		}
	}

	status := "passed"
	if len(violations) > 0 {
		status = "blocked"
	}

	return domain.ScopeCheckResult{
		Status:       status,
		ChangedFiles: changedFiles,
		Violations:   violations,
	}
}

func matchesAny(path string, patterns []string) bool {
	for _, pattern := range patterns {
		if globMatch(pattern, path) {
			return true
		}
	}
	return false
}

func globMatch(pattern string, path string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == path {
		return true
	}

	regex := regexp.QuoteMeta(pattern)
	regex = strings.ReplaceAll(regex, `\*\*`, `.*`)
	regex = strings.ReplaceAll(regex, `\*`, `[^/]*`)
	regex = "^" + regex + "$"
	return regexp.MustCompile(regex).MatchString(path)
}
