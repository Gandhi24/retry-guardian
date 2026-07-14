package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"retry-guardian/internal/engine"
	"retry-guardian/internal/rules"
	"retry-guardian/internal/store"
)

type RecordHandler struct {
	store *store.Store
	table *rules.Table
}

func NewRecordHandler(st *store.Store, table *rules.Table) *RecordHandler {
	return &RecordHandler{store: st, table: table}
}

func (h *RecordHandler) Handle(c *gin.Context) {
	var req engine.RecordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := engine.Record(c.Request.Context(), req, h.store, h.table)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrPaymentNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case errors.Is(err, engine.ErrMissingAuthData):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			slog.ErrorContext(c.Request.Context(), "record failed",
				"payment_id", req.PaymentID,
				"outcome", req.Outcome,
				"error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		}
		return
	}

	slog.InfoContext(c.Request.Context(), "record",
		"payment_id", req.PaymentID,
		"outcome", req.Outcome,
	)

	c.JSON(http.StatusAccepted, gin.H{"status": "recorded"})
}
