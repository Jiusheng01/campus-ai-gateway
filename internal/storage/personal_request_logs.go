package storage

import (
	"context"
	"errors"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// ErrPersonalRequestLogNotFound is the single observable answer for both "no
// such record" and "a record owned by someone else". The personal API must not
// distinguish the two: a 403 on another Account's record would confirm that the
// id exists.
var ErrPersonalRequestLogNotFound = errors.New("request record not found")

// PersonalRequestLogFilter narrows the member's own Request Records.
//
// OwnerUserID is not a filter in the same sense as the others: it is the
// authorisation predicate, always bound from the session, and there is no field
// here for a client-supplied owner or tenant. The remaining fields may only
// narrow the owner's set.
type PersonalRequestLogFilter struct {
	OwnerUserID string
	Limit       int
	Offset      int
	// Model matches either the requested alias or the actual alias, as the
	// record carries them.
	Model string
	// Status is 0 for "any"; otherwise it is the exact HTTP status code.
	Status int
	// APIKeyID narrows to one key. The owner predicate still applies, so naming
	// another Account's key matches nothing rather than widening the set.
	APIKeyID string
	// From and To bound created_at and are inclusive. A zero time is unbounded
	// on that side.
	From time.Time
	To   time.Time
}

// consoleRequestLogColumns is the member projection.
//
// It is intentionally not requestLogColumns: that projection carries the client
// IP, tenant id, provider, upstream model and route priority, which belong to
// the operator's view. The member sees the request they can recognise and
// debug, and the detail adds the attempt chain.
const consoleRequestLogColumns = `
SELECT
	request_id,
	COALESCE(api_key_id::text,''),
	COALESCE(requested_model,''),
	COALESCE(actual_model,''),
	COALESCE(input_tokens,0),
	COALESCE(output_tokens,0),
	COALESCE(total_tokens,0),
	COALESCE(latency_ms,0),
	COALESCE(status_code,0),
	COALESCE(error_code,''),
	COALESCE(failover,false),
	COALESCE(attempts,0),
	created_at
FROM gateway_request_log`

// consoleRequestLogWhere fixes the owner predicate at $1. Every other condition
// is an AND, so no combination of filters can select a row outside the owner's
// set. Identity and time parameters are explicitly typed because `”::uuid`
// would raise instead of matching nothing.
const consoleRequestLogWhere = `
WHERE owner_user_id = $1::uuid
  AND ($2='' OR requested_model=$2 OR actual_model=$2)
  AND ($3=0 OR status_code=$3)
  AND ($4::uuid IS NULL OR api_key_id=$4::uuid)
  AND ($5::timestamptz IS NULL OR created_at >= $5::timestamptz)
  AND ($6::timestamptz IS NULL OR created_at <= $6::timestamptz)`

func consoleRequestLogArgs(f PersonalRequestLogFilter) []any {
	return []any{
		f.OwnerUserID,
		f.Model,
		f.Status,
		optionalString(f.APIKeyID),
		optionalTime(f.From),
		optionalTime(f.To),
	}
}

// rowScanner is implemented by pgx.Row and pgx.Rows; it lets the list and the
// detail read their projection through the same scan, so the two cannot drift.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanConsoleRequestLog(row rowScanner) (model.ConsoleRequestLog, error) {
	var v model.ConsoleRequestLog
	err := row.Scan(
		&v.RequestID, &v.APIKeyID, &v.RequestedModel, &v.ActualModel,
		&v.InputTokens, &v.OutputTokens, &v.TotalTokens, &v.LatencyMS,
		&v.StatusCode, &v.ErrorCode, &v.Failover, &v.AttemptCount, &v.CreatedAt,
	)
	return v, err
}

// ListPersonalRequestLogs returns the Account's own Request Records, newest
// first.
//
// The owner predicate is part of the WHERE clause, so another Account's rows are
// never selected and cannot be revealed by a filter or a page boundary.
func (p *Postgres) ListPersonalRequestLogs(ctx context.Context, f PersonalRequestLogFilter) ([]model.ConsoleRequestLog, error) {
	args := append(consoleRequestLogArgs(f), f.Limit, f.Offset)
	rows, err := p.Pool.Query(ctx, consoleRequestLogColumns+consoleRequestLogWhere+`
ORDER BY created_at DESC, id DESC
LIMIT $7 OFFSET $8
`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.ConsoleRequestLog, 0, 16)
	for rows.Next() {
		v, err := scanConsoleRequestLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PersonalRequestLogDetail returns one record and its attempt chain only when
// the owner snapshot matches the Account. A row owned by someone else is
// reported as not found, exactly like a row that does not exist.
func (p *Postgres) PersonalRequestLogDetail(ctx context.Context, ownerID, requestID string) (model.ConsoleRequestLogDetail, error) {
	args := append(consoleRequestLogArgs(PersonalRequestLogFilter{OwnerUserID: ownerID}), requestID)
	v, err := scanConsoleRequestLog(p.Pool.QueryRow(ctx, consoleRequestLogColumns+consoleRequestLogWhere+`
  AND request_id=$7
`, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ConsoleRequestLogDetail{}, ErrPersonalRequestLogNotFound
	}
	if err != nil {
		return model.ConsoleRequestLogDetail{}, err
	}

	// The owner check above is what licenses this read: RequestAttempts is keyed
	// by request id alone, so it must never be called before the record has been
	// proved to belong to the caller.
	attempts, err := p.RequestAttempts(ctx, requestID)
	if err != nil {
		return model.ConsoleRequestLogDetail{}, err
	}
	if attempts == nil {
		attempts = []model.RequestAttempt{}
	}
	return model.ConsoleRequestLogDetail{ConsoleRequestLog: v, AttemptChain: attempts}, nil
}
