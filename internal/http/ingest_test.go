package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kongken/kapi/internal/ingest"
)

type testSZXFlightSubmitter struct {
	receipt ingest.SZXFlightReceipt
	err     error
	calls   int
	batch   ingest.SZXFlightBatch
}

func (s *testSZXFlightSubmitter) Submit(_ context.Context, batch ingest.SZXFlightBatch) (ingest.SZXFlightReceipt, error) {
	s.calls++
	s.batch = batch
	return s.receipt, s.err
}

func TestSZXFlightIngestRouteAcceptsAuthenticatedBatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	submitter := &testSZXFlightSubmitter{receipt: ingest.SZXFlightReceipt{
		RunID:     "c91abb94-dc37-40aa-bc68-d362e5375690",
		Airport:   "szx",
		Direction: "departure",
		Total:     12,
	}}
	router := gin.New()
	registerSZXFlightIngestRoute(router, "collector-secret", submitter)

	body := `{"schemaVersion":1,"runId":"c91abb94-dc37-40aa-bc68-d362e5375690","source":"szairport","airport":"szx","direction":"departure","serviceDate":"2026-09-14","collectedAt":"2026-09-14T06:40:00Z","pages":[]}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/v1/ingest/szx/flights", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer collector-secret")
	req.Header.Set("Idempotency-Key", "c91abb94-dc37-40aa-bc68-d362e5375690")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, recorder.Code, recorder.Body.String())
	}
	if submitter.calls != 1 {
		t.Fatalf("expected one submission, got %d", submitter.calls)
	}
	if submitter.batch.RunID != "c91abb94-dc37-40aa-bc68-d362e5375690" {
		t.Fatalf("unexpected submitted run ID %q", submitter.batch.RunID)
	}
}

func TestSZXFlightIngestRouteRejectsMissingCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	submitter := &testSZXFlightSubmitter{}
	router := gin.New()
	registerSZXFlightIngestRoute(router, "collector-secret", submitter)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/v1/ingest/szx/flights", strings.NewReader(`{}`))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, recorder.Code)
	}
	if submitter.calls != 0 {
		t.Fatalf("expected no submission, got %d", submitter.calls)
	}
}

func TestSZXFlightIngestRouteMapsInvalidAndStaleBatches(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid", err: ingest.ErrInvalidSZXFlightBatch, wantStatus: http.StatusUnprocessableEntity},
		{name: "stale", err: ingest.ErrStaleSZXFlightBatch, wantStatus: http.StatusConflict},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			submitter := &testSZXFlightSubmitter{err: test.err}
			router := gin.New()
			registerSZXFlightIngestRoute(router, "collector-secret", submitter)

			body := `{"runId":"c91abb94-dc37-40aa-bc68-d362e5375690"}`
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/v1/ingest/szx/flights", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer collector-secret")
			req.Header.Set("Idempotency-Key", "c91abb94-dc37-40aa-bc68-d362e5375690")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)

			if recorder.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", test.wantStatus, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestSZXFlightIngestRouteReturnsOKForDuplicate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	submitter := &testSZXFlightSubmitter{receipt: ingest.SZXFlightReceipt{
		RunID:       "c91abb94-dc37-40aa-bc68-d362e5375690",
		Airport:     "szx",
		Direction:   "arrival",
		CollectedAt: time.Date(2026, time.September, 14, 6, 40, 0, 0, time.UTC),
		Duplicate:   true,
	}}
	router := gin.New()
	registerSZXFlightIngestRoute(router, "collector-secret", submitter)

	body := `{"runId":"c91abb94-dc37-40aa-bc68-d362e5375690"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/v1/ingest/szx/flights", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer collector-secret")
	req.Header.Set("Idempotency-Key", "c91abb94-dc37-40aa-bc68-d362e5375690")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
}

func TestSZXFlightIngestRouteRejectsMismatchedIdempotencyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	submitter := &testSZXFlightSubmitter{}
	router := gin.New()
	registerSZXFlightIngestRoute(router, "collector-secret", submitter)

	body := `{"runId":"c91abb94-dc37-40aa-bc68-d362e5375690"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/v1/ingest/szx/flights", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer collector-secret")
	req.Header.Set("Idempotency-Key", "d0cd78ec-fd1c-4a27-b7df-c20ee2bb9646")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, recorder.Code)
	}
	if submitter.calls != 0 {
		t.Fatalf("expected no submission, got %d", submitter.calls)
	}
}

func TestSZXFlightIngestRouteRejectsMalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	submitter := &testSZXFlightSubmitter{err: errors.New("must not be called")}
	router := gin.New()
	registerSZXFlightIngestRoute(router, "collector-secret", submitter)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/v1/ingest/szx/flights", strings.NewReader(`{"schemaVersion":`))
	req.Header.Set("Authorization", "Bearer collector-secret")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, recorder.Code)
	}
	if submitter.calls != 0 {
		t.Fatalf("expected no submission, got %d", submitter.calls)
	}
}
