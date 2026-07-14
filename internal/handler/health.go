package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"retry-guardian/internal/store"
)

type HealthHandler struct {
	store *store.Store
}

func NewHealthHandler(st *store.Store) *HealthHandler {
	return &HealthHandler{store: st}
}

// Live handles GET /health — always 200 if the process is running.
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Ready handles GET /ready — 200 only if Redis is reachable.
func (h *HealthHandler) Ready(c *gin.Context) {
	if err := h.store.Ping(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unavailable",
			"reason": "redis unreachable",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
