package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"retry-guardian/internal/rules"
)

type RulesHandler struct {
	table *rules.Table
}

func NewRulesHandler(table *rules.Table) *RulesHandler {
	return &RulesHandler{table: table}
}

// Handle serves GET /v1/internal/retry-guard/rules.
// Returns the active rules table so callers can verify what the service is enforcing.
// Durations are formatted as human-readable strings rather than nanoseconds.
func (h *RulesHandler) Handle(c *gin.Context) {
	c.JSON(http.StatusOK, toRulesResponse(h.table))
}

// ---- response types (duration as string, not nanoseconds) ---------------

type rulesResponse struct {
	Version          string               `json:"rules_version"`
	Defaults         defaultsResponse     `json:"defaults"`
	MACRules         map[string]ruleEntry `json:"mac_rules"`
	NetworkCodeIndex map[string]ruleEntry `json:"network_code_index"`
}

type defaultsResponse struct {
	Window      string `json:"window"`
	MaxAttempts int    `json:"max_attempts"`
}

type ruleEntry struct {
	Class    string `json:"class"`
	Reason   string `json:"reason"`
	Cooldown string `json:"cooldown,omitempty"`
}

func toRulesResponse(t *rules.Table) rulesResponse {
	macRules := make(map[string]ruleEntry, len(t.MACRules))
	for code, r := range t.MACRules {
		macRules[code] = ruleEntry{
			Class:    string(r.Class),
			Reason:   r.Reason,
			Cooldown: formatDuration(r.Cooldown),
		}
	}

	networkCodes := make(map[string]ruleEntry, len(t.NetworkCodeIndex))
	for key, r := range t.NetworkCodeIndex {
		networkCodes[key] = ruleEntry{
			Class:    string(r.Class),
			Reason:   r.Reason,
			Cooldown: formatDuration(r.Cooldown),
		}
	}

	return rulesResponse{
		Version: t.Version,
		Defaults: defaultsResponse{
			Window:      t.Defaults.Window.String(),
			MaxAttempts: t.Defaults.MaxAttempts,
		},
		MACRules:         macRules,
		NetworkCodeIndex: networkCodes,
	}
}

func formatDuration(d time.Duration) string {
	if d == 0 {
		return ""
	}
	return d.String()
}
