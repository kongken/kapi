package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/kongken/kapi/internal/flight"
	"github.com/kongken/kapi/internal/szx"
)

const SZXFlightSchemaVersion = 1

const (
	maxSZXCollectionAge = 24 * time.Hour
	maxSZXFutureSkew    = 5 * time.Minute
)

var (
	ErrInvalidSZXFlightBatch = errors.New("invalid SZX flight batch")
	ErrStaleSZXFlightBatch   = errors.New("stale SZX flight batch")
)

var shanghaiLocation = mustLoadLocation("Asia/Shanghai")

// SZXFlightBatch is the versioned transport contract used by remote SZX
// collectors. Pages contain untouched upstream JSON; kapi owns normalization.
type SZXFlightBatch struct {
	SchemaVersion int             `json:"schemaVersion"`
	RunID         string          `json:"runId"`
	Source        string          `json:"source"`
	Airport       string          `json:"airport"`
	Direction     string          `json:"direction"`
	ServiceDate   string          `json:"serviceDate"`
	CollectedAt   time.Time       `json:"collectedAt"`
	Pages         []szx.DailyPage `json:"pages"`
}

type SZXFlightReceipt struct {
	RunID       string    `json:"runId"`
	Airport     string    `json:"airport"`
	Direction   string    `json:"direction"`
	ServiceDate string    `json:"serviceDate"`
	CollectedAt time.Time `json:"collectedAt"`
	AcceptedAt  time.Time `json:"acceptedAt"`
	Total       int       `json:"total"`
	Duplicate   bool      `json:"duplicate"`
}

type SnapshotLoader interface {
	Load(ctx context.Context, airportCode string, direction string, serviceDate string) ([]byte, error)
}

type SnapshotLoaderFunc func(context.Context, string, string, string) ([]byte, error)

func (f SnapshotLoaderFunc) Load(ctx context.Context, airportCode string, direction string, serviceDate string) ([]byte, error) {
	return f(ctx, airportCode, direction, serviceDate)
}

type SnapshotWriter interface {
	Save(ctx context.Context, snapshot flight.DailySnapshot) error
}

type SnapshotWriterFunc func(context.Context, flight.DailySnapshot) error

func (f SnapshotWriterFunc) Save(ctx context.Context, snapshot flight.DailySnapshot) error {
	return f(ctx, snapshot)
}

// SZXFlightIngestor validates collection runs, preserves ordering and publishes
// canonical snapshots through one interface.
type SZXFlightIngestor struct {
	loader SnapshotLoader
	writer SnapshotWriter
	now    func() time.Time
	mu     sync.Mutex
}

func NewSZXFlightIngestor(loader SnapshotLoader, writer SnapshotWriter) *SZXFlightIngestor {
	return &SZXFlightIngestor{
		loader: loader,
		writer: writer,
		now:    time.Now,
	}
}

func (i *SZXFlightIngestor) Submit(ctx context.Context, batch SZXFlightBatch) (SZXFlightReceipt, error) {
	now := i.now()
	if err := validateSZXFlightBatch(batch, now); err != nil {
		return SZXFlightReceipt{}, err
	}

	response, err := szx.BuildDailySnapshot(szx.DailyCollection{
		RunID:       batch.RunID,
		ServiceDate: batch.ServiceDate,
		Direction:   batch.Direction,
		CollectedAt: batch.CollectedAt,
		Pages:       batch.Pages,
	})
	if err != nil {
		return SZXFlightReceipt{}, fmt.Errorf("%w: %v", ErrInvalidSZXFlightBatch, err)
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	existing, err := i.loader.Load(ctx, "szx", batch.Direction, batch.ServiceDate)
	if err == nil {
		var metadata struct {
			RunID       string    `json:"runId"`
			CollectedAt time.Time `json:"collectedAt"`
			Total       int       `json:"total"`
		}
		if err := json.Unmarshal(existing, &metadata); err != nil {
			return SZXFlightReceipt{}, fmt.Errorf("decode current SZX snapshot metadata: %w", err)
		}
		if metadata.RunID == batch.RunID {
			return newSZXFlightReceipt(batch, now, metadata.Total, true), nil
		}
		if !metadata.CollectedAt.IsZero() && !batch.CollectedAt.After(metadata.CollectedAt) {
			return SZXFlightReceipt{}, fmt.Errorf(
				"%w: current snapshot was collected at %s",
				ErrStaleSZXFlightBatch,
				metadata.CollectedAt.Format(time.RFC3339Nano),
			)
		}
	} else if !errors.Is(err, flight.ErrDailySnapshotNotFound) {
		return SZXFlightReceipt{}, fmt.Errorf("load current SZX snapshot: %w", err)
	}

	data, err := json.Marshal(response)
	if err != nil {
		return SZXFlightReceipt{}, fmt.Errorf("encode SZX daily snapshot: %w", err)
	}
	if err := i.writer.Save(ctx, flight.DailySnapshot{
		AirportCode: "szx",
		Direction:   batch.Direction,
		ServiceDate: batch.ServiceDate,
		RunID:       batch.RunID,
		CollectedAt: batch.CollectedAt,
		Data:        data,
	}); err != nil {
		return SZXFlightReceipt{}, fmt.Errorf("save SZX daily snapshot: %w", err)
	}

	return newSZXFlightReceipt(batch, now, response.Total, false), nil
}

func validateSZXFlightBatch(batch SZXFlightBatch, now time.Time) error {
	if batch.SchemaVersion != SZXFlightSchemaVersion {
		return fmt.Errorf("%w: schemaVersion must be %d", ErrInvalidSZXFlightBatch, SZXFlightSchemaVersion)
	}
	runID, err := uuid.Parse(batch.RunID)
	if err != nil || runID == uuid.Nil || runID.String() != strings.ToLower(batch.RunID) {
		return fmt.Errorf("%w: runId must be a canonical, non-zero UUID", ErrInvalidSZXFlightBatch)
	}
	if batch.Source != "szairport" {
		return fmt.Errorf("%w: source must be szairport", ErrInvalidSZXFlightBatch)
	}
	if batch.Airport != "szx" {
		return fmt.Errorf("%w: airport must be szx", ErrInvalidSZXFlightBatch)
	}
	if batch.Direction != "departure" && batch.Direction != "arrival" {
		return fmt.Errorf("%w: direction must be departure or arrival", ErrInvalidSZXFlightBatch)
	}
	if batch.CollectedAt.IsZero() {
		return fmt.Errorf("%w: collectedAt is required", ErrInvalidSZXFlightBatch)
	}
	if batch.CollectedAt.After(now.Add(maxSZXFutureSkew)) {
		return fmt.Errorf("%w: collectedAt is too far in the future", ErrInvalidSZXFlightBatch)
	}
	if batch.CollectedAt.Before(now.Add(-maxSZXCollectionAge)) {
		return fmt.Errorf("%w: collectedAt is older than %s", ErrInvalidSZXFlightBatch, maxSZXCollectionAge)
	}
	if _, err := time.ParseInLocation(time.DateOnly, batch.ServiceDate, shanghaiLocation); err != nil {
		return fmt.Errorf("%w: serviceDate must use YYYY-MM-DD", ErrInvalidSZXFlightBatch)
	}
	collectedServiceDate := batch.CollectedAt.In(shanghaiLocation).Format(time.DateOnly)
	if batch.ServiceDate != collectedServiceDate {
		return fmt.Errorf(
			"%w: serviceDate %s does not match collectedAt date %s",
			ErrInvalidSZXFlightBatch,
			batch.ServiceDate,
			collectedServiceDate,
		)
	}
	return nil
}

func newSZXFlightReceipt(batch SZXFlightBatch, acceptedAt time.Time, total int, duplicate bool) SZXFlightReceipt {
	return SZXFlightReceipt{
		RunID:       batch.RunID,
		Airport:     batch.Airport,
		Direction:   batch.Direction,
		ServiceDate: batch.ServiceDate,
		CollectedAt: batch.CollectedAt,
		AcceptedAt:  acceptedAt,
		Total:       total,
		Duplicate:   duplicate,
	}
}

func mustLoadLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return location
}
