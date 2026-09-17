package api

import (
	"errors"
	"strings"

	"github.com/TwiN/gatus/v5/auth"
	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

// Session sign-in for the dashboard's own controls. DORMANT: none of these
// handlers is routed and RequireRole is attached to nothing. The dashboard has
// no sign-in, /v1/auth/* does not exist, and main.go never opens the accounts
// database, so auth.Enabled() is false and RequireRole would fail open anyway.
// The file is kept so sign-in can be restored by registering it again.
//
// It describes a separate mechanism from Gatus's built-in `security:` block, and
// the two were deliberately not wired together: `security:` puts one shared
// credential in front of whole route groups, which is the wrong shape for "any
// of these three people may pause an endpoint, and only one of them may add an
// account". `security:` is not installed either (see api.go).
//
// Collector pushes (/v1/endpoints/:key/external, /v1/phones/:key and
// /v1/unifi/:key) authenticate with the bearer token configured on their
// external endpoint and never pass through here. A token identifies a machine,
// a session identifies a person: gating a machine's push on a person's cookie
// would take the whole dashboard's data feed down the moment nobody was signed
// in, which on a wallboard is always.

// sessionUserLocal is where a resolved caller is cached for the rest of the
// request, so a route carrying RequireRole does not repeat the session lookup
// in its handler.
const sessionUserLocal = "authUser"

// currentUser resolves the caller from the session cookie, or nil when the
// request carries no usable session. A nil user is not an error: almost all of
// this API is readable anonymously by design.
func currentUser(c *fiber.Ctx) *auth.User {
	if cached, ok := c.Locals(sessionUserLocal).(*auth.User); ok && cached != nil {
		return cached
	}
	user := auth.UserForSession(c.Cookies(auth.SessionCookieName))
	if user != nil {
		c.Locals(sessionUserLocal, user)
	}
	return user
}

// RequireRole returns middleware that refuses callers below minimum. Unused: it
// is attached to no route. If sign-in returns, attach it to the individual
// routes that change state and leave every GET anonymous, because the wallboards
// run with no session and nobody is standing in front of them to sign one in.
func RequireRole(minimum auth.Role) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Fail OPEN when the auth database could not be opened. Every control
		// here was anonymous before accounts existed, so this is a return to the
		// previous behaviour on a LAN tool rather than a hole. Failing closed
		// would instead strand the on-call tech in front of a dashboard whose
		// every button is refused until someone repairs a SQLite file, which is
		// exactly the moment they can least afford it.
		if !auth.Enabled() {
			return c.Next()
		}
		user := currentUser(c)
		if user == nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "sign in to do that"})
		}
		if !user.Role.AtLeast(minimum) {
			logr.Warnf("[api.RequireRole] Refused %s %s for %s: role %s is below %s", c.Method(), c.Path(), user.Username, user.Role, minimum)
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "your account does not have permission to do that"})
		}
		return c.Next()
	}
}

// --- cookie ----------------------------------------------------------------

// setSessionCookie hands the browser its session token.
//
// Secure is set only when the request actually arrived over TLS. This dashboard
// is served over plain http on the LAN, and a Secure cookie there is accepted
// and then never sent back, which presents as a login that silently does
// nothing: the request succeeds, the page reloads, and the user is anonymous
// again.
//
// SameSite is Lax rather than Strict so the cookie survives a click from a
// bookmark, a chat message or a link on another internal page. Lax still keeps
// the cookie off cross-site POSTs, which is what stops a form on some other
// origin from pausing an endpoint on the reader's behalf.
func setSessionCookie(c *fiber.Ctx, token string) {
	c.Cookie(&fiber.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		Secure:   c.Protocol() == "https",
		MaxAge:   int(auth.SessionTTL().Seconds()),
	})
}

// clearSessionCookie expires the cookie. Every attribute other than the value
// matches setSessionCookie, because a browser only replaces a cookie whose
// name, path and domain all line up.
func clearSessionCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		Secure:   c.Protocol() == "https",
		MaxAge:   -1,
	})
}

// --- handlers ---------------------------------------------------------------

// AuthLogin exchanges credentials for a session cookie.
//
// The response carries the whole auth.User rather than a trimmed-down copy: its
// PasswordHash field is tagged "-", so the type cannot leak the one field that
// matters, and returning the same shape here as /v1/users saves the frontend
// from carrying two notions of what a user looks like.
func AuthLogin(c *fiber.Ctx) error {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "send a username and a password"})
	}
	session, user, err := auth.Login(strings.TrimSpace(body.Username), body.Password)
	if err != nil {
		return respondWithAuthError(c, "AuthLogin", err)
	}
	// Drop whatever session this browser already held. Signing in twice would
	// otherwise leave the first session alive and unreachable for its full TTL,
	// with no way for its owner to end it.
	if previous := c.Cookies(auth.SessionCookieName); previous != "" && previous != session.Token {
		auth.Logout(previous)
	}
	setSessionCookie(c, session.Token)
	// auth.Login already logs the sign-in, so there is nothing to add here.
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"user": user})
}

// AuthLogout ends the caller's session and clears the cookie. Logging out
// without a session is not an error, so this always answers 204.
func AuthLogout(c *fiber.Ctx) error {
	auth.Logout(c.Cookies(auth.SessionCookieName))
	clearSessionCookie(c)
	return c.SendStatus(fiber.StatusNoContent)
}

// AuthMe reports whether accounts are available and who, if anyone, is signed
// in.
//
// This ALWAYS answers 200, never 401. The frontend calls it on every page load
// to decide what to render, so an error status would be indistinguishable from
// the server being down and would paint an outage over a perfectly healthy
// anonymous visit. "enabled" false means the auth database could not be opened,
// which is the UI's cue to hide the sign-in entirely and show every control,
// matching how RequireRole fails open.
func AuthMe(c *fiber.Ctx) error {
	user := currentUser(c)
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"enabled":       auth.Enabled(),
		"authenticated": user != nil,
		"user":          user,
	})
}

// AuthChangePassword lets the signed-in user replace their own password. An
// admin changing somebody else's goes through PATCH /v1/users/:id instead.
func AuthChangePassword(c *fiber.Ctx) error {
	if !auth.Enabled() {
		return respondWithAuthError(c, "AuthChangePassword", auth.ErrNotConfigured)
	}
	user := currentUser(c)
	if user == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "sign in to change your password"})
	}
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "send a currentPassword and a newPassword"})
	}
	// Re-check the current password through Login, which is the only exported
	// path that knows how passwords are compared, rather than teaching this
	// package about hashing. It mints a throwaway session as a side effect:
	// SetPassword drops every session this user holds, so on the happy path the
	// throwaway is gone a moment later, and the failure path below ends it
	// explicitly instead of leaving a live session behind a rejected change.
	session, _, err := auth.Login(user.Username, body.CurrentPassword)
	if err != nil {
		return respondWithAuthError(c, "AuthChangePassword", err)
	}
	if err = auth.SetPassword(user.ID, body.NewPassword); err != nil {
		auth.Logout(session.Token)
		return respondWithAuthError(c, "AuthChangePassword", err)
	}
	// Changing a password invalidates every session its owner held, including
	// the one that made this request, so the cookie now points at a session that
	// no longer exists. Clear it rather than leaving the browser to discover
	// that on its next write.
	clearSessionCookie(c)
	logr.Infof("[api.AuthChangePassword] %s changed their own password", user.Username)
	return c.SendStatus(fiber.StatusNoContent)
}

// --- errors -----------------------------------------------------------------

// respondWithAuthError maps an auth package error onto a status code. Anything
// unrecognised is treated as a server fault: it is logged with its detail and
// answered with a generic message, so a SQLite or bcrypt failure never reaches
// the browser. handler names the caller for the log line.
//
// None of these errors carry a password, and none of the callers put one in a
// log line either.
func respondWithAuthError(c *fiber.Ctx, handler string, err error) error {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, auth.ErrWeakPassword):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, auth.ErrUserExists):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, auth.ErrUserNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, auth.ErrLastAdmin):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, auth.ErrNotConfigured):
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "user accounts are unavailable on this instance"})
	default:
		logr.Errorf("[api.%s] %s", handler, err.Error())
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "something went wrong"})
	}
}
