package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// LineItem holds the schema definition for the LineItem entity.
type LineItem struct {
	ent.Schema
}

// Fields of the LineItem.
func (LineItem) Fields() []ent.Field {
	return []ent.Field{
		field.String("description").NotEmpty(),
		field.Int("quantity").Default(1),
		field.Float("unit_price").Default(0),
		field.Float("total").Default(0),
	}
}

// Edges of the LineItem.
func (LineItem) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("invoice", Invoice.Type).
			Ref("items").
			Unique().
			Required(),
	}
}
