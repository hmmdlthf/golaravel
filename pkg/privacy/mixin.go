package privacy

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"entgo.io/ent/schema/mixin"
)

// SoftDeleteMixin adds a nullable `deleted_at` timestamp field and database index
// to an Ent schema, automatically enabling soft delete queries and mutations.
type SoftDeleteMixin struct {
	mixin.Schema
}

// Fields provides the `deleted_at` field definition.
func (SoftDeleteMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Time("deleted_at").
			Optional().
			Nillable(),
	}
}

// Indexes adds a database index on `deleted_at` for fast filtering.
func (SoftDeleteMixin) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("deleted_at"),
	}
}

// TenantMixin adds a mandatory `tenant_id` string field and database index
// to an Ent schema, automatically enabling tenant privacy scopes.
type TenantMixin struct {
	mixin.Schema
}

// Fields provides the `tenant_id` field definition.
func (TenantMixin) Fields() []ent.Field {
	return []ent.Field{
		field.String("tenant_id").
			NotEmpty(),
	}
}

// Indexes adds a database index on `tenant_id` for fast scoped lookups.
func (TenantMixin) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
	}
}
