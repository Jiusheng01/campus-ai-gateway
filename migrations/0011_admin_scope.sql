-- 0011_admin_scope.sql
-- Confines a tenant administrator to one subtree of the tenant hierarchy.
--
-- NULL means global reach: super administrators and gateway administrators see
-- every tenant. A non-NULL value restricts the account to the strict
-- descendants of that tenant, which is what turns delegation into an enforced
-- rule rather than a convention -- "the department allocates to its teachers,
-- a teacher allocates to their students" is checked on every write.
--
-- The scope tenant itself is deliberately excluded from the reachable set. An
-- administrator must not be able to raise their own budget; changing the scope
-- tenant's limits stays with whoever allocated them.
--
-- ON DELETE RESTRICT, not SET NULL: a NULL scope means global reach, so
-- nulling it when a tenant is deleted would silently promote the account to
-- administrator-of-everything. Refusing the delete is loud and reversible.

ALTER TABLE gateway_admin_user
  ADD COLUMN IF NOT EXISTS scope_tenant_id UUID REFERENCES gateway_tenant(id) ON DELETE RESTRICT;

CREATE INDEX IF NOT EXISTS idx_gateway_admin_user_scope
  ON gateway_admin_user(scope_tenant_id);
