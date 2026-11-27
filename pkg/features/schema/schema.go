// Package schema holds the Ent schema for the tenant_settings table.
//
// features.Manager reads and writes the table with SQL, so it works without
// Ent codegen (create it with Manager.Migrate). Use this schema instead
// when Ent or Atlas manages your migrations:
//
//	// ent/schema/tenant_setting.go
//	type TenantSetting struct{ schema.TenantSetting }
//
// A custom Options.Table name only applies to Manager.Migrate.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TenantSetting is one tenant's value for one feature. id is
// "tenant_id|name". A missing row means the tenant uses the code default.
type TenantSetting struct{ ent.Schema }

// Annotations of TenantSetting.
func (TenantSetting) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "tenant_settings"}}
}

// Fields of TenantSetting.
func (TenantSetting) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").MaxLen(191).Immutable(),
		field.String("tenant_id").MaxLen(191).Immutable(),
		field.String("name").MaxLen(191).Immutable(),
		field.Text("value"),
		field.String("updated_by").MaxLen(191).Optional().Nillable(),
		field.Int64("updated_at"),
	}
}

// Indexes of TenantSetting.
func (TenantSetting) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id")}
}
