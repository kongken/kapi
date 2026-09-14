package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/kongken/kapi/internal/ingest"
)

const maxSZXFlightIngestBodyBytes int64 = 28 << 20

type szxFlightSubmitter interface {
	Submit(ctx context.Context, batch ingest.SZXFlightBatch) (ingest.SZXFlightReceipt, error)
}

func registerSZXFlightIngestRoute(r *gin.Engine, token string, submitter szxFlightSubmitter) {
	if token == "" || submitter == nil {
		return
	}
	r.POST("/internal/v1/ingest/szx/flights", handleSZXFlightIngest(token, submitter))
}

func handleSZXFlightIngest(token string, submitter szxFlightSubmitter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !validBearerToken(c.GetHeader("Authorization"), token) {
			c.Header("WWW-Authenticate", "Bearer")
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"message": "valid collector credentials are required",
			})
			return
		}

		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSZXFlightIngestBodyBytes)
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()

		var batch ingest.SZXFlightBatch
		if err := decoder.Decode(&batch); err != nil {
			writeSZXFlightDecodeError(c, err)
			return
		}
		if err := ensureJSONEnd(decoder); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_json",
				"message": err.Error(),
			})
			return
		}
		if idempotencyKey := c.GetHeader("Idempotency-Key"); idempotencyKey == "" || idempotencyKey != batch.RunID {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_idempotency_key",
				"message": "Idempotency-Key must match runId",
			})
			return
		}

		receipt, err := submitter.Submit(c.Request.Context(), batch)
		if err != nil {
			switch {
			case errors.Is(err, ingest.ErrInvalidSZXFlightBatch):
				c.JSON(http.StatusUnprocessableEntity, gin.H{
					"error":   "invalid_collection",
					"message": err.Error(),
				})
			case errors.Is(err, ingest.ErrStaleSZXFlightBatch):
				c.JSON(http.StatusConflict, gin.H{
					"error":   "stale_collection",
					"message": err.Error(),
				})
			default:
				slog.Error("SZX flight ingestion failed",
					"run_id", batch.RunID,
					"direction", batch.Direction,
					"error", err,
				)
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error":   "ingestion_unavailable",
					"message": "the collection could not be persisted",
				})
			}
			return
		}

		status := http.StatusCreated
		if receipt.Duplicate {
			status = http.StatusOK
		}
		slog.Info("accepted SZX flight collection",
			"run_id", receipt.RunID,
			"direction", receipt.Direction,
			"service_date", receipt.ServiceDate,
			"total", receipt.Total,
			"duplicate", receipt.Duplicate,
		)
		c.JSON(status, receipt)
	}
}

func validBearerToken(authorization string, expected string) bool {
	scheme, token, ok := strings.Cut(strings.TrimSpace(authorization), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func writeSZXFlightDecodeError(c *gin.Context, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error":   "payload_too_large",
			"message": "collection payload exceeds the allowed size",
		})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{
		"error":   "invalid_json",
		"message": err.Error(),
	})
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("request body must contain exactly one JSON object")
}
