# Database & Ent ORM Integration 🗄️

Rather than relying on runtime reflection (which degrades performance and makes schema refactoring error-prone), GoLaravel integrates **Ent** as the core ORM layer.

---

## 1. Why Ent ORM?

- **Zero Runtime Reflection**: All builders, queries, and mutations are generated into native Go code at compile time.
- **100% Type-Safe Relations**: Direct graph edge traversal without manual SQL joins.
- **AI-Optimized Ergonomics**: Generated methods and explicit Go interfaces make LLM code completion accurate and eliminate hallucinations.

---

## 2. Defining Schemas (`app/schema/`)

Create schema definitions using pure Go under `app/schema/`:

```bash
artisan make:schema Invoice
```

Edit `app/schema/invoice.go`:

```go
package schema

import (
	"time"
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type Invoice struct {
	ent.Schema
}

func (Invoice) Fields() []ent.Field {
	return []ent.Field{
		field.String("invoice_number").Unique().NotEmpty(),
		field.String("customer_name").NotEmpty(),
		field.Float("amount").Default(0),
		field.String("status").Default("sent"),
		field.String("token").Unique(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (Invoice) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("owner", User.Type).Ref("invoices").Unique(),
		edge.To("items", LineItem.Type),
	}
}
```

---

## 3. Generating Models

Whenever schemas change, execute:

```bash
artisan generate
```

This compiles type-safe entities into `app/ent/` (or `app/`), including:
- Query builders (`client.Invoice.Query()`)
- Predicate filters (`invoice.StatusEQ("paid")`, `invoice.AmountGTE(100)`)
- Mutators (`client.Invoice.Create()`, `client.Invoice.UpdateOneID()`)

---

## 4. Querying & Graph Edge Traversal

Traverse graph edges cleanly without manual joins:

```go
// Equivalent to: Invoice::with(['items', 'owner'])->where('status', 'sent')->get()
invoices, err := client.Invoice.
	Query().
	Where(invoice.StatusEQ("sent")).
	WithItems().
	WithOwner().
	Order(app.Desc(invoice.FieldCreatedAt)).
	All(ctx)
```

---

## 5. Atomic Transactions

Execute mutations inside a transaction with automated rollback on error or panic:

```go
tx, err := client.Tx(ctx)
if err != nil {
	return err
}

inv, err := tx.Invoice.Create().
	SetCustomerName("Acme Corp").
	SetAmount(1500.00).
	Save(ctx)
if err != nil {
	_ = tx.Rollback()
	return err
}

_, err = tx.LineItem.Create().
	SetInvoice(inv).
	SetDescription("Server Hosting").
	SetQuantity(1).
	SetUnitPrice(1500.00).
	Save(ctx)
if err != nil {
	_ = tx.Rollback()
	return err
}

return tx.Commit()
```

---

## 6. Connection Pool Optimization

GoLaravel tunes the underlying database connection pool for extreme concurrency:

```go
import "github.com/golaravel/golaravel/pkg/database"

db, err := database.OpenDB(database.Config{
	Driver:          "postgres",
	DSN:             "postgres://user:pass@localhost:5432/app_db?sslmode=disable",
	MaxOpenConns:    100, // Maximum concurrent open sockets
	MaxIdleConns:    25,  // Kept warm in pool
	ConnMaxLifetime: 15 * time.Minute,
})
```
