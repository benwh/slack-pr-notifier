package middleware

import (
	"net/http"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"github.com/gin-gonic/gin"
)

// CloudTasksAuthMiddleware creates middleware that verifies static secret from Cloud Tasks.
func CloudTasksAuthMiddleware(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// Extract secret from custom header
		providedSecret := c.GetHeader("X-Cloud-Tasks-Secret")
		if providedSecret == "" {
			log.Error(ctx, "Missing X-Cloud-Tasks-Secret header for Cloud Tasks request")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			c.Abort()
			return
		}

		// Verify the secret matches our configured value
		if providedSecret != cfg.CloudTasksSecret {
			log.Error(ctx, "Invalid Cloud Tasks secret provided")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication failed"})
			c.Abort()
			return
		}

		log.Debug(ctx, "Cloud Tasks authentication successful")
		c.Next()
	}
}
