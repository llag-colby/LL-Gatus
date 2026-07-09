package api

import (
	"time"

	"github.com/gofiber/fiber/v2"
)

// Version is the build identifier. It is injected at build time via
// -ldflags "-X github.com/TwiN/gatus/v5/api.Version=<git-sha>" so a deploy can
// be verified against the git commit that produced the running binary.
var Version = "dev"

// VersionHandler returns the running build version.
func VersionHandler(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"version": Version})
}

// TimeHandler returns the server's current time (Unix ms) so all clients can
// anchor relative "x ago" labels to the same clock regardless of local time.
func TimeHandler(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"time": time.Now().UnixMilli()})
}
