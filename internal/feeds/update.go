// Package feeds refreshes the baked vulnerability databases (grype DB and
// OSV offline databases) when they are stale.  Every operation fails open:
// a network or tool error logs a warning and leaves the baked data in place.
package feeds

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/traust-security/traust-cli/internal/bootstrap"
)

// LogFn matches the logging callback used throughout traust-cli.
type LogFn func(format string, a ...any)

// Result reports what happened for each feed.
type Result struct {
	GrypeStatus string
	OsvStatus   string
}

// Update refreshes stale vulnerability databases, failing open on any error.
// maxAge controls the grype staleness threshold; OSV databases are refreshed
// unconditionally (they carry no built-in timestamp metadata).
func Update(ctx context.Context, state *bootstrap.State, maxAge time.Duration, log LogFn) Result {
	binDir := state.CliBinDir()
	result := Result{GrypeStatus: "unchanged", OsvStatus: "unchanged"}

	result.GrypeStatus = updateGrype(ctx, binDir, maxAge, log)
	result.OsvStatus = updateOSV(ctx, binDir, state, log)

	return result
}

func updateGrype(ctx context.Context, binDir string, maxAge time.Duration, log LogFn) string {
	grype := filepath.Join(binDir, "grype")
	if _, err := os.Stat(grype); err != nil {
		log("grype not installed; skipping")
		return "not installed"
	}

	// Check staleness.  grype db check exits 0 when current, non-zero when stale.
	check := exec.CommandContext(ctx, grype, "db", "check", "-q")
	check.Env = append(os.Environ(), fmt.Sprintf("GRYPE_DB_MAX_ALLOWED_BUILT_AGE=%s", maxAge))
	if err := check.Run(); err == nil {
		log("grype DB is current")
		return "current"
	}

	log("grype DB is stale; updating...")
	update := exec.CommandContext(ctx, grype, "db", "update", "-q")
	update.Env = os.Environ()
	if err := update.Run(); err != nil {
		log("grype DB update failed (%v); using baked DB", err)
		return "update failed; using baked DB"
	}
	log("grype DB updated")
	return "updated"
}

func updateOSV(ctx context.Context, binDir string, state *bootstrap.State, log LogFn) string {
	scanner := filepath.Join(binDir, "osv-scanner")
	if _, err := os.Stat(scanner); err != nil {
		log("osv-scanner not installed; skipping")
		return "not installed"
	}

	dbDir := os.Getenv("OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY")
	if dbDir == "" {
		dbDir = filepath.Join(state.Workspace, "osv-db")
	}

	ecosystems := []string{"Go", "Maven", "NuGet", "Packagist", "PyPI", "RubyGems", "crates.io", "npm"}

	// Snapshot mtime of each DB before the download so we can distinguish
	// "actually refreshed" from "pre-existing baked DB still there".
	before := make(map[string]time.Time, len(ecosystems))
	for _, eco := range ecosystems {
		db := filepath.Join(dbDir, "osv-scalibr", eco, "all.zip")
		if info, err := os.Stat(db); err == nil {
			before[eco] = info.ModTime()
		}
	}

	log("refreshing osv offline databases...")

	// Create a temporary stub with minimal lockfiles to trigger downloads
	// for each ecosystem.
	stub, err := os.MkdirTemp("", "feeds-osv-stub-")
	if err != nil {
		log("failed to create temp dir (%v); using baked DBs", err)
		return "update failed; using baked DBs"
	}
	defer os.RemoveAll(stub)

	// Real package names with known advisories (same as the Containerfile's
	// build-time stub): fake names fail deps.dev resolution, which makes
	// osv-scanner exit 127 even though the DB download succeeded.
	stubs := map[string]string{
		"requirements.txt":   "requests==2.20.0\n",
		"go.mod":             "module example.com/m\ngo 1.21\nrequire github.com/gin-gonic/gin v1.6.0\n",
		"package-lock.json":  `{"name":"s","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"s","version":"1.0.0","dependencies":{"lodash":"4.17.20"}},"node_modules/lodash":{"version":"4.17.20","resolved":"https://registry.npmjs.org/lodash/-/lodash-4.17.20.tgz","integrity":"sha512-x"}}}` + "\n",
		"pom.xml":            "<project><modelVersion>4.0.0</modelVersion><groupId>a</groupId><artifactId>b</artifactId><version>1</version><dependencies><dependency><groupId>org.apache.logging.log4j</groupId><artifactId>log4j-core</artifactId><version>2.14.0</version></dependency></dependencies></project>\n",
		"Gemfile.lock":       "GEM\n  remote: https://rubygems.org/\n  specs:\n    rack (2.0.5)\n\nPLATFORMS\n  ruby\n\nDEPENDENCIES\n  rack\n",
		"Cargo.lock":         "[[package]]\nname = \"time\"\nversion = \"0.1.42\"\n",
		"composer.lock":      `{"packages":[{"name":"guzzlehttp/guzzle","version":"6.5.0"}],"packages-dev":[]}` + "\n",
		"packages.lock.json": `{"version":1,"dependencies":{".NETCoreApp,Version=v6.0":{"Newtonsoft.Json":{"type":"Direct","requested":"12.0.0","resolved":"12.0.0","contentHash":"x"}}}}` + "\n",
	}
	for name, content := range stubs {
		if err := os.WriteFile(filepath.Join(stub, name), []byte(content), 0644); err != nil {
			log("failed to write stub %s (%v); using baked DBs", name, err)
			return "update failed; using baked DBs"
		}
	}

	// osv-scanner exits non-zero when it finds vulnerabilities (expected)
	// and also on real errors.  We cannot distinguish the two from the exit
	// code alone, so we check whether DBs were actually written/updated.
	cmd := exec.CommandContext(ctx, scanner, "scan", "source",
		"--offline-vulnerabilities", "--download-offline-databases", stub)
	cmd.Env = append(os.Environ(), fmt.Sprintf("OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY=%s", dbDir))
	scanErr := cmd.Run()

	// Compare each DB's mtime against the pre-download snapshot.
	refreshed := 0
	stale := 0
	missing := 0
	for _, eco := range ecosystems {
		db := filepath.Join(dbDir, "osv-scalibr", eco, "all.zip")
		info, err := os.Stat(db)
		if err != nil || info.Size() == 0 {
			missing++
			continue
		}
		prevTime, existed := before[eco]
		if !existed || info.ModTime().After(prevTime) {
			refreshed++
		} else {
			stale++
		}
	}

	total := refreshed + stale + missing
	if refreshed > 0 {
		log("osv offline databases: %d refreshed, %d unchanged, %d missing (of %d)",
			refreshed, stale, missing, total)
		return fmt.Sprintf("refreshed %d/%d", refreshed, total)
	}
	if stale > 0 {
		// Download produced no new files but baked DBs exist.
		if scanErr != nil {
			log("osv download failed (%v); %d baked DBs available", scanErr, stale)
		} else {
			log("osv databases unchanged; %d baked DBs available", stale)
		}
		return fmt.Sprintf("unchanged; %d baked DBs available", stale)
	}
	log("no osv offline databases available")
	return "update failed; no databases available"
}
