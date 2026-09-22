package api

import (
	"crypto/subtle"
	"strings"

	"github.com/TwiN/gatus/v5/config"
	"github.com/TwiN/gatus/v5/config/endpoint"
	"github.com/TwiN/gatus/v5/targets"
	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

// Runtime target editing.
//
// config.yaml is baked into the image and update.sh wipes the working tree, so
// re-pointing a check has always meant a rebuild and a redeploy. These handlers
// let an operator do it from the dashboard instead; the override lives in
// /data (see the targets package) and the watchdog picks it up on the next tick.
//
// UNLIKE every other write route on this server, these are gated. The rest of
// the API is open because it is read-mostly and presentational: the worst a
// caller can do is rename a card or pause a check, both of which are visible
// and trivially reversible. Re-pointing a probe is different in kind - this
// container holds NET_RAW - so it needs a credential, and the only shape of
// credential this deployment has is a shared bearer token.
//
// The token comes from targets.EditToken(): GATUS_EDIT_TOKEN when an operator
// sets it, otherwise one generated into /data on first use. It is deliberately
// NOT a hand-edited .env entry on the box - .env is gitignored, so update.sh
// never carries it over, and a feature that ships switched off on every deploy
// is one nobody ever turns on.

// authorizeEdit reports whether the caller may edit a target. When it returns
// false it has ALREADY written the response, and the handler must return the
// accompanying error immediately.
//
// The two return values are not redundant. c.Status(401).SendString(...) writes
// the response and returns a nil error, so a handler that only checks the error
// sails straight past a rejected request and answers 200 over the top of its
// own 401. The bool is the part that actually says no.
//
// Fails CLOSED: with no token configured there is no way to authorize an edit,
// so editing is off rather than open to anyone who can reach the port.
func authorizeEdit(c *fiber.Ctx) (bool, error) {
	expected := targets.EditToken()
	if expected == "" {
		logr.Warnf("[api.authorizeEdit] Refusing a target edit because no edit token could be resolved")
		return false, c.Status(503).SendString("target editing is disabled: the server could not resolve an edit token")
	}
	authorizationHeader := string(c.Request().Header.Peek("Authorization"))
	if !strings.HasPrefix(authorizationHeader, "Bearer ") {
		return false, c.Status(401).SendString("invalid Authorization header")
	}
	provided := strings.TrimSpace(strings.TrimPrefix(authorizationHeader, "Bearer "))
	// Constant-time, unlike the collector push token's plain !=. A shared secret
	// that one host can guess a byte at a time is not much of a secret.
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		logr.Errorf("[api.authorizeEdit] Rejected a target edit with an invalid token")
		return false, c.Status(401).SendString("invalid token")
	}
	return true, nil
}

// endpointByKey finds a configured (non-external) endpoint by its key.
func endpointByKey(cfg *config.Config, key string) *endpoint.Endpoint {
	for _, ep := range cfg.Endpoints {
		if ep.Key() == key {
			return ep
		}
	}
	return nil
}

type targetView struct {
	Key string `json:"key"`
	// Name and Group are echoed so the UI can label a row without a second call.
	Name  string `json:"name"`
	Group string `json:"group"`
	// Configured is what config.yaml says. Effective is what is actually being
	// checked. They differ exactly when Overridden is true, which is what lets
	// the UI show "differs from config" and offer a reset.
	Configured string `json:"configured"`
	Effective  string `json:"effective"`
	Overridden bool   `json:"overridden"`
	// Editable is false when the endpoint hides its URL in the UI. Honouring
	// HideURL matters: without it this endpoint would be a way to read back the
	// very addresses that flag exists to conceal.
	Editable bool `json:"editable"`
}

func viewFor(ep *endpoint.Endpoint, override string) targetView {
	hidden := ep.UIConfig != nil && ep.UIConfig.HideURL
	v := targetView{
		Key:        ep.Key(),
		Name:       ep.Name,
		Group:      ep.Group,
		Configured: ep.URL,
		Effective:  ep.URL,
		Overridden: override != "" && override != ep.URL,
		Editable:   !hidden,
	}
	if v.Overridden {
		v.Effective = override
	}
	if hidden {
		v.Configured, v.Effective = "", ""
	}
	return v
}

// GetEndpointTargets returns every configured endpoint's target, plus whether it
// is currently overridden. Unauthenticated, like the rest of the read API - it
// exposes the same addresses the config drill-in already shows, minus any the
// endpoint asked to hide.
func GetEndpointTargets(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		overrides := targets.All()
		views := make([]targetView, 0, len(cfg.Endpoints))
		for _, ep := range cfg.Endpoints {
			views = append(views, viewFor(ep, overrides[ep.Key()]))
		}
		// Report whether editing is even possible, so the UI can hide the
		// control instead of offering one that always 503s.
		return c.Status(200).JSON(fiber.Map{
			"editingEnabled": targets.EditToken() != "",
			"targets":        views,
		})
	}
}

type setTargetRequest struct {
	URL string `json:"url"`
}

// SetEndpointTarget overrides one endpoint's target. Takes effect on that
// endpoint's next check, with no restart.
func SetEndpointTarget(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if ok, err := authorizeEdit(c); !ok {
			return err
		}
		key := c.Params("key")
		ep := endpointByKey(cfg, key)
		if ep == nil {
			// Validated against the live config for the same reason
			// SetMonitoringForKey does it: otherwise typos accumulate as dead
			// keys in a file nobody ever reads again.
			return c.Status(404).SendString("no configured endpoint with key " + key)
		}
		if ep.UIConfig != nil && ep.UIConfig.HideURL {
			return c.Status(403).SendString("this endpoint hides its URL and cannot be re-pointed from the dashboard")
		}
		var body setTargetRequest
		if err := c.BodyParser(&body); err != nil {
			return c.Status(400).SendString("invalid request body")
		}
		requested := strings.TrimSpace(body.URL)
		if err := targets.Validate(ep.URL, requested); err != nil {
			return c.Status(400).SendString(err.Error())
		}
		// Asking for the configured value back is a reset, not an override.
		// Storing it would leave a permanent entry that silently ignores any
		// later config.yaml change for this endpoint.
		if requested == ep.URL {
			if err := targets.Clear(key); err != nil {
				logr.Errorf("[api.SetEndpointTarget] Could not clear the override for key=%s: %s", key, err.Error())
				return c.Status(500).SendString("could not persist the change")
			}
		} else if err := targets.Set(key, requested); err != nil {
			logr.Errorf("[api.SetEndpointTarget] Could not persist the override for key=%s: %s", key, err.Error())
			return c.Status(500).SendString("could not persist the change")
		}
		logr.Infof("[api.SetEndpointTarget] Target for key=%s is now %s (config says %s)", key, requested, ep.URL)
		return c.Status(200).JSON(viewFor(ep, targets.For(key)))
	}
}

// DeleteEndpointTarget drops an endpoint's override, sending it back to the
// address config.yaml gave it.
func DeleteEndpointTarget(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if ok, err := authorizeEdit(c); !ok {
			return err
		}
		key := c.Params("key")
		ep := endpointByKey(cfg, key)
		if ep == nil {
			return c.Status(404).SendString("no configured endpoint with key " + key)
		}
		if err := targets.Clear(key); err != nil {
			logr.Errorf("[api.DeleteEndpointTarget] Could not clear the override for key=%s: %s", key, err.Error())
			return c.Status(500).SendString("could not persist the change")
		}
		logr.Infof("[api.DeleteEndpointTarget] Target for key=%s is back to its configured value %s", key, ep.URL)
		return c.Status(200).JSON(viewFor(ep, ""))
	}
}
