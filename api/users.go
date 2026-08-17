package api

import (
	"strings"

	"github.com/TwiN/gatus/v5/auth"
	"github.com/TwiN/logr"
	"github.com/gofiber/fiber/v2"
)

// Account administration. Every route in this file is registered behind
// RequireRole(auth.RoleAdmin) in api.go; none of these handlers re-check the
// caller's role, so keep that gate on anything added under /v1/users.
//
// Usernames and roles are validated here rather than left to the auth package.
// Its errors for "no username" and "unknown role" are plain errors with no
// sentinel to match on, so they would fall through to a 500; caught here they
// are the 400 they actually are.

// ListUsers returns every account, oldest first.
//
// Nothing is stripped from the response because there is nothing to strip: the
// auth.User type tags PasswordHash "-", so the hashes the auth package loads
// cannot be marshalled back out.
func ListUsers(c *fiber.Ctx) error {
	users, err := auth.ListUsers()
	if err != nil {
		return respondWithAuthError(c, "ListUsers", err)
	}
	return c.Status(fiber.StatusOK).JSON(users)
}

// CreateUser adds an account. Body: {"username","password","role"}.
func CreateUser(c *fiber.Ctx) error {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": `invalid body: expected {"username","password","role"}`})
	}
	username := strings.TrimSpace(body.Username)
	if username == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "username is required"})
	}
	role := auth.Role(strings.TrimSpace(body.Role))
	if !role.Valid() {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "role must be one of viewer, operator or admin"})
	}
	user, err := auth.CreateUser(username, body.Password, role)
	if err != nil {
		return respondWithAuthError(c, "CreateUser", err)
	}
	logr.Infof("[api.CreateUser] Created account %s with role %s", user.Username, user.Role)
	return c.Status(fiber.StatusCreated).JSON(user)
}

// UpdateUser changes a role, a password, or both. Body: {"role"?,"password"?},
// where an absent field is left alone (hence the pointers: an omitted role and
// a role of "" have to be told apart).
//
// The role is applied first because it is the change that can be refused on its
// own merits: demoting the last admin fails and leaves the account completely
// untouched. Should the password then be rejected for being too short, the new
// role stands and the 400 names the part that did not land, which is the least
// confusing way for a two-field update to half-fail.
func UpdateUser(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil || id <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id must be a positive integer"})
	}
	var body struct {
		Role     *string `json:"role"`
		Password *string `json:"password"`
	}
	if err = c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": `invalid body: expected {"role":"...","password":"..."}`})
	}
	if body.Role == nil && body.Password == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "send a role, a password, or both"})
	}
	var changed []string
	if body.Role != nil {
		role := auth.Role(strings.TrimSpace(*body.Role))
		if !role.Valid() {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "role must be one of viewer, operator or admin"})
		}
		if err = auth.SetRole(int64(id), role); err != nil {
			return respondWithAuthError(c, "UpdateUser", err)
		}
		changed = append(changed, "role="+string(role))
	}
	if body.Password != nil {
		if err = auth.SetPassword(int64(id), *body.Password); err != nil {
			return respondWithAuthError(c, "UpdateUser", err)
		}
		// The new password is named, never quoted: this line is the one place a
		// plaintext password could plausibly end up in a log file.
		changed = append(changed, "password")
	}
	user, err := auth.GetUser(int64(id))
	if err != nil {
		return respondWithAuthError(c, "UpdateUser", err)
	}
	logr.Infof("[api.UpdateUser] Updated account %s (%s)", user.Username, strings.Join(changed, ", "))
	return c.Status(fiber.StatusOK).JSON(user)
}

// DeleteUser removes an account. The auth package refuses to remove the last
// admin, so this cannot lock the system out of its own settings.
func DeleteUser(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil || id <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id must be a positive integer"})
	}
	// Read the account first so the log line can name it. Afterwards there is
	// nothing left to look up, and "deleted user 7" is not much of a record.
	user, err := auth.GetUser(int64(id))
	if err != nil {
		return respondWithAuthError(c, "DeleteUser", err)
	}
	if err = auth.DeleteUser(int64(id)); err != nil {
		return respondWithAuthError(c, "DeleteUser", err)
	}
	logr.Infof("[api.DeleteUser] Deleted account %s", user.Username)
	return c.SendStatus(fiber.StatusNoContent)
}
