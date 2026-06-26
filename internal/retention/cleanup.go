package retention

import (
	"os"
	"path/filepath"
	"time"
)

type CleanupOptions struct {
	Roots  []string
	MaxAge time.Duration
	DryRun bool
	Now    time.Time
}

type CleanupResult struct {
	Roots          []string `json:"roots"`
	MaxAgeSeconds  int64    `json:"max_age_seconds"`
	DryRun         bool     `json:"dry_run"`
	Scanned        int64    `json:"scanned"`
	Deleted        int64    `json:"deleted"`
	BytesReclaimed int64    `json:"bytes_reclaimed"`
	Errors         []string `json:"errors"`
}

func Cleanup(opts CleanupOptions) CleanupResult {
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	result := CleanupResult{
		Roots:         opts.Roots,
		MaxAgeSeconds: int64(opts.MaxAge.Seconds()),
		DryRun:        opts.DryRun,
		Errors:        []string{},
	}
	if opts.MaxAge <= 0 {
		result.Errors = append(result.Errors, "max age must be positive")
		return result
	}
	for _, root := range opts.Roots {
		cleanupRoot(root, opts, &result)
	}
	return result
}

func cleanupRoot(root string, opts CleanupOptions, result *CleanupResult) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		result.Errors = append(result.Errors, err.Error())
		return
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
			continue
		}
		result.Scanned++
		if opts.Now.Sub(info.ModTime()) < opts.MaxAge {
			continue
		}
		size := sizeOf(path)
		if !opts.DryRun {
			if err := os.RemoveAll(path); err != nil {
				result.Errors = append(result.Errors, err.Error())
				continue
			}
		}
		result.Deleted++
		result.BytesReclaimed += size
	}
}

func sizeOf(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := entry.Info(); err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
