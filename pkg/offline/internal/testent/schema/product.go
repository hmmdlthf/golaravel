// Package schema is a test fixture: a synced product, generated with the
// sql/execquery feature like an app would be.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"

	offline "github.com/golaravel/golaravel/pkg/offline/schema"
	"github.com/golaravel/golaravel/pkg/privacy"
)

// Product is a synced entity.
type Product struct{ ent.Schema }

// Mixin of Product.
func (Product) Mixin() []ent.Mixin {
	return []ent.Mixin{offline.SyncableMixin{}, privacy.TenantMixin{}, privacy.SoftDeleteMixin{}}
}

// Fields of Product.
func (Product) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Default(""),
		field.Int64("price").Default(0),
		field.Float("stock").Default(0),
	}
}

// The sync tables, managed by Ent.
type (
	SyncLedger struct{ offline.SyncLedger }
	SyncDevice struct{ offline.SyncDevice }
	SyncTenant struct{ offline.SyncTenant }
)
