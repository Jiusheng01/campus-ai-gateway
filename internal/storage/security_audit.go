package storage

import (
	"context"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

func (p *Postgres) InsertAdminAudit(ctx context.Context, v model.AdminAuditLog) error {
	_, err := p.Pool.Exec(ctx, `
INSERT INTO gateway_admin_audit_log(request_id, actor, action, resource, status_code, client_ip)
VALUES (NULLIF($1,''), $2, $3, $4, $5, NULLIF($6,''))
`, v.RequestID, v.Actor, v.Action, v.Resource, v.StatusCode, v.ClientIP)
	return err
}

func (p *Postgres) ListAdminAudit(ctx context.Context, limit int) ([]model.AdminAuditLog, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT id, COALESCE(request_id,''), actor, action, resource, status_code, COALESCE(client_ip,''), created_at
FROM gateway_admin_audit_log
ORDER BY id DESC
LIMIT $1
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AdminAuditLog
	for rows.Next() {
		var v model.AdminAuditLog
		if err := rows.Scan(&v.ID, &v.RequestID, &v.Actor, &v.Action, &v.Resource, &v.StatusCode, &v.ClientIP, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) InsertSecurityEvent(ctx context.Context, v model.SecurityEvent) error {
	_, err := p.Pool.Exec(ctx, `
INSERT INTO gateway_security_event(request_id, tenant_id, api_key_id, event_type, client_ip, requested_model, detail)
VALUES (
	NULLIF($1,''),
	NULLIF($2,'')::uuid,
	NULLIF($3,'')::uuid,
	$4,
	NULLIF($5,''),
	NULLIF($6,''),
	NULLIF($7,'')
)
`, v.RequestID, v.TenantID, v.APIKeyID, v.EventType, v.ClientIP, v.RequestedModel, v.Detail)
	return err
}

func (p *Postgres) ListSecurityEvents(ctx context.Context, limit int) ([]model.SecurityEvent, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT id, COALESCE(request_id,''), COALESCE(tenant_id::text,''), COALESCE(api_key_id::text,''),
       event_type, COALESCE(client_ip,''), COALESCE(requested_model,''), COALESCE(detail,''), created_at
FROM gateway_security_event
ORDER BY id DESC
LIMIT $1
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SecurityEvent
	for rows.Next() {
		var v model.SecurityEvent
		if err := rows.Scan(&v.ID, &v.RequestID, &v.TenantID, &v.APIKeyID, &v.EventType, &v.ClientIP, &v.RequestedModel, &v.Detail, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
