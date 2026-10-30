# Ent Model Factories & Faker Data Seeder 🏭

GoLaravel brings Laravel-style **Model Factories and Faker Data Generation** to Go and Ent ORM (`pkg/database/factory`). It provides compile-time generic model factories, deterministic fake data generation with zero external dependencies, sequence counters, state mutations, and integration with Ent database seeders and automated tests.

---

## 1. Defining Model Factories

Model factories are typically stored in `database/factories/` and created using the Artisan CLI:

```bash
artisan make:factory User
```

This generates `database/factories/user_factory.go`:

```go
package factories

import (
    "context"
    "fmt"

    "github.com/golaravel/golaravel/app"
    "github.com/golaravel/golaravel/pkg/database/factory"
    "github.com/golaravel/golaravel/pkg/hash"
)

type UserDef struct {
    Name     string
    Email    string
    Password string
    Role     string
    IsActive bool
}

// UserFactory returns the configured factory for User models.
func UserFactory() *factory.Factory[UserDef] {
    return factory.New[UserDef]().
        Definition(func(f *factory.Faker) UserDef {
            return UserDef{
                Name:     f.Name(),
                Email:    f.SafeEmail(),
                Password: "password123",
                Role:     "member",
                IsActive: true,
            }
        }).
        Creator(func(ctx context.Context, client any, def UserDef) (any, error) {
            c, ok := client.(*app.Client)
            if !ok {
                return nil, fmt.Errorf("expected *app.Client, got %T", client)
            }

            hashed, _ := hash.Make(def.Password)
            return c.User.Create().
                SetName(def.Name).
                SetEmail(def.Email).
                SetPassword(hashed).
                SetRole(def.Role).
                Save(ctx)
        })
}
```

---

## 2. Using Factories in Seeders & Tests

Factories support both in-memory generation (`Make`) and direct database persistence (`Create`):

### In-Memory Generation (`Make` / `MakeOne`)
Great for unit tests where database IO is unnecessary:
```go
// Generate 1 in-memory model definition
user := factories.UserFactory().MakeOne()

// Generate a slice of 10 in-memory models
users := factories.UserFactory().Count(10).Make()
```

### Database Persistence (`Create` / `CreateOne`)
Persists records directly to the database via the Ent client:
```go
// Persist 1 record into the database
createdUser, err := factories.UserFactory().CreateOne(ctx, client)

// Persist 50 records in bulk
createdUsers, err := factories.UserFactory().Count(50).Create(ctx, client)
```

---

## 3. Factory States & Sequence Counters

### State Transformations (`.State`)
Create specialized variants of a model (e.g. admins, inactive accounts, verified emails):

```go
// Define an admin state factory
adminFactory := factories.UserFactory().
    State(func(u *factories.UserDef) {
        u.Role = "admin"
        u.Email = "admin@example.com"
    })

admin, err := adminFactory.CreateOne(ctx, client)
```

### Indexed Sequences (`.Sequence`)
Generate incremental IDs, indexed email addresses, or staggered dates:

```go
factoryWithSeq := factories.UserFactory().
    Sequence(func(index int, u *factories.UserDef) {
        u.Email = fmt.Sprintf("user_%d@test.com", index+1)
    })

// Produces: user_1@test.com, user_2@test.com, user_3@test.com...
users := factoryWithSeq.Count(5).Make()
```

### Lifecycle Hooks (`AfterMaking` / `AfterCreating`)
Attach callbacks to execute immediately after in-memory assembly or database saving:

```go
invoiceFactory := factories.InvoiceFactory().
    AfterCreating(func(ctx context.Context, client any, entity any) error {
        inv := entity.(*app.Invoice)
        // Automatically seed line items for every invoice
        return seedLineItemsForInvoice(ctx, client, inv)
    })
```

---

## 4. Built-in High-Performance Faker Engine

GoLaravel includes a comprehensive, zero-dependency Faker engine (`factory.Faker`) with dozens of data generators:

```go
import "github.com/golaravel/golaravel/pkg/database/factory"

f := factory.NewFaker()
```

### Generator Reference

| Category | Method | Sample Output |
|---|---|---|
| **Identity** | `f.Name()` | `"Sarah Connor"` |
| | `f.FirstName()` | `"Sarah"` |
| | `f.LastName()` | `"Connor"` |
| | `f.Username()` | `"sarah_connor42"` |
| **Internet** | `f.Email()` | `"sarah.connor12@starlight.io"` |
| | `f.SafeEmail()` | `"sarah.connor12@example.com"` |
| | `f.DomainName()` | `"acme.dev"` |
| | `f.URL()` | `"https://www.acme.dev"` |
| | `f.IPv4()` | `"192.168.1.10"` |
| | `f.IPv6()` | `"2001:db8:85a3::8a2e:370:7334"` |
| | `f.PhoneNumber()` | `"+1 (555) 234-5678"` |
| **Text** | `f.Word()` | `"consectetur"` |
| | `f.Words(3)` | `[]string{"voluptas", "ipsum", "amet"}` |
| | `f.Sentence()` | `"Alias consequatur aut perferendis sit."` |
| | `f.Paragraph()` | 4 sentences concatenated into a paragraph |
| | `f.Title()` | `"Senior Enterprise Architect"` |
| **Commerce** | `f.Company()` | `"Global Acme Solutions"` |
| | `f.JobTitle()` | `"Senior Backend Developer"` |
| | `f.Price(10.0, 100.0)` | `49.99` |
| | `f.CurrencyCode()` | `"USD"`, `"EUR"`, `"GBP"` |
| | `f.CreditCardNumber()` | `"4111-2345-6789-0123"` |
| **Location** | `f.Country()` | `"United States"`, `"Germany"` |
| | `f.City()` | `"San Francisco"`, `"Austin"` |
| | `f.StreetAddress()` | `"742 Evergreen Terrace"` |
| | `f.ZipCode()` | `"94107"` |
| **Primitives** | `f.UUID()` | `"f47ac10b-58cc-4372-a567-0e02b2c3d479"` |
| | `f.IntBetween(1, 100)` | `42` |
| | `f.FloatBetween(1.0, 5.0)`| `3.75` |
| | `f.Bool()` | `true` or `false` |
| | `f.Digits(6)` | `"839201"` |
| | `f.Hex(8)` | `"a3f8b01c"` |
| **Dates** | `f.PastDate(1)` | Timestamp in the past year |
| | `f.FutureDate(1)` | Timestamp in the next year |
| | `f.DateTimeBetween(t1, t2)` | Random timestamp between bounds |

### Deterministic Seeded Generation
For reproducible integration tests, pass a fixed seed:

```go
faker1 := factory.NewFaker(12345)
faker2 := factory.NewFaker(12345)

// faker1 and faker2 produce 100% identical outputs in identical order!
name1 := faker1.Name()
name2 := faker2.Name() // name1 == name2
```

---

## 5. Using Factories inside Database Seeders

Combine factories with GoLaravel database seeders (`pkg/database`):

```go
package seeders

import (
    "context"
    "github.com/golaravel/golaravel/database/factories"
)

type DatabaseSeeder struct{}

func (s *DatabaseSeeder) Seed(ctx context.Context, client any) error {
    // 1. Seed 10 admin users
    _, err := factories.UserFactory().
        Count(10).
        State(func(u *factories.UserDef) { u.Role = "admin" }).
        Create(ctx, client)
    if err != nil {
        return err
    }

    // 2. Seed 100 regular members
    _, err = factories.UserFactory().
        Count(100).
        Create(ctx, client)
    return err
}
```

Run seeders with Artisan:
```bash
artisan db:seed
```
