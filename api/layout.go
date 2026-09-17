package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

// Dashboard layout: which cards and rows are shown, in what order, under what
// name. It is PRESENTATION ONLY. Nothing here changes what is monitored, what
// is checked, what alerts, or what a card's status says — hiding a row hides
// the row, and a hidden row that goes down still turns its card red. Pausing
// (see monitoring.go) is the control that actually stops a check, and the two
// are deliberately kept apart.
//
// Shared rather than per-browser on purpose: the point is that the wallboards,
// the TV and a laptop all show the same arranged dashboard. Per-viewer taste
// (colours, sound, refresh interval) stays in localStorage on the client.
//
// Renaming lives here rather than in config.yaml because an endpoint's key is
// sanitize(group)_sanitize(name): renaming either one in config mints a new key,
// and the next boot deletes the history of every key that is no longer in the
// config. A rename must never be a config edit.

type rowLayout struct {
	Hidden bool   `json:"hidden,omitempty"`
	Order  int    `json:"order"`
	Label  string `json:"label,omitempty"` // empty = use the derived label
}

type cardLayout struct {
	Hidden bool                 `json:"hidden,omitempty"`
	Order  int                  `json:"order"`
	Title  string               `json:"title,omitempty"` // empty = use the endpoint name
	Rows   map[string]rowLayout `json:"rows,omitempty"`
}

type layoutFile struct {
	// Version is what makes a future shape change migratable instead of a
	// silent reinterpretation of everyone's arrangement. None of the older
	// stores in this directory have one, which is a mistake worth not copying.
	Version int                   `json:"version"`
	Cards   map[string]cardLayout `json:"cards"`
}

const (
	layoutPath    = "/data/layout.json"
	layoutVersion = 1

	// Bounds. The layout is keyed by card name and row key, and nothing prunes
	// an entry when an endpoint leaves config.yaml — a card that is temporarily
	// absent (a collector that has not pushed yet, an endpoint commented out for
	// an afternoon) must keep its arrangement, so pruning would be wrong. Caps
	// are what keeps that from growing without limit.
	maxLayoutCards = 200
	maxLayoutRows  = 40
	maxLabelLength = 60
)

var (
	layoutMu     sync.RWMutex
	layoutCards  = map[string]cardLayout{}
	layoutLoaded bool
)

func ensureLayoutLoaded() {
	layoutMu.Lock()
	defer layoutMu.Unlock()
	if layoutLoaded {
		return
	}
	layoutLoaded = true
	b, err := os.ReadFile(layoutPath)
	if err != nil {
		return // no file yet: every card and row takes its derived defaults
	}
	var f layoutFile
	if err := json.Unmarshal(b, &f); err != nil {
		// Do not let the next write quietly overwrite a file we failed to read:
		// somebody's whole arrangement is in there. Keep a copy, say so loudly,
		// and carry on with defaults.
		logr.Errorf("[api.ensureLayoutLoaded] %s is unreadable (%s); keeping a copy at %s.corrupt and starting from defaults", layoutPath, err.Error(), layoutPath)
		if err := os.WriteFile(layoutPath+".corrupt", b, 0o644); err != nil {
			logr.Errorf("[api.ensureLayoutLoaded] could not preserve the unreadable file: %s", err.Error())
		}
		return
	}
	// Clamp on read as well as on write: /data is on the host and hand-editable.
	layoutCards = sanitizeLayout(f.Cards)
}

func persistLayout() error {
	// caller holds layoutMu
	f := layoutFile{Version: layoutVersion, Cards: layoutCards}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	// /data exists only because compose mounts it; outside Docker it may not,
	// and every other store in this package silently loses its writes there.
	if err := os.MkdirAll(filepath.Dir(layoutPath), 0o755); err != nil {
		return err
	}
	// Write to a sibling and rename: a truncated file parses as no file at all,
	// which would silently reset the whole dashboard to its derived defaults.
	tmp := layoutPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, layoutPath)
}

// sanitizeLayout bounds and normalizes whatever arrived, from the file or from
// a request. Out-of-range input is clamped rather than rejected: this is a
// dashboard arrangement, and refusing the whole document over one long label
// would be worse than trimming it.
func sanitizeLayout(in map[string]cardLayout) map[string]cardLayout {
	out := make(map[string]cardLayout, len(in))
	for _, name := range sortedCardNames(in) {
		if len(out) >= maxLayoutCards {
			break
		}
		card := in[name]
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		clean := cardLayout{Hidden: card.Hidden, Order: card.Order, Title: trimLabel(card.Title)}
		if len(card.Rows) > 0 {
			clean.Rows = make(map[string]rowLayout, len(card.Rows))
			for _, key := range sortedRowKeys(card.Rows) {
				if len(clean.Rows) >= maxLayoutRows {
					break
				}
				row := card.Rows[key]
				key = strings.TrimSpace(key)
				if key == "" {
					continue
				}
				clean.Rows[key] = rowLayout{Hidden: row.Hidden, Order: row.Order, Label: trimLabel(row.Label)}
			}
		}
		out[name] = clean
	}
	return out
}

func trimLabel(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxLabelLength {
		s = strings.TrimSpace(s[:maxLabelLength])
	}
	return s
}

// Deterministic iteration so the cap drops the same entries every time rather
// than whichever ones the map happened to yield first.
func sortedCardNames(m map[string]cardLayout) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedRowKeys(m map[string]rowLayout) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// GetLayout returns the whole arrangement. An empty object means every card and
// row is at its derived default, which is what a fresh deployment serves.
func GetLayout(c *fiber.Ctx) error {
	ensureLayoutLoaded()
	layoutMu.RLock()
	defer layoutMu.RUnlock()
	return c.Status(200).JSON(layoutFile{Version: layoutVersion, Cards: layoutCards})
}

// SetLayout replaces the whole arrangement. Whole-document rather than per-card
// patches because the document is small, the editor holds all of it anyway, and
// one writer replacing one value cannot then race a second writer into a
// half-applied state.
func SetLayout(c *fiber.Ctx) error {
	ensureLayoutLoaded()
	var body layoutFile
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": `invalid body: expected {"cards":{...}}`})
	}
	clean := sanitizeLayout(body.Cards)
	layoutMu.Lock()
	previous := layoutCards
	layoutCards = clean
	err := persistLayout()
	if err != nil {
		layoutCards = previous // keep memory and disk agreeing
	}
	layoutMu.Unlock()
	if err != nil {
		// Unlike the older stores here, a failed write is reported rather than
		// logged and shrugged off. Somebody is watching this one land.
		logr.Errorf("[api.SetLayout] could not save the layout: %s", err.Error())
		return c.Status(500).JSON(fiber.Map{"error": "could not save the layout: " + err.Error()})
	}
	layoutMu.RLock()
	defer layoutMu.RUnlock()
	return c.Status(200).JSON(layoutFile{Version: layoutVersion, Cards: layoutCards})
}

// ResetLayout drops every override and returns the dashboard to what config.yaml
// implies.
func ResetLayout(c *fiber.Ctx) error {
	ensureLayoutLoaded()
	layoutMu.Lock()
	previous := layoutCards
	layoutCards = map[string]cardLayout{}
	err := persistLayout()
	if err != nil {
		layoutCards = previous
	}
	layoutMu.Unlock()
	if err != nil {
		logr.Errorf("[api.ResetLayout] could not save the layout: %s", err.Error())
		return c.Status(500).JSON(fiber.Map{"error": "could not reset the layout: " + err.Error()})
	}
	return c.Status(200).JSON(layoutFile{Version: layoutVersion, Cards: map[string]cardLayout{}})
}
