package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Invoice holds the schema definition for the Invoice entity.
type Invoice struct {
	ent.Schema
}

// Fields of the Invoice.
func (Invoice) Fields() []ent.Field {
	return []ent.Field{
		field.String("invoice_number").Unique().NotEmpty(),
		field.String("customer_name").NotEmpty(),
		field.String("customer_email").NotEmpty(),
		field.String("status").Default("sent"),
		field.Float("amount").Default(0),
		field.Time("due_date").Default(time.Now),
		field.String("token").Unique().NotEmpty(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

// Edges of the Invoice.
func (Invoice) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("owner", User.Type).
			Ref("invoices").
			Unique(),
		edge.To("items", LineItem.Type),
	}
}
