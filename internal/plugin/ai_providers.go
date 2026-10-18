package plugin

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"cpa-key-billing/internal/billing"
)

const maxAIProviderIndexes = 4096

// The host's per-type configuration lists provide the runtime auth indexes.
func (a *App) aiProviderUsage(req ManagementRequest) ManagementResponse {
	var from, to time.Time
	if err := timeRangeParams(req.Query, &from, &to); err != nil {
		return errorResponse(err)
	}
	indexes := make([]string, 0, len(req.Query["auth_index"]))
	for _, value := range req.Query["auth_index"] {
		index := strings.TrimSpace(value)
		if index == "" || len(index) > 512 {
			return JSONError(http.StatusBadRequest, "invalid", "Invalid configured credential identifier")
		}
		indexes = append(indexes, index)
	}
	slices.Sort(indexes)
	indexes = slices.Compact(indexes)
	if len(indexes) > maxAIProviderIndexes {
		return JSONError(http.StatusBadRequest, "invalid", "Too many configured credentials")
	}
	usage := map[string]billing.CredentialUsageStats{}
	if len(indexes) > 0 {
		var err error
		if usage, err = a.store.CredentialUsageStats(indexes, "", from, to); err != nil {
			return errorResponse(err)
		}
	}
	return JSONResponse(http.StatusOK, map[string]any{"usage": usage})
}
