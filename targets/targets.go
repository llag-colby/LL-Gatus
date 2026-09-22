// Package targets holds runtime target overrides, per endpoint key.
//
// config.yaml is baked into the image at build time (see Dockerfile) and
// update.sh does a `git reset --hard` + `git clean -fd`, so re-pointing a check
// by rewriting config.yaml at runtime would appear to work, survive a restart,
// and then be silently destroyed by the next deploy. That is the worst kind of
// bug: it tests fine and disappears weeks later. So an override lives here
// instead, as JSON in /data, which is the only mounted volume.
//
// An override is consulted by the watchdog on every check rather than being
// applied to the config struct at startup. That has three consequences worth
// knowing:
//
//   - An edit takes effect on the endpoint's next tick. No restart, no
//     re-staging of 45 goroutines, no blip on the wallboards.
//   - Endpoint.URL keeps exactly the value config.yaml gave it, so "reset to
//     config" is always available and never has to guess.
//   - A config reload rebuilds the whole *config.Config from disk, and
//     overrides survive it automatically because nothing was ever written into
//     that struct.
//
// This package must not import api, config or watchdog. watchdog imports it,
// api imports watchdog, and api imports this — any other direction is a cycle.
// It mirrors the monitoring package, which solves the same problem for pause
// state.
package targets

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/TwiN/logr"
)

var (
	mu        sync.RWMutex
	overrides = map[string]string{}
	loaded    bool
)

const (
	overridesPath = "/data/endpoint_targets.json"
	editTokenPath = "/data/edit_token"
	// Generous, but bounded: a target is a host or a URL, not a document.
	maxTargetLength = 512
	// Refuse to load an absurd file rather than let a corrupt or hostile one
	// balloon memory. Far above the 45 endpoints this deployment actually has.
	maxOverrides = 1000
)

var (
	ErrEmptyTarget    = errors.New("target cannot be empty")
	ErrTargetTooLong  = fmt.Errorf("target cannot exceed %d characters", maxTargetLength)
	ErrTargetSpaces   = errors.New("target cannot contain whitespace")
	ErrTargetNoHost   = errors.New("target must include a host")
	ErrTooManyTargets = fmt.Errorf("cannot store more than %d overrides", maxOverrides)
)

type targetsFile struct {
	Version int               `json:"version"`
	Targets map[string]string `json:"targets"`
}

// ensureLoaded reads the persisted overrides once. A missing file means "no
// overrides", which is the correct default. A CORRUPT file is different: it
// silently sends every check back to its config target, so the bad file is kept
// alongside as .corrupt rather than being overwritten on the next write.
func ensureLoaded() {
	mu.Lock()
	defer mu.Unlock()
	if loaded {
		return
	}
	loaded = true
	b, err := os.ReadFile(overridesPath)
	if err != nil {
		return
	}
	var f targetsFile
	if err := json.Unmarshal(b, &f); err != nil {
		logr.Errorf("[targets.ensureLoaded] %s is not valid JSON, ignoring it and keeping a copy at %s.corrupt: %s", overridesPath, overridesPath, err.Error())
		if writeErr := os.WriteFile(overridesPath+".corrupt", b, 0o644); writeErr != nil {
			logr.Errorf("[targets.ensureLoaded] could not preserve the corrupt file: %s", writeErr.Error())
		}
		return
	}
	for key, target := range f.Targets {
		if len(overrides) >= maxOverrides {
			logr.Errorf("[targets.ensureLoaded] %s holds more than %d overrides; ignoring the rest", overridesPath, maxOverrides)
			break
		}
		if key == "" || strings.TrimSpace(target) == "" {
			continue
		}
		overrides[key] = strings.TrimSpace(target)
	}
}

// persist writes the overrides atomically. Caller holds mu.
//
// Temp file plus rename, not a plain write: a truncated file here reads as "no
// overrides", which would quietly send every edited check back to a stale
// address. Nobody notices that until the day it matters.
func persist() error {
	f := targetsFile{Version: 1, Targets: make(map[string]string, len(overrides))}
	for key, target := range overrides {
		f.Targets[key] = target
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(overridesPath), 0o755); err != nil {
		return err
	}
	tmp := overridesPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, overridesPath)
}

// SchemeOf returns the lowercased scheme of a target, or "" if it has none.
// DNS endpoints legitimately carry a bare address, so "" is a real scheme here
// and not an error.
func SchemeOf(raw string) string {
	if i := strings.Index(raw, "://"); i > 0 {
		return strings.ToLower(raw[:i])
	}
	return ""
}

// Validate reports whether replacement is an acceptable stand-in for the
// endpoint's configured target.
//
// The rule that matters is the scheme lock. This container runs with NET_RAW
// (see docker-compose.yml) and an override is settable over HTTP, so letting an
// icmp:// check become http://some-internal-admin/delete would turn a
// monitoring tool into a request forwarder. Re-pointing a probe at a different
// host is the feature; changing what KIND of probe it is, is not.
func Validate(configured, replacement string) error {
	replacement = strings.TrimSpace(replacement)
	if replacement == "" {
		return ErrEmptyTarget
	}
	if len(replacement) > maxTargetLength {
		return ErrTargetTooLong
	}
	if strings.ContainsAny(replacement, " \t\r\n\v\f") {
		return ErrTargetSpaces
	}
	want, got := SchemeOf(configured), SchemeOf(replacement)
	if want != got {
		if want == "" {
			return fmt.Errorf("this endpoint's target has no scheme, so %q cannot be used; drop the %q:// prefix", replacement, got)
		}
		return fmt.Errorf("target must stay a %s:// address; %q is not one", want, replacement)
	}
	host := replacement
	if want != "" {
		host = replacement[len(want)+3:]
	}
	if strings.TrimSpace(host) == "" || strings.HasPrefix(host, "/") {
		return ErrTargetNoHost
	}
	return nil
}

// For returns the override for the given endpoint key, or "" if there is none.
// Called on every check, so it only takes a read lock.
func For(key string) string {
	ensureLoaded()
	mu.RLock()
	defer mu.RUnlock()
	return overrides[key]
}

// Set records an override for the given endpoint key and persists it. The
// in-memory change is rolled back if the write fails, so what the UI reads back
// is always what survived to disk.
func Set(key, target string) error {
	ensureLoaded()
	mu.Lock()
	defer mu.Unlock()
	previous, had := overrides[key]
	if !had && len(overrides) >= maxOverrides {
		return ErrTooManyTargets
	}
	overrides[key] = strings.TrimSpace(target)
	if err := persist(); err != nil {
		if had {
			overrides[key] = previous
		} else {
			delete(overrides, key)
		}
		return err
	}
	return nil
}

// Clear removes the override for the given endpoint key, sending it back to the
// address config.yaml gave it. Clearing a key that has no override is not an
// error; the caller asked for a state that is already true.
func Clear(key string) error {
	ensureLoaded()
	mu.Lock()
	defer mu.Unlock()
	previous, had := overrides[key]
	if !had {
		return nil
	}
	delete(overrides, key)
	if err := persist(); err != nil {
		overrides[key] = previous
		return err
	}
	return nil
}

// All returns a copy of every override, keyed by endpoint key.
func All() map[string]string {
	ensureLoaded()
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]string, len(overrides))
	for key, target := range overrides {
		out[key] = target
	}
	return out
}

// Keys returns every overridden endpoint key, sorted.
func Keys() []string {
	ensureLoaded()
	mu.RLock()
	defer mu.RUnlock()
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}


// --- edit token -------------------------------------------------------------

var (
	tokenMu     sync.Mutex
	cachedToken string
)

// EditToken returns the credential that gates target editing.
//
// GATUS_EDIT_TOKEN wins when it is set, so an operator who wants to manage the
// secret themselves still can. Otherwise one is generated on first use and kept
// in /data, which is the mounted volume, so it survives both a rebuild and
// update.sh's git reset. That is the point: requiring a hand-edited .env on the
// box meant the feature shipped switched off and stayed that way, and a feature
// nobody can turn on is not a feature.
//
// The token is never logged. /data is bind-mounted, so reading it back is
// `cat data/edit_token` on the host - no shell in the container, and no secret
// sitting in `docker logs`.
func EditToken() string {
	if fromEnv := strings.TrimSpace(os.Getenv("GATUS_EDIT_TOKEN")); fromEnv != "" {
		return fromEnv
	}
	tokenMu.Lock()
	defer tokenMu.Unlock()
	if cachedToken != "" {
		return cachedToken
	}
	if b, err := os.ReadFile(editTokenPath); err == nil {
		if existing := strings.TrimSpace(string(b)); existing != "" {
			cachedToken = existing
			return cachedToken
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// Without a random token there is no safe value to fall back to, so
		// editing stays off rather than being gated on something guessable.
		logr.Errorf("[targets.EditToken] Could not generate an edit token, so target editing stays disabled: %s", err.Error())
		return ""
	}
	generated := base64.RawURLEncoding.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(editTokenPath), 0o755); err != nil {
		logr.Errorf("[targets.EditToken] Could not create %s: %s", filepath.Dir(editTokenPath), err.Error())
		return ""
	}
	// 0600: this is the one real secret the dashboard holds.
	if err := os.WriteFile(editTokenPath, []byte(generated+"\n"), 0o600); err != nil {
		logr.Errorf("[targets.EditToken] Could not persist the edit token to %s, so target editing stays disabled: %s", editTokenPath, err.Error())
		return ""
	}
	logr.Infof("[targets.EditToken] Generated an edit token for the dashboard target editor and wrote it to %s (read it with: cat data/edit_token)", editTokenPath)
	cachedToken = generated
	return cachedToken
}
