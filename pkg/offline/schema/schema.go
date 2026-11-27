// Package schema holds Ent schemas for the offline sync tables and the
// mixin that makes an entity syncable.
//
// The engine reads and writes its tables with SQL, so they work without
// Ent codegen (create them with Engine.Migrate). Use these schemas instead
// when Ent or Atlas manages your migrations: embed them in your schema
// package and the generated tables match what the engine expects.
//
//	// ent/schema/sync.go
//	type SyncLedger struct{ schema.SyncLedger }
//	type SyncDevice struct{ schema.SyncDevice }
//	type SyncTenant struct{ schema.SyncTenant }
//
// Table prefixes (Options.Prefix) only apply to Engine.Migrate.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"entgo.io/ent/schema/mixin"
)

// SyncableMixin adds what a synced entity needs: a client-generated string
// id (devices create rows offline, so ids cannot come from the database),
// the row's version, its latest hybrid logical clock, and per-field clocks
// for field-level last-write-wins. Pair it with privacy.TenantMixin and
// privacy.SoftDeleteMixin (deletes must leave tombstones devices can pull).
type SyncableMixin struct{ mixin.Schema }

// Fields of the mixin.
func (SyncableMixin) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().MaxLen(191).Immutable(),
		field.Int64("sync_version").Default(0),
		field.String("sync_hlc").Default(""),
		field.JSON("sync_clocks", map[string]string{}).Optional(),
	}
}

// SyncLedger is the append-only log of processed mutations. id is
// "tenant_id|mutation_id", which makes idempotency per tenant.
type SyncLedger struct{ ent.Schema }

// Annotations of SyncLedger.
func (SyncLedger) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "sync_ledger"}}
}

// Fields of SyncLedger.
func (SyncLedger) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.String("tenant_id").Immutable(),
		field.String("mutation_id").Immutable(),
		field.Int64("seq").Default(0).Immutable(),
		field.String("device_id").Immutable(),
		field.String("user_id").Optional().Immutable(),
		field.String("entity").Immutable(),
		field.String("entity_id").Immutable(),
		field.String("op").Immutable(),
		field.String("hlc").Optional().Immutable(),
		field.String("group_id").Optional().Immutable(),
		field.String("status").Immutable(),
		field.Text("reason").Optional().Immutable(),
		field.Text("payload").Optional().Immutable(),
		field.Int64("created_at").Immutable(),
	}
}

// Indexes of SyncLedger.
func (SyncLedger) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "seq").StorageKey("sync_ledger_seq_idx"),
		index.Fields("tenant_id", "entity", "entity_id").StorageKey("sync_ledger_entity_idx"),
	}
}

// SyncDevice is a registered client installation and its pull cursor.
type SyncDevice struct{ ent.Schema }

// Annotations of SyncDevice.
func (SyncDevice) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "sync_devices"}}
}

// Fields of SyncDevice.
func (SyncDevice) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.String("tenant_id").Immutable(),
		field.String("user_id").Optional(),
		field.String("name").Optional(),
		field.String("platform").Optional(),
		field.String("app_version").Optional(),
		field.Text("public_key").Optional(),
		field.Int64("cursor_seq").Default(0),
		field.Int64("created_at").Immutable(),
		field.Int64("last_push_at").Optional().Nillable(),
		field.Int64("last_pull_at").Optional().Nillable(),
		field.Int64("revoked_at").Optional().Nillable(),
	}
}

// Indexes of SyncDevice.
func (SyncDevice) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id").StorageKey("sync_devices_tenant_idx")}
}

// SyncTenant holds each tenant's ledger sequence (locked by every ledger
// write so sequence numbers commit in order) and the compaction horizon.
type SyncTenant struct{ ent.Schema }

// Annotations of SyncTenant.
func (SyncTenant) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "sync_tenants"}}
}

// Fields of SyncTenant.
func (SyncTenant) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.Int64("seq").Default(0),
		field.Int64("horizon").Default(0),
	}
}
