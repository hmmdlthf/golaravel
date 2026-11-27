// Package schema is a test fixture: a tenant-scoped entity.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"

	"github.com/golaravel/golaravel/pkg/privacy"
)

// Note is a tenant-scoped entity.
type Note struct{ ent.Schema }

// Mixin of Note.
func (Note) Mixin() []ent.Mixin {
	return []ent.Mixin{privacy.TenantMixin{}}
}

// Fields of Note.
func (Note) Fields() []ent.Field {
	return []ent.Field{field.String("title").Default("")}
}
