package handler

import (
	"log/slog"
	"net/http"

	"retry-guardian/internal/engine"
	"retry-guardian/internal/rules"
	"retry-guardian/internal/store"

	"github.com/gin-gonic/gin"
)

type EvaluateHandler struct {
	store *store.Store
	table *rules.Table
}

func NewEvaluateHandler(st *store.Store, table *rules.Table) *EvaluateHandler {
	return &EvaluateHandler{store: st, table: table}
}

func (h *EvaluateHandler) Handle(c *gin.Context) {
	var req engine.EvaluateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp := engine.Evaluate(c.Request.Context(), req, h.store)

	slog.InfoContext(c.Request.Context(), "evaluate",
		"payment_id", req.PaymentID,
		"merchant_id", req.MerchantID,
		"network", req.Network,
		"decision", resp.Decision,
		"reason_code", resp.ReasonCode,
	)

	c.JSON(http.StatusOK, resp)
}
